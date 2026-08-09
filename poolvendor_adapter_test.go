package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	chatRouter "github.com/6Kmfi6HP/opencode2api/core/router"
)

// fakePoolVendor 实现 contract.Vendor + contract.PoolVendor：
// 记录 EnsureReady 是否被请求路径调用（账号池"无号自动注册"的触发点）。
type fakePoolVendor struct {
	id          string
	ensureErr   error
	ensureCalls int
	chatCalled  bool
}

func (f *fakePoolVendor) ID() string   { return f.id }
func (f *fakePoolVendor) Name() string { return f.id }
func (f *fakePoolVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	return []contract.Model{{ID: "swe-1-6-slow", Provider: f.id, Free: true}}, nil
}
func (f *fakePoolVendor) IsFree(string) bool { return true }
func (f *fakePoolVendor) Chat(_ context.Context, _ *contract.Message) (*contract.Reply, error) {
	f.chatCalled = true
	return &contract.Reply{Status: http.StatusOK, Body: []byte(`{"id":"ok","object":"chat.completion","choices":[]}`)}, nil
}
func (f *fakePoolVendor) ChatStream(_ context.Context, _ *contract.Message) (*contract.Stream, error) {
	f.chatCalled = true
	return &contract.Stream{ReadCloser: io.NopCloser(strings.NewReader("data: ok\n\n")), Status: http.StatusOK}, nil
}
func (f *fakePoolVendor) Auth(*http.Request) string { return "" }
func (f *fakePoolVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{Switchable: []int{http.StatusTooManyRequests}}
}
func (f *fakePoolVendor) Health() contract.VendorHealth { return contract.VendorHealth{Available: true} }

// PoolVendor 部分
func (f *fakePoolVendor) EnsureReady(context.Context) error { f.ensureCalls++; return f.ensureErr }
func (f *fakePoolVendor) PoolStatus() contract.PoolStatus   { return contract.PoolStatus{} }
func (f *fakePoolVendor) Acquire() (contract.AcctID, error) { return "acct-1", nil }
func (f *fakePoolVendor) Release(contract.AcctID)           {}

func installPoolRouter(t *testing.T, v contract.Vendor) {
	t.Helper()
	agg := aggregator.New()
	agg.Register(v)
	_ = agg.Refresh(context.Background())
	old := chatRouterVar
	chatRouterVar = chatRouter.New(agg, nil, "")
	t.Cleanup(func() { chatRouterVar = old })
}

// TestPoolVendorEnsureReadyInvoked：请求路径（适配层）必须在池型厂商 Chat 之前调用
// EnsureReady——账号池"无号自动注册"的触发点（P3 全链路接线）。
func TestPoolVendorEnsureReadyInvoked(t *testing.T) {
	v := &fakePoolVendor{id: "pool"}
	installPoolRouter(t, v)

	body, status, _, _, err := callOpenCodeAPI([]byte(`{"model":"swe-1-6-slow","messages":[]}`), "swe-1-6-slow", UpstreamAuth{Mode: AuthRoutePublic})
	if err != nil {
		t.Fatalf("callOpenCodeAPI: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if v.ensureCalls != 1 {
		t.Fatalf("EnsureReady calls = %d, want 1 (adapter must trigger pool readiness)", v.ensureCalls)
	}
	if !v.chatCalled {
		t.Fatal("vendor Chat was not called after EnsureReady")
	}
	if !strings.Contains(string(body), "chat.completion") {
		t.Fatalf("body = %s, want chat completion", string(body))
	}
}

// TestPoolVendorEnsureReadyFailureStopsRequest：EnsureReady 失败（如自动注册失败）时
// 请求必须中止，不得继续 Chat，且适配层返回错误（handler 侧归一化为 502）。
func TestPoolVendorEnsureReadyFailureStopsRequest(t *testing.T) {
	v := &fakePoolVendor{id: "pool", ensureErr: errors.New("windsurf: 无可用账号且自动注册失败: boom")}
	installPoolRouter(t, v)

	body, status, _, _, err := callOpenCodeAPI([]byte(`{"model":"swe-1-6-slow","messages":[]}`), "swe-1-6-slow", UpstreamAuth{Mode: AuthRoutePublic})
	if err == nil {
		t.Fatal("err = nil, want EnsureReady failure to abort request")
	}
	if !strings.Contains(err.Error(), "无可用账号") {
		t.Fatalf("err = %v, want 无可用账号 message", err)
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0 (no upstream reply)", status)
	}
	if v.chatCalled {
		t.Fatal("vendor Chat must NOT be called when EnsureReady fails")
	}
	if v.ensureCalls != 1 {
		t.Fatalf("EnsureReady calls = %d, want 1", v.ensureCalls)
	}
	if len(body) != 0 {
		t.Fatalf("body = %s, want empty", string(body))
	}
}
