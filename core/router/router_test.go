package router

import (
	"context"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// 轻量测试厂商（只用 ID/Name/ListModels）。
type stubVendor struct {
	id   string
	name string
	mods []string
}

func (s *stubVendor) ID() string   { return s.id }
func (s *stubVendor) Name() string { return s.name }
func (s *stubVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	out := make([]contract.Model, 0, len(s.mods))
	for _, m := range s.mods {
		out = append(out, contract.Model{ID: m, Provider: s.id, Free: true})
	}
	return out, nil
}
func (s *stubVendor) IsFree(_ string) bool { return true }
func (s *stubVendor) Chat(context.Context, *contract.Message) (*contract.Reply, error) {
	return nil, nil
}
func (s *stubVendor) ChatStream(context.Context, *contract.Message) (*contract.Stream, error) {
	return nil, nil
}
func (s *stubVendor) Auth(_ *http.Request) string { return "" }
func (s *stubVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{}
}
func (s *stubVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{Available: true}
}

func buildRegistry(modelMap map[string]string, defaultID string) *Router {
	agg := aggregator.New()
	agg.Register(&stubVendor{id: "opencode", name: "OpenCode", mods: []string{"m-shared", "m-free", "m-opc"}})
	agg.Register(&stubVendor{id: "windsurf", name: "Windsurf", mods: []string{"m-shared", "swe-1-6-slow"}})
	_ = agg.Refresh(context.Background())
	return New(agg, modelMap, defaultID)
}

func TestResolvePriority(t *testing.T) {
	r := buildRegistry(map[string]string{"swe-1-6-slow": "windsurf", "m-opc": "opencode"}, "opencode")

	// 1) 映射精确命中
	if got := r.Resolve("swe-1-6-slow"); got == nil || got.ID() != "windsurf" {
		t.Fatalf("map hit: got %v, want windsurf", got)
	}
	// 2) 目录提供者 + 共享模型默认取已注册顺序第一个（opencode 先注册）
	if got := r.Resolve("m-shared"); got == nil || got.ID() != "opencode" {
		t.Fatalf("directory candidate: got %v, want opencode", got)
	}
	// 3) 兜底默认厂商（本模型无人提供）
	if got := r.Resolve("m-fallback"); got == nil || got.ID() != "opencode" {
		t.Fatalf("default fallback: got %v, want opencode", got)
	}
}

func TestCandidatesOrder(t *testing.T) {
	r := buildRegistry(nil, "opencode")
	cs := r.Candidates("m-shared")
	if len(cs) != 2 {
		t.Fatalf("candidates(%q) length = %d, want 2", "m-shared", len(cs))
	}
	if cs[0].ID() != "opencode" || cs[1].ID() != "windsurf" {
		t.Fatalf("candidates order = [%s %s], want [opencode windsurf]", cs[0].ID(), cs[1].ID())
	}
}

func TestCandidatesDedupAndDefault(t *testing.T) {
	r := buildRegistry(map[string]string{"m-opc": "opencode"}, "windsurf")
	cs := r.Candidates("m-opc")
	// 映射命中 + 目录命中（同一厂商）→ 去重后只出现一次
	if len(cs) != 1 || cs[0].ID() != "opencode" {
		t.Fatalf("dedup failed: %v", cs)
	}
	// 无人提供的模型 → 兜底 windsurf
	cs = r.Candidates("m-unknown")
	if len(cs) != 1 || cs[0].ID() != "windsurf" {
		t.Fatalf("default candidate = %v, want windsurf", cs)
	}
}
