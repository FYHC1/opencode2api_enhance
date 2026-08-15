// S2 429 感知测试：冷却内跳过竞速 / 指数退避序列 / 可见报错文案 / 非 429 回归。
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// s2RacerTransport 实现 contract.Racer + RaceTracker：计数 CandidateClients 调用次数，
// 各候选共用同一 fakeRT（竞速/单发共享响应队列）。HealthyNodeCount 恒高 → 压力≈0，
// 副本数取配置上限，竞速路径与生产同构（S5 阈值不影响本组断言）。
type s2RacerTransport struct {
	candCalls atomic.Int64
	rt        *fakeRT
}

func (t *s2RacerTransport) CandidateClients(_ contract.Tier, _ bool, n int) ([]*http.Client, []string) {
	t.candCalls.Add(1)
	clients := make([]*http.Client, 0, n)
	addrs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		clients = append(clients, &http.Client{Transport: t.rt})
		addrs = append(addrs, fmt.Sprintf("n%d", i))
	}
	return clients, addrs
}

func (t *s2RacerTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return &http.Client{Transport: t.rt}, "n1"
}

func (t *s2RacerTransport) Mark(string, int, error) {}

func (t *s2RacerTransport) HealthyNodeCount() int { return 1000 }

func (t *s2RacerTransport) RaceStarted([]string) {}

func (t *s2RacerTransport) RaceFinished([]string) {}

// newS2SingleVendor 构造单发（非 Racer）厂商，带自定义 429 参数。
func newS2SingleVendor(rt *fakeRT, cooldownSec, backoffBaseMS, backoffCapMS int) *Vendor {
	v := New(Config{
		Transport:              &fakeContractTransport{rt: rt, proxyAddr: "n1"},
		RateLimitCooldownSec:   cooldownSec,
		RateLimitBackoffBaseMS: backoffBaseMS,
		RateLimitBackoffCapMS:  backoffCapMS,
	})
	v.SetSession("1.15.3", "ses_s2", "proj_s2")
	return v
}

func s2Msg() *contract.Message {
	return msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", "")
}

// 429 响应序列：maxRetries=3（默认）时单发路径共 3 次尝试、2 次退避睡眠。
func s2Many429(n int) []fakeResp {
	out := make([]fakeResp, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fakeResp{status: http.StatusTooManyRequests, body: `{"error":"rate_limited"}`})
	}
	return out
}

// Test429CooldownSkipsRace 首 429 后冷却期内第二次请求不调 raceDo（CandidateClients 不增）。
func Test429CooldownSkipsRace(t *testing.T) {
	tr := &s2RacerTransport{rt: &fakeRT{}}
	v := New(Config{
		Transport:              tr,
		RaceCopies:             2,
		RateLimitCooldownSec:   3600,
		RateLimitBackoffBaseMS: 1,
		RateLimitBackoffCapMS:  8,
	})
	v.SetSession("1.15.3", "ses_s2", "proj_s2")

	// 首次调用：第 1 次尝试竞速（2 候选共 2 个 429），后 2 次重试单发各 1 个。
	tr.rt.responses = append(tr.rt.responses, s2Many429(4)...)
	reply, err := v.Chat(context.Background(), s2Msg())
	if reply == nil || reply.Status != http.StatusTooManyRequests {
		t.Fatalf("first call status = %d (err=%v), want 429", reply.Status, err)
	}
	if tr.candCalls.Load() != 1 {
		t.Fatalf("candidate calls after first 429 = %d, want 1（首轮竞速）", tr.candCalls.Load())
	}

	// 冷却期内第二次调用：不竞速，全部单发（3 次尝试，各 1 个响应）。
	tr.rt.mu.Lock()
	tr.rt.responses = append(tr.rt.responses, s2Many429(3)...)
	tr.rt.mu.Unlock()
	reply2, err := v.Chat(context.Background(), s2Msg())
	if reply2 == nil || reply2.Status != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d (err=%v), want 429", reply2.Status, err)
	}
	if tr.candCalls.Load() != 1 {
		t.Fatalf("candidate calls within cooldown = %d, want 1（冷却内单发，不再竞速）", tr.candCalls.Load())
	}
}

// TestRateLimitBackoffSequence 指数退避序列：1s/2s/4s/8s/16s/30s(cap)；自定义 base/cap。
func TestRateLimitBackoffSequence(t *testing.T) {
	v := New(Config{})
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := v.rateLimitBackoff(i); got != w {
			t.Fatalf("backoff(%d) = %v, want %v", i, got, w)
		}
	}
	v2 := New(Config{RateLimitBackoffBaseMS: 50, RateLimitBackoffCapMS: 400})
	want2 := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond}
	for i, w := range want2 {
		if got := v2.rateLimitBackoff(i); got != w {
			t.Fatalf("custom backoff(%d) = %v, want %v", i, got, w)
		}
	}
}

// TestChat429BackoffSleepApplied 实际调用中 429 重试真正退避（10ms/20ms/cap）。
func TestChat429BackoffSleepApplied(t *testing.T) {
	rt := &fakeRT{responses: s2Many429(3)}
	v := newS2SingleVendor(rt, 3600, 10, 20) // base 10ms cap 20ms

	start := time.Now()
	reply, _ := v.Chat(context.Background(), s2Msg())
	elapsed := time.Since(start)
	if reply == nil || reply.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", reply.Status)
	}
	// 3 次尝试 → 2 次退避：10ms + 20ms（cap）。
	if elapsed < 25*time.Millisecond {
		t.Fatalf("elapsed=%v, want >= 25ms（10ms+20ms 退避已生效）", elapsed)
	}
}

