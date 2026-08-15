package main

// CONC-10 后端低危并发批测试：L5（storedResponses 有界 + 会话 TTL）与 L6（并发流上限）。
// 全部单测（httptest/fake），不触网、不启真实服务。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- L5：storedResponses 有界（淘汰最旧 / 过期 miss） ----

// saveStoredState / restoreStoredState 保存并清空 storedResponses 全局（避免测试间污染）。
func saveStoredState(t *testing.T) {
	t.Helper()
	storedResponsesMu.Lock()
	orig := storedResponses
	origSeq := storedSeq
	origPurge := storedLastPurge
	storedResponses = map[string]storedResponseEntry{}
	storedSeq = 0
	storedLastPurge = time.Time{}
	storedResponsesMu.Unlock()
	t.Cleanup(func() {
		storedResponsesMu.Lock()
		storedResponses = orig
		storedSeq = origSeq
		storedLastPurge = origPurge
		storedResponsesMu.Unlock()
	})
}

// TestStoredResponsesBoundedEvictsOldest 超限淘汰插入序最旧的条目：总数封顶、
// 旧条目被逐、最新条目保留。
func TestStoredResponsesBoundedEvictsOldest(t *testing.T) {
	saveStoredState(t)
	for i := 0; i < maxStoredResponses+3; i++ {
		storeResponseState(map[string]any{"id": fmt.Sprintf("resp_%d", i), "output": []any{"ok"}}, ResponsesAPIRequest{Model: "m"})
	}
	storedResponsesMu.Lock()
	defer storedResponsesMu.Unlock()
	if n := len(storedResponses); n != maxStoredResponses {
		t.Fatalf("store count = %d, want cap %d", n, maxStoredResponses)
	}
	if _, ok := storedResponses["resp_0"]; ok {
		t.Fatal("oldest entry resp_0 should be evicted")
	}
	if _, ok := storedResponses[fmt.Sprintf("resp_%d", maxStoredResponses+2)]; !ok {
		t.Fatal("newest entry should survive")
	}
}

// TestStoredResponsesExpiredMiss 读取路径确定性过期：条目超期 → miss 且被惰性删除。
func TestStoredResponsesExpiredMiss(t *testing.T) {
	saveStoredState(t)
	storeResponseState(map[string]any{"id": "resp_fresh", "output": []any{"x"}}, ResponsesAPIRequest{Model: "m"})
	storedResponsesMu.Lock()
	e := storedResponses["resp_fresh"]
	e.expiresAt = time.Now().Add(-time.Second)
	storedResponses["resp_fresh"] = e
	storedResponsesMu.Unlock()

	if _, ok := loadResponseState("resp_fresh"); ok {
		t.Fatal("expired entry must miss")
	}
	storedResponsesMu.Lock()
	_, alive := storedResponses["resp_fresh"]
	storedResponsesMu.Unlock()
	if alive {
		t.Fatal("expired entry not lazily deleted")
	}
	// 新鲜条目不受影响：重新写入后立即可读。
	storeResponseState(map[string]any{"id": "resp_fresh2", "output": []any{"y"}}, ResponsesAPIRequest{Model: "m"})
	if _, ok := loadResponseState("resp_fresh2"); !ok {
		t.Fatal("fresh entry must remain loadable")
	}
}

// ---- L5：会话 TTL（过期惰性删除 / 滑动续期） ----

