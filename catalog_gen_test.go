package main

// CONC-2（M8）目录同步代际：syncVendorState / seedVendorCatalog 仅在目录
// 代际变化时执行 SetCatalog；首次请求（缺项）强制同步。

import (
	"context"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/vendors/opencode"
)

// catalogFakeVendor 计数 SetCatalog 的假厂商（seedVendorCatalog 路径用）。
type catalogFakeVendor struct {
	id       string
	models   []contract.Model
	setCalls int
	lastCat  []contract.Model
}

func (f *catalogFakeVendor) ID() string   { return f.id }
func (f *catalogFakeVendor) Name() string { return f.id }
func (f *catalogFakeVendor) ListModels(context.Context) ([]contract.Model, error) {
	return append([]contract.Model(nil), f.models...), nil
}
func (f *catalogFakeVendor) IsFree(string) bool { return false }
func (f *catalogFakeVendor) Chat(context.Context, *contract.Message) (*contract.Reply, error) {
	return &contract.Reply{Status: 200, Body: []byte(`{}`)}, nil
}
func (f *catalogFakeVendor) ChatStream(context.Context, *contract.Message) (*contract.Stream, error) {
	return &contract.Stream{Status: 200}, nil
}
func (f *catalogFakeVendor) Auth(*http.Request) string { return "" }
func (f *catalogFakeVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{}
}
func (f *catalogFakeVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{}
}
func (f *catalogFakeVendor) SetCatalog(models []contract.Model) {
	f.setCalls++
	f.lastCat = append([]contract.Model(nil), models...)
}

// snapshotCatalogGen 快照并重置目录代际相关全局状态（测试间隔离，
// 重置到"从未同步"初始态）。
func snapshotCatalogGen(t *testing.T) {
	t.Helper()
	oldAgg := globalAgg
	oldGen := catalogGen.Load()
	oldLastGen := vendorCatalogLastGen
	modelMu.RLock()
	oldZen, oldGo, oldLoaded := modelsCache, goModelsCache, modelsLoaded
	modelMu.RUnlock()

	catalogGen.Store(0)
	vendorCatalogLastGenMu.Lock()
	vendorCatalogLastGen = map[string]int64{}
	vendorCatalogLastGenMu.Unlock()

	t.Cleanup(func() {
		globalAgg = oldAgg
		catalogGen.Store(oldGen)
		vendorCatalogLastGenMu.Lock()
		vendorCatalogLastGen = oldLastGen
		vendorCatalogLastGenMu.Unlock()
		modelMu.Lock()
		modelsCache, goModelsCache, modelsLoaded = oldZen, oldGo, oldLoaded
		modelMu.Unlock()
	})
}

// 目录代际未变：连续同步 SetCatalog 只执行一次；代际变化后下一次执行。
func TestSyncVendorStateGenerationSkip(t *testing.T) {
	snapshotCatalogGen(t)

	v := opencode.New(opencode.Config{ID: "opencode"})

	modelMu.Lock()
	modelsCache = []ModelInfo{{ID: "m-a"}}
	goModelsCache = nil
	modelMu.Unlock()

	syncVendorState(v)
	if got := v.Cache(context.Background()); len(got) != 1 || got[0].ID != "m-a" {
		t.Fatalf("首次同步后缓存 = %+v, want [m-a]", got)
	}

	// 目录未变（代际未增）：modelsCache 有差异但同步应被跳过。
	modelMu.Lock()
	modelsCache = []ModelInfo{{ID: "m-a"}, {ID: "m-b"}}
	modelMu.Unlock()
	syncVendorState(v)
	if got := v.Cache(context.Background()); len(got) != 1 {
		t.Fatalf("代际未变应跳过 SetCatalog，缓存 = %+v, want 仍为 [m-a]", got)
	}

	// 目录变更（代际+1）：下一次同步执行 SetCatalog。
	catalogGen.Add(1)
	syncVendorState(v)
	if got := v.Cache(context.Background()); len(got) != 2 || got[1].ID != "m-b" {
		t.Fatalf("代际变化后应重新 SetCatalog，缓存 = %+v, want [m-a m-b]", got)
	}
}

// chatViaVendor 连续两次（目录未变）SetCatalog 只执行一次；目录变更后下一次执行。
func TestChatViaVendorCatalogGenerationSkip(t *testing.T) {
	snapshotCatalogGen(t)

	fake := &catalogFakeVendor{
		id: "fake",
		models: []contract.Model{
			{ID: "f1", Provider: "fake"},
			{ID: "f2", Provider: "fake"},
		},
	}
	agg := aggregator.New()
	agg.Register(fake)
	globalAgg = agg

	// refreshModelCatalog：拉取目录（假厂商）→ 写入缓存 → 代际+1。
	refreshModelCatalog()
	if catalogGen.Load() != 1 {
		t.Fatalf("refreshModelCatalog 后代际 = %d, want 1", catalogGen.Load())
	}

	auth := UpstreamAuth{Mode: AuthRoutePublic}
	body := []byte(`{"model":"f1","messages":[{"role":"user","content":"hi"}]}`)

	if _, err := chatViaVendor(context.Background(), fake, body, "f1", auth); err != nil {
		t.Fatalf("chatViaVendor: %v", err)
	}
	if fake.setCalls != 1 {
		t.Fatalf("首次请求 SetCatalog 次数 = %d, want 1", fake.setCalls)
	}

	// 目录未变：第二次请求跳过 SetCatalog。
	if _, err := chatViaVendor(context.Background(), fake, body, "f1", auth); err != nil {
		t.Fatalf("chatViaVendor: %v", err)
	}
	if fake.setCalls != 1 {
		t.Fatalf("目录未变 SetCatalog 次数 = %d, want 仍为 1", fake.setCalls)
	}

	// 目录变更（新增模型 + 刷新）→ 下一次请求执行 SetCatalog。
	fake.models = append(fake.models, contract.Model{ID: "f3", Provider: "fake"})
	refreshModelCatalog()
	if _, err := chatViaVendor(context.Background(), fake, body, "f1", auth); err != nil {
		t.Fatalf("chatViaVendor: %v", err)
	}
	if fake.setCalls != 2 {
		t.Fatalf("目录变更后 SetCatalog 次数 = %d, want 2", fake.setCalls)
	}
	found := false
	for _, m := range fake.lastCat {
		if m.ID == "f3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("目录变更后的 SetCatalog 未包含新模型, got %+v", fake.lastCat)
	}
}
