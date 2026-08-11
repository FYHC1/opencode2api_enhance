package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	chatRouter "github.com/6Kmfi6HP/opencode2api/core/router"
)

// brokenChatVendor 模拟"上游未接线/传输失败"：Chat/ChatStream 返回 (nil, err)，
// 即 status==0 场景（如 windsurf Chatter 未接线）。历史上会让 handler
// WriteHeader(0) 触发 net/http panic（500 internal error: handler panic）。
type brokenChatVendor struct{ id string }

func (b *brokenChatVendor) ID() string   { return b.id }
func (b *brokenChatVendor) Name() string { return b.id }
func (b *brokenChatVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	return []contract.Model{{ID: "swe-1-6-slow", Provider: b.id, Free: true}}, nil
}
func (b *brokenChatVendor) IsFree(string) bool { return true }
func (b *brokenChatVendor) Chat(context.Context, *contract.Message) (*contract.Reply, error) {
	return nil, errors.New("broken: no upstream status")
}
func (b *brokenChatVendor) ChatStream(context.Context, *contract.Message) (*contract.Stream, error) {
	return nil, errors.New("broken: no upstream status")
}
func (b *brokenChatVendor) Auth(*http.Request) string { return "" }
func (b *brokenChatVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{Switchable: []int{http.StatusUnauthorized, http.StatusTooManyRequests}}
}
func (b *brokenChatVendor) Health() contract.VendorHealth { return contract.VendorHealth{Available: true} }

// installBrokenRouter 把路由器替换为单一故障厂商（返回 nil 流 + error，status==0）。
func installBrokenRouter(t *testing.T) {
	t.Helper()
	agg := aggregator.New()
	agg.Register(&brokenChatVendor{id: "broken"})
	_ = agg.Refresh(context.Background())
	old := chatRouterVar
	chatRouterVar = chatRouter.New(agg, nil, "broken")
	t.Cleanup(func() { chatRouterVar = old })
}

// TestWriteHeaderZeroStatusNoPanic：上游返回 status==0（无 HTTP 状态码）时，
// /v1/chat/completions 必须返回 502 而非 panic（历史 bug：WriteHeader(0)）。
func TestWriteHeaderZeroStatusNoPanic(t *testing.T) {
	installBrokenRouter(t)

	oldCallLogPath := callLogPath
	callLogPath = filepath.Join(t.TempDir(), "call_log.jsonl")
	t.Cleanup(func() { callLogPath = oldCallLogPath })

	for _, tc := range []struct {
		name   string
		body   string
		want   int
	}{
		{"stream", `{"model":"swe-1-6-slow","messages":[],"stream":true}`, http.StatusBadGateway},
		{"non-stream", `{"model":"swe-1-6-slow","messages":[]}`, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			// 直接调 handler：若仍存在 WriteHeader(0) panic，测试进程会直接崩（recover 日志），
			// 这里用 httptest.Recorder + recover 断言不 panic。
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("handler panicked: %v", r)
					}
				}()
				chatCompletionsHandler(rec, req)
			}()
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (no WriteHeader(0) panic)", rec.Code, tc.want)
			}
			if !strings.Contains(rec.Body.String(), "upstream error") {
				t.Fatalf("body = %q, want upstream error JSON", rec.Body.String())
			}
		})
	}
}