// TestSessionExpiryAndRenewal 会话 TTL：有效会话放行并续期；过期会话删除并重定向；
// 未知会话重定向。
func TestSessionExpiryAndRenewal(t *testing.T) {
	oldPwd := adminPassword
	adminPassword = "conc10-test-pass"
	t.Cleanup(func() { adminPassword = oldPwd })

	sessionsMu.Lock()
	orig := sessions
	sessions = map[string]sessionEntry{}
	sessionsMu.Unlock()
	t.Cleanup(func() {
		sessionsMu.Lock()
		sessions = orig
		sessionsMu.Unlock()
	})

	// 有效会话：通过鉴权并滑动续期。
	sessionsMu.Lock()
	sessions["tok_valid"] = sessionEntry{expiresAt: time.Now().Add(time.Minute)}
	sessionsMu.Unlock()

	handler := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "tok_valid"})
	requireAuth(handler)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid session status = %d, want 200", rr.Code)
	}
	sessionsMu.Lock()
	entry := sessions["tok_valid"]
	sessionsMu.Unlock()
	if time.Until(entry.expiresAt) < 20*time.Hour {
		t.Fatalf("session not renewed (sliding): expiresAt=%v", entry.expiresAt)
	}

	// 过期会话：重定向登录并惰性删除。
	sessionsMu.Lock()
	sessions["tok_expired"] = sessionEntry{expiresAt: time.Now().Add(-time.Minute)}
	sessionsMu.Unlock()
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: "session", Value: "tok_expired"})
	requireAuth(handler)(rr2, req2)
	if rr2.Code != http.StatusFound {
		t.Fatalf("expired session status = %d, want 302", rr2.Code)
	}
	sessionsMu.Lock()
	_, still := sessions["tok_expired"]
	sessionsMu.Unlock()
	if still {
		t.Fatal("expired session not lazily deleted")
	}

	// 未知会话：重定向。
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.AddCookie(&http.Cookie{Name: "session", Value: "tok_missing"})
	requireAuth(handler)(rr3, req3)
	if rr3.Code != http.StatusFound {
		t.Fatalf("missing session status = %d, want 302", rr3.Code)
	}
}

// ---- L6：并发流上限 ----

// TestStreamSemaphoreConcurrent 信号量并发正确性：任意时刻持有数 ≤ 上限，
// 全部释放后名额回落（-race 覆盖 channel 并发）。
func TestStreamSemaphoreConcurrent(t *testing.T) {
	var mu sync.Mutex
	held, maxHeld := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 2000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !tryAcquireStream() {
				return // 满员被拒可接受（2000 并发 > 512 上限；拒收数调度相关，不作断言）
			}
			mu.Lock()
			held++
			if held > maxHeld {
				maxHeld = held
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			held--
			mu.Unlock()
			releaseStream()
		}()
	}
	wg.Wait()
	if maxHeld > maxConcurrentStreams {
		t.Fatalf("max concurrent holders = %d, want ≤ %d", maxHeld, maxConcurrentStreams)
	}
	if len(streamSlots) != 0 {
		t.Fatalf("streamSlots not drained after release: %d slots held", len(streamSlots))
	}
}

// TestStreamCapacityHandlerRejectsWhenFull 满员时流式请求 503、非流式不受限、
// 释放后流式恢复，且 defer 释放路径让计数回落（不泄漏名额）。
func TestStreamCapacityHandlerRejectsWhenFull(t *testing.T) {
	// 占满全部名额。
	for i := 0; i < cap(streamSlots); i++ {
		streamSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for len(streamSlots) > 0 {
			<-streamSlots
		}
	})

	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`}, // 非流式
		{status: http.StatusOK, body: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"}, // 流式（释放后）
	})
	_ = transport

	streamBody := `{"model":"primary-model","messages":[{"role":"user","content":"hi"}],"stream":true}`

	// 满员：流式 → 503（未触网）。
	rec := httptest.NewRecorder()
	chatCompletionsHandler(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(streamBody)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stream over-capacity status = %d, want 503", rec.Code)
	}

	// 非流式不受信号量限制。
	rec2 := httptest.NewRecorder()
	chatCompletionsHandler(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("non-stream status = %d, want 200", rec2.Code)
	}

	// 释放 1 个名额 → 流式恢复；完成后 defer 释放，计数回落。
	<-streamSlots
	before := len(streamSlots)
	rec3 := httptest.NewRecorder()
	chatCompletionsHandler(rec3, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(streamBody)))
	if rec3.Code != http.StatusOK {
		t.Fatalf("stream after release status = %d, want 200", rec3.Code)
	}
	if after := len(streamSlots); after != before {
		t.Fatalf("stream slot not released by defer: before=%d after=%d", before, after)
	}
}