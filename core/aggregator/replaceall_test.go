package aggregator

import (
	"context"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// stubVendor 最小契约桩（ReplaceAll 测试用）。
type stubVendor struct{ id string }

func (s *stubVendor) ID() string                                           { return s.id }
func (s *stubVendor) Name() string                                         { return s.id }
func (s *stubVendor) ListModels(context.Context) ([]contract.Model, error) { return nil, nil }
func (s *stubVendor) IsFree(string) bool                                   { return true }
func (s *stubVendor) Chat(context.Context, *contract.Message) (*contract.Reply, error) {
	return nil, nil
}
func (s *stubVendor) ChatStream(context.Context, *contract.Message) (*contract.Stream, error) {
	return nil, nil
}
func (s *stubVendor) Auth(*http.Request) string       { return "" }
func (s *stubVendor) ErrSemantics() contract.ErrRules { return contract.ErrRules{} }
func (s *stubVendor) Health() contract.VendorHealth   { return contract.VendorHealth{} }

// TestReplaceAll：运行时替换厂商集合后 Vendors 反映新集合，
// 且旧目录/倒排索引立即清空（替换后、Refresh 前不保留已移除源的路由）。
func TestReplaceAll(t *testing.T) {
	agg := New()
	agg.Register(&stubVendor{id: "a"})
	agg.mu.Lock()
	agg.catalog = []contract.Model{{ID: "m1", Provider: "a", Free: true}}
	agg.providersByModel = map[string][]string{"m1": {"a"}}
	agg.mu.Unlock()

	agg.ReplaceAll([]contract.Vendor{&stubVendor{id: "b"}})

	if vs := agg.Vendors(); len(vs) != 1 || vs[0].ID() != "b" {
		t.Fatalf("vendors after replace = %v", vs)
	}
	if cat := agg.Catalog(); len(cat) != 0 {
		t.Fatalf("catalog should be cleared, got %v", cat)
	}
	if p := agg.ProvidersOf("m1"); len(p) != 0 {
		t.Fatalf("providersByModel should be cleared, got %v", p)
	}
}
