package contract

import (
	"context"
	"net/http"
	"testing"
)

// 为测试注册两个假厂商，验证注册表行为。
func init() {
	Register("fake-a", func(spec ProviderSpec) (Vendor, error) {
		return &fakeVendor{id: "a", name: spec.Name}, nil
	})
	Register("fake-b", func(spec ProviderSpec) (Vendor, error) {
		return &fakeVendor{id: "b", name: spec.Name}, nil
	})
}

type fakeVendor struct{ id, name string }

func (f *fakeVendor) ID() string                         { return f.id }
func (f *fakeVendor) Name() string                       { return f.name }
func (f *fakeVendor) ListModels(ctx context.Context) ([]Model, error) { return nil, nil }
func (f *fakeVendor) IsFree(string) bool                 { return false }
func (f *fakeVendor) Chat(ctx context.Context, m *Message) (*Reply, error) { return nil, nil }
func (f *fakeVendor) ChatStream(ctx context.Context, m *Message) (*Stream, error) { return nil, nil }
func (f *fakeVendor) Auth(*http.Request) string          { return "" }
func (f *fakeVendor) ErrSemantics() ErrRules             { return ErrRules{} }
func (f *fakeVendor) Health() VendorHealth               { return VendorHealth{Available: true} }

// TestRegistryAutoDiscover 验证注册表核心能力：扩增即生效——
// 新厂商 init() 注册后，RegisteredTypes/Create 自动可见，无需配置声明。
func TestRegistryAutoDiscover(t *testing.T) {
	types := RegisteredTypes()
	seen := map[string]bool{}
	for _, tt := range types {
		seen[tt] = true
	}
	if !seen["fake-a"] || !seen["fake-b"] {
		t.Fatalf("registered types = %v, want fake-a & fake-b", types)
	}
	v, err := Create("fake-b", ProviderSpec{Name: "B 厂商"})
	if err != nil {
		t.Fatalf("Create(fake-b): %v", err)
	}
	if v.Name() != "B 厂商" {
		t.Fatalf("created vendor name = %q, want override", v.Name())
	}
	// 未注册类型报错
	if _, err := Create("no-such", ProviderSpec{}); err == nil {
		t.Fatal("Create(no-such) should error")
	}
}
