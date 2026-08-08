// 适配层消息种子化测试（P3-B7 配套）：非 opencode 厂商（windsurf 等）
// 必须能从 contract.Message.Messages 拿到归一化对话，而非「hello」兜底。
package main

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// recorderVendor 记录收到的 contract.Message（复用 adapter_failover_test.go 的 scriptedVendor 行为）。
type recorderVendor struct {
	*scriptedVendor
	mu  sync.Mutex
	got []*contract.Message
}

func (r *recorderVendor) Chat(_ context.Context, m *contract.Message) (*contract.Reply, error) {
	r.mu.Lock()
	r.got = append(r.got, m)
	r.mu.Unlock()
	return r.scriptedVendor.Chat(context.Background(), m)
}

func (r *recorderVendor) ChatStream(_ context.Context, m *contract.Message) (*contract.Stream, error) {
	r.mu.Lock()
	r.got = append(r.got, m)
	r.mu.Unlock()
	return r.scriptedVendor.ChatStream(context.Background(), m)
}

func TestRawBodyToContractMessages(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"你好"},
		{"role":"assistant","content":[{"type":"text","text":"x"}]},
		{"role":""}
	]}`)
	msgs := rawBodyToContractMessages(body)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "你好" {
		t.Fatalf("msg0 = %+v", msgs[0])
	}
	if arr, ok := msgs[1].Content.([]any); !ok || len(arr) != 1 {
		t.Fatalf("msg1 content should stay array: %+v", msgs[1].Content)
	}
}

// TestAdapterSeedsContractMessages：非流式与流式都须把请求消息种子化给厂商。
func TestAdapterSeedsContractMessages(t *testing.T) {
	base := &scriptedVendor{
		id: "rec", name: "Rec", models: []string{"m-shared"},
		replies: []contract.Reply{{Status: http.StatusOK, Body: []byte(`{"ok":true}`)}},
	}
	rec := &recorderVendor{scriptedVendor: base}
	oldRouter := chatRouterVar
	chatRouterVar = installFailoverRouter(t, rec)
	t.Cleanup(func() { chatRouterVar = oldRouter })

	body := []byte(`{"model":"m-shared","messages":[{"role":"user","content":"continue please"}]}`)

	if _, status, _, _, err := callOpenCodeAPI(body, "m-shared", UpstreamAuth{Mode: AuthRoutePublic}); err != nil || status != http.StatusOK {
		t.Fatalf("callOpenCodeAPI: %d %v", status, err)
	}
	rec.mu.Lock()
	got := append([]*contract.Message(nil), rec.got...)
	rec.mu.Unlock()
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Content != "continue please" {
		t.Fatalf("non-stream messages = %+v", got)
	}

	rec.mu.Lock()
	rec.got = nil
	rec.mu.Unlock()
	if _, status, _, _, err := callOpenCodeAPIStream(body, "m-shared", UpstreamAuth{Mode: AuthRoutePublic}); err != nil || status != http.StatusOK {
		t.Fatalf("callOpenCodeAPIStream: %d %v", status, err)
	}
	rec.mu.Lock()
	got = append([]*contract.Message(nil), rec.got...)
	rec.mu.Unlock()
	if len(got) != 1 || len(got[0].Messages) != 1 {
		t.Fatalf("stream messages = %+v", got)
	}
}
