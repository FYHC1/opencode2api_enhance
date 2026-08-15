package manager

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// recorderRunner 并发安全记录网关 Start/Kill；
// Start 返回测试进程自身 pid（pidAlive 恒真），使并发 Status 的锁内「重查 isRunning」可命中已启动状态。
type recorderRunner struct {
	mu      sync.Mutex
	starts  int
	gateway int
	kills   []int
}

func (r *recorderRunner) Start(spec ExecSpec) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	if hasGatewayStart([]ExecSpec{spec}) {
		r.gateway++
	}
	return os.Getpid(), nil
}

func (r *recorderRunner) Kill(pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kills = append(r.kills, pid)
	return nil
}

// M1: 并发 Status 自动拉起只 spawn 一个网关子进程（check-then-act 竞态回归）。
func TestGatewayConcurrentStatusSingleSpawn(t *testing.T) {
	m := newTestManager(t)
	run := &fakeRunner{}
	ln1, ln2 := occupyPort(t, 28901), occupyPort(t, 28901+singboxPortOffset)
	defer ln1.Close()
	defer ln2.Close()
	runningInstanceHeld(t, m, run, "c1", 28901, true, ln1, ln2)

	gw := NewGateway(m, 20085)
	rec := &recorderRunner{}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gw.Status(rec)
		}()
	}
	wg.Wait()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.gateway != 1 {
		t.Fatalf("gateway spawns = %d, want 1", rec.gateway)
	}
	if len(rec.kills) != 0 {
		t.Fatalf("kills = %v, want none", rec.kills)
	}
	if !gw.isRunning(rec) {
		t.Fatal("gateway should be running after concurrent Status")
	}
}

// M1: 网关运行中时 ApplyKey 热重启与并发 Status 互斥——不双启、替换的旧子进程必被停（无孤儿）。
func TestGatewayApplyKeyConcurrentStatusNoOrphan(t *testing.T) {
	m := newTestManager(t)
	run := &fakeRunner{}
	ln1, ln2 := occupyPort(t, 28911), occupyPort(t, 28911+singboxPortOffset)
	defer ln1.Close()
	defer ln2.Close()
	runningInstanceHeld(t, m, run, "c2", 28911, true, ln1, ln2)

	gw := NewGateway(m, 20086)
	rec := &recorderRunner{}

	// 网关先处于运行中（Status 自动拉起 = 第 1 次 spawn）
	gw.Status(rec)
	rec.mu.Lock()
	pre := rec.gateway
	rec.mu.Unlock()
	if pre != 1 {
		t.Fatalf("initial gateway spawns = %d, want 1", pre)
	}

	applyErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		applyErr <- gw.ApplyKey("conc-key-123", rec)
	}()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gw.Status(rec)
		}()
	}
	wg.Wait()
	if err := <-applyErr; err != nil {
		t.Fatalf("ApplyKey: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	// 预期：初启 1 + ApplyKey 重启 1；并发 Status 不得额外 spawn。
	if rec.gateway != pre+1 {
		t.Fatalf("gateway spawns = %d, want %d", rec.gateway, pre+1)
	}
	if len(rec.kills) != 1 {
		t.Fatalf("kills = %v, want exactly 1 (替换的旧子进程)", rec.kills)
	}
	for _, k := range rec.kills {
		if k != os.Getpid() {
			t.Fatalf("killed unexpected pid %d", k)
		}
	}
	if gw.password != "conc-key-123" {
		t.Fatalf("password = %q, want conc-key-123", gw.password)
	}
}

// M2: ApplyKey 并发 refreshModels 无数据竞态；抓模型请求使用锁内快照密钥（必为已应用过的 key）。
func TestGatewayApplyKeyConcurrentRefreshModels(t *testing.T) {
	var authMu sync.Mutex
	var seenAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		authMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"grok-3"}]}`))
	}))
	defer srv.Close()

	m := newTestManager(t)
	gw := NewGateway(m, 20087)
	gw.port = uint16(srv.Listener.Addr().(*net.TCPAddr).Port)

	const rounds = 20
	valid := make(map[string]bool, rounds+1)
	// 预置初始密钥（可能被首轮抓取快照），避免默认密钥不在已应用集合内。
	gw.mu.Lock()
	gw.password = "conc-key-init"
	gw.mu.Unlock()
	valid["conc-key-init"] = true
	for i := 0; i < rounds; i++ {
		valid[fmt.Sprintf("conc-key-%d", i)] = true
	}

	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = gw.ApplyKey(fmt.Sprintf("conc-key-%d", i), &fakeRunner{})
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				// 节流重置（lastFetch/updatedAt 归零）后每次 refreshModels 都真实抓取，
				// 扩大与 ApplyKey 写 g.password 的并发窗口。
				gw.mu.Lock()
				gw.lastFetch = 0
				gw.updatedAt = 0
				gw.mu.Unlock()
				gw.refreshModels()
				// 等待本次抓取 goroutine 完成（loading 复位）再触发下一轮
				for {
					gw.loadingMu.Lock()
					busy := gw.loading
					gw.loadingMu.Unlock()
					if !busy {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	wg.Wait()

	authMu.Lock()
	defer authMu.Unlock()
	if len(seenAuth) == 0 {
		t.Fatal("no fetch reached the models server")
	}
	for _, a := range seenAuth {
		if len(a) < 7 || a[:7] != "Bearer " || !valid[a[7:]] {
			t.Fatalf("fetch used unexpected key %q", a)
		}
	}
}