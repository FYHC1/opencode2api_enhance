package aggregator

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

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

// enteringVendor 可编程时序厂商：ListModels 开始时关闭 entered（如果有），
// release 非 nil 时阻塞到关闭或 ctx 取消——用于断言并发与饥饿场景。
type enteringVendor struct {
	fakeVendor
	entered chan struct{}
	release chan struct{}
}

func (v *enteringVendor) ListModels(ctx context.Context) ([]contract.Model, error) {
	if v.entered != nil {
		close(v.entered)
	}
	if v.release != nil {
		select {
		case <-v.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return append([]contract.Model(nil), v.mods...), nil
}

// TestRefreshFastVendorDoesNotWaitForSlow 快厂商不等慢厂商：
// 慢厂商阻塞期间快厂商必须已开始拉取（并行过滤）；串行实现中快厂商
// 要等慢厂商返回才开始。
func TestRefreshFastVendorDoesNotWaitForSlow(t *testing.T) {
	a := New()
	enteredSlow := make(chan struct{})
	enteredFast := make(chan struct{})
	releaseSlow := make(chan struct{})
	a.Register(&enteringVendor{
		fakeVendor: fakeVendor{id: "slow", name: "Slow", mods: []contract.Model{{ID: "s1", Provider: "slow", Free: true}}},
		entered:    enteredSlow,
		release:    releaseSlow,
	})
	a.Register(&enteringVendor{
		fakeVendor: fakeVendor{id: "fast", name: "Fast", mods: []contract.Model{{ID: "f1", Provider: "fast", Free: true}}},
		entered:    enteredFast,
	})

	done := make(chan struct{})
	var refreshErr error
	go func() {
		refreshErr = a.Refresh(context.Background())
		close(done)
	}()

	<-enteredSlow // 慢厂商已进入且阻塞中
	select {
	case <-enteredFast:
		// 快厂商已并行开始拉取
	case <-time.After(2 * time.Second):
		t.Fatal("快厂商在慢厂商阻塞期间未开始拉取（串行等待）")
	}
	close(releaseSlow)
	<-done
	if refreshErr != nil {
		t.Fatalf("Refresh: %v", refreshErr)
	}
	if !a.HasModel("slow", "s1") || !a.HasModel("fast", "f1") {
		t.Fatalf("两家目录都应合入，got slow=%v fast=%v", a.HasModel("slow", "s1"), a.HasModel("fast", "f1"))
	}
}

// TestRefreshHungVendorDoesNotBlockOthers 挂起厂商不拖死整体：
// 挂到 ctx 取消的厂商在预算内退出，快厂商目录已在首轮合入。
func TestRefreshHungVendorDoesNotBlockOthers(t *testing.T) {
	a := New()
	a.Register(&enteringVendor{
		fakeVendor: fakeVendor{id: "hang", name: "Hang", mods: []contract.Model{{ID: "h1", Provider: "hang", Free: true}}},
		release:    make(chan struct{}), // 永不释放 → 只能被 ctx 取消唤醒
	})
	a.Register(&fakeVendor{
		id:   "fast",
		name: "Fast",
		mods: []contract.Model{{ID: "f1", Provider: "fast", Free: true}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("Refresh must return even with a hung vendor: %v", err)
	}
	// 总时长由预算（而非 ∑各厂商预算）决定：挂起厂商被取消后就该返回。
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Refresh 被挂起厂商拖了 %v", elapsed)
	}
	// 轮到挂起厂商前快厂商目录已合入，不被饿死。
	if !a.HasModel("fast", "f1") {
		t.Fatal("快厂商目录未合入（被挂起厂商饿死）")
	}
	if a.HasModel("hang", "h1") {
		t.Fatal("挂起厂商目录不应合入（未完成拉取）")
	}
}
