package aggregator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// fakeVendor 是可编程测试厂商。
type fakeVendor struct {
	id   string
	name string
	mods []contract.Model
}

func (f *fakeVendor) ID() string   { return f.id }
func (f *fakeVendor) Name() string { return f.name }
func (f *fakeVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	return append([]contract.Model(nil), f.mods...), nil
}
func (f *fakeVendor) IsFree(_ string) bool { return false }
func (f *fakeVendor) Chat(_ context.Context, _ *contract.Message) (*contract.Reply, error) {
	return nil, nil
}
func (f *fakeVendor) ChatStream(_ context.Context, _ *contract.Message) (*contract.Stream, error) {
	return nil, nil
}
func (f *fakeVendor) Auth(_ *http.Request) string { return "" }
func (f *fakeVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{}
}
func (f *fakeVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{Available: true}
}

var errBroken = errors.New("broken vendor")

// brokenVendor 的目录拉取永远失败，用于验证聚合隔离。
type brokenVendor struct{}

func (b *brokenVendor) ID() string   { return "broken" }
func (b *brokenVendor) Name() string { return "Broken" }
func (b *brokenVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	return nil, errBroken
}
func (b *brokenVendor) IsFree(_ string) bool { return false }
func (b *brokenVendor) Chat(_ context.Context, _ *contract.Message) (*contract.Reply, error) {
	return nil, errBroken
}
func (b *brokenVendor) ChatStream(_ context.Context, _ *contract.Message) (*contract.Stream, error) {
	return nil, errBroken
}
func (b *brokenVendor) Auth(_ *http.Request) string { return "" }
func (b *brokenVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{}
}
func (b *brokenVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{}
}

func TestAggregate(t *testing.T) {
	a := New()
	a.Register(&fakeVendor{
		id:   "opencode",
		name: "OpenCode",
		mods: []contract.Model{
			{ID: "deepseek-v4-flash-free", Provider: "opencode", Free: true},
			{ID: "big-pickle", Provider: "opencode", Free: true},
			{ID: "glm-5.2", Provider: "opencode", Free: false},
		},
	})
	a.Register(&fakeVendor{
		id:   "windsurf",
		name: "Windsurf",
		mods: []contract.Model{
			{ID: "swe-1-6-slow", Provider: "windsurf", Free: true},
		},
	})

	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if cat := a.Catalog(); len(cat) != 4 {
		t.Fatalf("catalog length = %d, want 4", len(cat))
	}
	if free := a.FreeModels(); len(free) != 3 {
		t.Fatalf("free models = %d, want 3", len(free))
	}
	if !a.HasModel("windsurf", "swe-1-6-slow") {
		t.Fatal("HasModel(windsurf, swe-1-6-slow) should be true")
	}
	if a.HasModel("opencode", "swe-1-6-slow") {
		t.Fatal("HasModel(opencode, swe-1-6-slow) should be false")
	}
}

func TestAggregatorRefreshIsolation(t *testing.T) {
	a := New()
	a.Register(&fakeVendor{id: "ok", name: "OK", mods: []contract.Model{{ID: "x1", Provider: "ok", Free: true}}})
	a.Register(&brokenVendor{})

	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh must tolerate a broken vendor: %v", err)
	}
	if len(a.Catalog()) != 1 {
		t.Fatalf("catalog = %d, want 1 (broken vendor isolated)", len(a.Catalog()))
	}
}

func TestProvidersOfIndex(t *testing.T) {
	a := New()
	a.Register(&fakeVendor{
		id: "v1",
		mods: []contract.Model{
			{ID: "shared", Provider: "v1", Free: true},
			{ID: "only-v1", Provider: "v1", Free: true},
		},
	})
	a.Register(&fakeVendor{
		id: "v2",
		mods: []contract.Model{
			{ID: "shared", Provider: "v2", Free: true},
			{ID: "only-v2", Provider: "v2", Free: true},
		},
	})
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// 同名模型两个厂商都提供，顺序按目录出现（注册序）。
	if got := a.ProvidersOf("shared"); len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Fatalf("ProvidersOf(shared) = %#v, want [v1 v2]", got)
	}
	if got := a.ProvidersOf("only-v1"); len(got) != 1 || got[0] != "v1" {
		t.Fatalf("ProvidersOf(only-v1) = %#v, want [v1]", got)
	}
	if got := a.ProvidersOf("nope"); len(got) != 0 {
		t.Fatalf("ProvidersOf(nope) = %#v, want empty", got)
	}
	if !a.HasModel("v1", "shared") || !a.HasModel("v2", "shared") {
		t.Fatal("HasModel(shared) should be true for both providers")
	}
	if a.HasModel("v2", "only-v1") {
		t.Fatal("HasModel(v2, only-v1) should be false")
	}
}