// TestChat429BackoffInterruptedByCancel L3：429 退避 sleep 期间取消 ctx——
// 调用立即返回（远小于退避上限 30s），不硬睡。
func TestChat429BackoffInterruptedByCancel(t *testing.T) {
	rt := &fakeRT{responses: s2Many429(3)}
	v := newS2SingleVendor(rt, 3600, 30000, 60000) // 退避 base 30s cap 60s，远超测试窗口

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := v.Chat(ctx, s2Msg())
		done <- err
	}()

	// 等第一个 429 已发出（进入退避 sleep）再取消。
	deadline := time.Now().Add(2 * time.Second)
	for {
		rt.mu.Lock()
		n := len(rt.urls)
		rt.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("上游请求未发出")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled（退避被取消中断）", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("429 退避未感知 ctx 取消，调用未及时返回（硬睡 30s）")
	}
}

// TestChat429VisibleErrorMessage 429 最终失败：非流式/流式两条路径错误体均为中文文案，status=429。
func TestChat429VisibleErrorMessage(t *testing.T) {
	rt := &fakeRT{responses: s2Many429(3)}
	v := newS2SingleVendor(rt, 3600, 1, 8)
	reply, _ := v.Chat(context.Background(), s2Msg())
	if reply == nil || reply.Status != http.StatusTooManyRequests {
		t.Fatalf("chat status = %d, want 429", reply.Status)
	}
	body := string(reply.Body)
	if !strings.Contains(body, "额度") {
		t.Fatalf("非流式错误体无「额度」文案: %s", body)
	}
	var outer map[string]any
	if err := json.Unmarshal(reply.Body, &outer); err != nil {
		t.Fatalf("错误体非 JSON: %v", err)
	}
	errObj, _ := outer["error"].(map[string]any)
	if errObj == nil || errObj["message"] != rateLimitExceededMsg {
		t.Fatalf("error.message 与约定文案不一致: %v", outer)
	}

	// 流式路径同样注入文案。
	rt2 := &fakeRT{responses: s2Many429(3)}
	v2 := newS2SingleVendor(rt2, 3600, 1, 8)
	stream, err := v2.ChatStream(context.Background(), s2Msg())
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if stream.Status != http.StatusTooManyRequests {
		t.Fatalf("stream status = %d, want 429", stream.Status)
	}
	data, _ := io.ReadAll(stream.ReadCloser)
	if !strings.Contains(string(data), "额度") {
		t.Fatalf("流式错误体无「额度」文案: %s", string(data))
	}
}

// TestNon429RaceBehaviorUnchanged 非 429 场景竞速行为不变：首轮竞速、重试单发、冷却不生效。
func TestNon429RaceBehaviorUnchanged(t *testing.T) {
	tr := &s2RacerTransport{rt: &fakeRT{}}
	v := New(Config{Transport: tr, RaceCopies: 2})
	v.SetSession("1.15.3", "ses_s2", "proj_s2")

	// 第一次调用：500（竞速 2 候选）+ 500（重试单发）→ 第三次尝试 200 成功。
	tr.rt.responses = append(tr.rt.responses,
		fakeResp{status: http.StatusInternalServerError, body: `{"error":"boom"}`},
		fakeResp{status: http.StatusInternalServerError, body: `{"error":"boom"}`},
		fakeResp{status: http.StatusOK, body: `{"id":"z","object":"chat.completion","choices":[]}`},
	)
	reply, err := v.Chat(context.Background(), s2Msg())
	if err != nil || reply == nil || reply.Status != http.StatusOK {
		t.Fatalf("first call = %d/%v, want 200", reply.Status, err)
	}
	if tr.candCalls.Load() != 1 {
		t.Fatalf("candidate calls after non-429 + retry = %d, want 1（仅首轮竞速）", tr.candCalls.Load())
	}

	// 未发生 429 → 冷却不生效：下次调用仍竞速。
	tr.rt.mu.Lock()
	tr.rt.responses = append(tr.rt.responses,
		fakeResp{status: http.StatusOK, body: `{"id":"z2","object":"chat.completion","choices":[]}`},
		fakeResp{status: http.StatusOK, body: `{"id":"z2","object":"chat.completion","choices":[]}`},
	)
	tr.rt.mu.Unlock()
	reply2, err := v.Chat(context.Background(), s2Msg())
	if err != nil || reply2 == nil || reply2.Status != http.StatusOK {
		t.Fatalf("second call = %d/%v, want 200", reply2.Status, err)
	}
	if tr.candCalls.Load() != 2 {
		t.Fatalf("candidate calls after 2nd call = %d, want 2（非 429 不影响竞速）", tr.candCalls.Load())
	}
}

// TestRateLimitCooldownConfig 冷却时长 0 → 默认 30s（未 429 时冷却判定为 false。
func TestRateLimitCooldownNo429(t *testing.T) {
	v := New(Config{RateLimitCooldownSec: 0})
	if v.inRateLimitCooldown() {
		t.Fatal("从未 429 不应处于冷却期")
	}
	v.last429.Store(time.Now().Add(-time.Hour).UnixNano())
	if v.inRateLimitCooldown() {
		t.Fatal("距 429 已 1 小时，不应处于冷却期")
	}
}