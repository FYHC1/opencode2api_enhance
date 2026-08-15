package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	chatRouter "github.com/6Kmfi6HP/opencode2api/core/router"
)

// scriptedVendor 是可脚本化的测试厂商（实现 contract.Vendor）。
// 非 2xx 返回按真实厂商语义带 error（供适配层面板/失败判定）。
type scriptedVendor struct {
	id      string
	name    string
	models  []string
	replies []contract.Reply // 按序返回；用尽后循环最后一条
	mu      sync.Mutex
	calls   int
}

func (s *scriptedVendor) ID() string   { return s.id }
func (s *scriptedVendor) Name() string { return s.name }
func (s *scriptedVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	out := make([]contract.Model, 0, len(s.models))
	for _, m := range s.models {
		out = append(out, contract.Model{ID: m, Provider: s.id, Free: true})
	}
	return out, nil
}
func (s *scriptedVendor) IsFree(_ string) bool { return true }

func (s *scriptedVendor) next() contract.Reply {
	if len(s.replies) == 0 {
		return contract.Reply{Status: http.StatusInternalServerError}
	}
	if s.calls > 0 && s.calls >= len(s.replies) {
		return s.replies[len(s.replies)-1]
	}
	r := s.replies[s.calls]
	return r
}

func (s *scriptedVendor) Chat(_ context.Context, _ *contract.Message) (*contract.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	r := s.next()
	if r.Status >= 200 && r.Status < 300 {
		return &r, nil
	}
	return &r, errors.New("upstream error")
}

func (s *scriptedVendor) ChatStream(_ context.Context, _ *contract.Message) (*contract.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	r := s.next()
	if r.Status >= 200 && r.Status < 300 {
		return &contract.Stream{ReadCloser: io.NopCloser(strings.NewReader(string(r.Body))), Status: r.Status}, nil
	}
	return &contract.Stream{ReadCloser: io.NopCloser(strings.NewReader(string(r.Body))), Status: r.Status}, errors.New("upstream error")
}

func (s *scriptedVendor) Auth(_ *http.Request) string { return "" }
func (s *scriptedVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{Switchable: []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable}}
}
func (s *scriptedVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{Available: true}
}

func installFailoverRouter(t *testing.T, vendors ...contract.Vendor) *chatRouter.Router {
	t.Helper()
	agg := aggregator.New()
	for _, v := range vendors {
		agg.Register(v)
	}
	if err := agg.Refresh(context.Background()); err != nil {
		t.Fatalf("agg.Refresh: %v", err)
	}
	return chatRouter.New(agg, nil, "")
}

func TestAdapterVendorFailover(t *testing.T) {
	v1 := &scriptedVendor{id: "a1", name: "A1", models: []string{"m-shared"}, replies: []contract.Reply{
		{Status: http.StatusTooManyRequests, Body: []byte(`{"error":"throttled"}`)},
	}}
	v2 := &scriptedVendor{id: "b1", name: "B1", models: []string{"m-shared"}, replies: []contract.Reply{
		{Status: http.StatusOK, Body: []byte(`{"id":"ok","object":"chat.completion","choices":[]}`)},
	}}

	oldRouter := chatRouterVar
	chatRouterVar = installFailoverRouter(t, v1, v2)
	t.Cleanup(func() { chatRouterVar = oldRouter })

	body, status, _, _, err := callOpenCodeAPI(context.Background(), []byte(`{"model":"m-shared","messages":[]}`), "m-shared", UpstreamAuth{Mode: AuthRoutePublic})
	if err != nil {
		t.Fatalf("callOpenCodeAPI: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (failover to b1)", status)
	}
	if string(body) != `{"id":"ok","object":"chat.completion","choices":[]}` {
		t.Fatalf("body = %s, want b1 success body", string(body))
	}
	if v1.calls != 1 || v2.calls != 1 {
		t.Fatalf("calls a1=%d b1=%d, want 1/1", v1.calls, v2.calls)
	}
}

func TestAdapterNoFailoverOnNonSwitchable(t *testing.T) {
	v1 := &scriptedVendor{id: "a2", name: "A2", models: []string{"m-shared"}, replies: []contract.Reply{
		{Status: http.StatusForbidden, Body: []byte(`{"error":"forbidden"}`)},
	}}
	v2 := &scriptedVendor{id: "b2", name: "B2", models: []string{"m-shared"}, replies: []contract.Reply{
		{Status: http.StatusOK, Body: []byte(`{"id":"ok","object":"chat.completion","choices":[]}`)},
	}}

	oldRouter := chatRouterVar
	chatRouterVar = installFailoverRouter(t, v1, v2)
	t.Cleanup(func() { chatRouterVar = oldRouter })

	body, status, _, _, err := callOpenCodeAPI(context.Background(), []byte(`{"model":"m-shared","messages":[]}`), "m-shared", UpstreamAuth{Mode: AuthRoutePublic})
	if err == nil {
		t.Fatal("error = nil, want upstream error (403 not switchable)")
	}
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (no failover)", status)
	}
	if string(body) != `{"error":"forbidden"}` {
		t.Fatalf("body = %s, want v1 403 body", string(body))
	}
	if v2.calls != 0 {
		t.Fatalf("b2 calls = %d, want 0 (must not be tried)", v2.calls)
	}
}
