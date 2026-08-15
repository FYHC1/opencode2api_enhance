package manager

// H4 StopInstance/RemoveInstanceAlive 短锁三段式的时序单测：
//   - Kill 必须发生在未持 m.mu 的窗口（Kill 内短超时尝试取 m.mu，取不到即证明锁被持有）；
//   - 阶段1→阶段3 之间可见 Stopping 中间态，且 Stopping 状态下 Start 依旧被拒；
//   - 幂等：Stopped 再 Stop 静默成功，不存在实例报错。
// 全部使用 fake Runner + 临时目录 + 占口监听，不触网、不 spawn 真实进程。

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// lockProbeRunner 记录 Kill 并发起锁可用性探测：Kill 内用短超时尝试获取 m.mu，
// 能拿到说明调用方未持有 m.mu（持有则会一直阻塞到超时）。
type lockProbeRunner struct {
	mu     sync.Mutex
	target *Manager
	starts int
	killed []int
	lockOK []bool // 每个 Kill 对应的锁探测结果
}

func (f *lockProbeRunner) Start(_ ExecSpec) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return 100 + f.starts, nil
}

func (f *lockProbeRunner) Kill(pid int) error {
	got := make(chan struct{})
	go func() {
		f.target.mu.Lock()
		close(got)
		f.target.mu.Unlock()
	}()
	ok := true
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		ok = false // 探测期间仍拿不到锁：Kill 在 m.mu 持锁的窗口内执行
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, pid)
	f.lockOK = append(f.lockOK, ok)
	return nil
}

func TestStopInstanceKillsOutsideLock(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &lockProbeRunner{target: m}
	const port = 28501
	lnAPI := occupyPort(t, port)
	defer lnAPI.Close()
	lnSB := occupyPort(t, port+singboxPortOffset)
	defer lnSB.Close()

	if err := m.AddInstance(Instance{Name: "i1", Port: port, Node: "node-a", SingboxPort: port + singboxPortOffset}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := m.StartInstance(run, "i1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.StopInstance(run, "i1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.killed) != 2 {
		t.Fatalf("killed = %d, want 2", len(run.killed))
	}
	for i, ok := range run.lockOK {
		if !ok {
			t.Fatalf("kill #%d (pid %d) ran while m.mu held", i, run.killed[i])
		}
	}
	got, _ := m.FindInstance("i1")
	if got.Status.State != "Stopped" || got.PID != nil || got.SingboxPID != nil {
		t.Fatalf("after stop = %+v", got)
	}
}

func TestRemoveInstanceAliveKillsOutsideLock(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &lockProbeRunner{target: m}
	const port = 28502
	lnAPI := occupyPort(t, port)
	defer lnAPI.Close()
	lnSB := occupyPort(t, port+singboxPortOffset)
	defer lnSB.Close()

	_ = m.AddInstance(Instance{Name: "i2", Port: port, Node: "node-a", SingboxPort: port + singboxPortOffset})
	if err := m.StartInstance(run, "i2"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.RemoveInstanceAlive(run, "i2"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.killed) != 2 {
		t.Fatalf("killed = %d, want 2", len(run.killed))
	}
	for i, ok := range run.lockOK {
		if !ok {
			t.Fatalf("kill #%d (pid %d) ran while m.mu held", i, run.killed[i])
		}
	}
	if _, ok := m.FindInstance("i2"); ok {
		t.Fatal("instance should be gone")
	}
}

// blockingKillRunner Kill 阻塞到 release 关闭（拉宽阶段2 窗口，供观察 Stopping 中间态）。
type blockingKillRunner struct {
	mu      sync.Mutex
	starts  int
	killed  []int
	release chan struct{}
}

func (f *blockingKillRunner) Start(_ ExecSpec) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return 100 + f.starts, nil
}

func (f *blockingKillRunner) Kill(pid int) error {
	<-f.release
	f.mu.Lock()
	f.killed = append(f.killed, pid)
	f.mu.Unlock()
	return nil
}

func TestStopInstanceExposesStoppingState(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &blockingKillRunner{release: make(chan struct{})}
	const port = 28503
	lnAPI := occupyPort(t, port)
	defer lnAPI.Close()
	lnSB := occupyPort(t, port+singboxPortOffset)
	defer lnSB.Close()

	_ = m.AddInstance(Instance{Name: "i3", Port: port, Node: "node-a", SingboxPort: port + singboxPortOffset})
	if err := m.StartInstance(run, "i3"); err != nil {
		t.Fatalf("start: %v", err)
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- m.StopInstance(run, "i3") }()
	// 阶段2 窗口（Kill 阻塞中）：锁外，前端应可见 Stopping 中间态
	if !waitCond(t, 3*time.Second, func() bool {
		got, _ := m.FindInstance("i3")
		return got.Status.State == "Stopping"
	}) {
		t.Fatalf("Stopping intermediate state not visible during kill")
	}
	// Stopping 状态下 Start 依旧被拒（markStartingLocked 的「正在忙」）
	if err := m.StartInstance(&fakeRunner{}, "i3"); err == nil || !strings.Contains(err.Error(), "正在忙") {
		t.Fatalf("start during stopping = %v, want 正在忙", err)
	}
	// 放行 Kill → 收尾为 Stopped
	close(run.release)
	if err := <-stopDone; err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, _ := m.FindInstance("i3")
	if got.Status.State != "Stopped" || got.PID != nil {
		t.Fatalf("after stop = %+v", got)
	}
	if len(run.killed) != 2 {
		t.Fatalf("killed = %d, want 2", len(run.killed))
	}
}

func TestStopInstanceIdempotentAndMissing(t *testing.T) {
	m := newTestManager(t)
	run := &fakeRunner{}
	_ = m.AddInstance(Instance{Name: "i9", Port: 28509, Node: "node-a", SingboxPort: 28509 + singboxPortOffset})
	// Stopped 状态下 Stop 幂等成功（不 kill）
	if err := m.StopInstance(run, "i9"); err != nil {
		t.Fatalf("stop stopped instance: %v", err)
	}
	got, _ := m.FindInstance("i9")
	if got.Status.State != "Stopped" {
		t.Fatalf("after idempotent stop = %+v", got)
	}
	if len(run.killed) != 0 {
		t.Fatalf("killed = %d, want 0", len(run.killed))
	}
	// 不存在实例 → 报错
	if err := m.StopInstance(run, "不存在"); err == nil {
		t.Fatal("missing instance should error")
	}
	if err := m.RemoveInstanceAlive(run, "不存在"); err == nil {
		t.Fatal("missing instance should error on remove")
	}
}