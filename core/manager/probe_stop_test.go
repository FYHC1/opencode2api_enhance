package manager

// V1 停止扫描并发接逻辑单测：RequestStop 并发中断活跃探针（stop_scan_concurrency 上限）、
// 停止统计、停止后 probeNode/worker 正常退出（不 panic、无残留）。全部使用 fake Runner
// + 临时目录 + httptest 端口，不触网、不 spawn 真实进程。

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// stopRunner 线程安全 fake Runner：记录 spawn/kill 并统计同时进行中的 kill 并发窗口
// （Kill 停留 20ms 拉宽窗口，供 stop_scan_concurrency 上限断言）。
type stopRunner struct {
	mu          sync.Mutex
	starts      int
	killed      []int
	inflight    int
	maxInflight int
}

func (f *stopRunner) Start(spec ExecSpec) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return 100 + f.starts, nil
}

func (f *stopRunner) Kill(pid int) error {
	f.mu.Lock()
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	f.inflight--
	f.killed = append(f.killed, pid)
	f.mu.Unlock()
	return nil
}

func (f *stopRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// spawnedSet spawn 出的全部 pid（fake 规则：100+序号）。
func (f *stopRunner) spawnedSet() map[int]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]bool{}
	for i := 1; i <= f.starts; i++ {
		out[100+i] = true
	}
	return out
}

func (f *stopRunner) killedSet() map[int]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]bool{}
	for _, p := range f.killed {
		out[p] = true
	}
	return out
}

func (f *stopRunner) maxConcurrentKills() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInflight
}

// waitCond 轮询条件直至超时。
func waitCond(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// V1: RequestStop 按 stop_scan_concurrency 并发上限 kill 活跃探针
// （登记 5 对、上限 2 → 3 批 2+2+1；全部被杀、统计正确、登记清空）。
func TestRequestStopInterruptsProbesBounded(t *testing.T) {
	m := newTestManager(t)
	if err := m.ConfigSet("stop_scan_concurrency", "2"); err != nil {
		t.Fatalf("set config: %v", err)
	}
	run := &stopRunner{}
	ctrl := NewScanController(m, run)
	ctrl.mu.Lock()
	ctrl.progress.Status = ScanRunning
	ctrl.mu.Unlock()
	pairs := [][2]int{{101, 201}, {102, 202}, {103, 203}, {104, 204}, {105, 205}}
	for w, p := range pairs {
		ctrl.registerProbe(w, p[0], p[1])
	}

	snap := ctrl.RequestStop()
	if snap.Status != ScanStopping {
		t.Fatalf("status = %s, want stopping", snap.Status)
	}
	if snap.StoppingCount != len(pairs) || snap.StoppedCount != len(pairs) {
		t.Fatalf("counts = stopping %d / stopped %d, want %d", snap.StoppingCount, snap.StoppedCount, len(pairs))
	}
	got := run.killedSet()
	for _, p := range pairs {
		for _, pid := range p {
			if !got[pid] {
				t.Fatalf("pid %d not killed: %v", pid, got)
			}
		}
	}
	// 并发窗口 ≤ 上限 2（同时最多 kill 2 对探针进程）。
	if got := run.maxConcurrentKills(); got > 2 {
		t.Fatalf("max concurrent kills = %d, want ≤ 2", got)
	}
	ctrl.activeMu.Lock()
	left := len(ctrl.activeProbes)
	ctrl.activeMu.Unlock()
	if left != 0 {
		t.Fatalf("activeProbes not cleared: %d entries remain", left)
	}
}

// V1: stop_scan_concurrency 未设置/0 → 默认 4；显式 1 → 串行。
func TestRequestStopConcurrencyCapFallback(t *testing.T) {
	cases := []struct {
		name  string
		set   string // 空 = 不设置
		limit int
	}{
		{"unset_default4", "", 4},
		{"zero_fallback4", "0", 4},
		{"explicit1", "1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			if tc.set != "" {
				if err := m.ConfigSet("stop_scan_concurrency", tc.set); err != nil {
					t.Fatalf("set config: %v", err)
				}
			}
			run := &stopRunner{}
			ctrl := NewScanController(m, run)
			ctrl.mu.Lock()
			ctrl.progress.Status = ScanRunning
			ctrl.mu.Unlock()
			for w := 0; w < 5; w++ {
				ctrl.registerProbe(w, 1000+w*10, 1000+w*10+1)
			}
			snap := ctrl.RequestStop()
			if snap.StoppingCount != 5 || snap.StoppedCount != 5 {
				t.Fatalf("counts = %d/%d, want 5/5", snap.StoppingCount, snap.StoppedCount)
			}
			if got := run.maxConcurrentKills(); got > tc.limit {
				t.Fatalf("max concurrent kills = %d, want ≤ %d", got, tc.limit)
			}
			// 上限真正生效而非退化成串行：默认 4 时观察到的并发窗口应 >1。
			if tc.limit > 1 && run.maxConcurrentKills() < 2 {
				t.Fatalf("concurrency cap not exercised: max = %d", run.maxConcurrentKills())
			}
		})
	}
}

// V1: 扫描运行中停止 → 活跃探针被中断、worker 循环退出、扫描正常收尾（-race 下跑无泄漏）。
// 探针走到 api 等待（已登记）后 RequestStop：kill 后 waitForPort 中止检查让阻塞轮询快速退出。
func TestScanStopInterruptsRunningProbes(t *testing.T) {
	m := newTestManager(t)
	run := &stopRunner{}
	ctrl := NewScanController(m, run)
	const n = 12
	nodes := make([]ClashNode, n)
	for i := range nodes {
		nodes[i] = ClashNode{Name: fmt.Sprintf("n%02d", i), NodeType: "trojan", Server: "1.2.3.4", Port: 443, Password: "p"}
	}
	opts := ScanOptions{APIPort: 25100, SocksPort: 26100, TimeoutSec: 3, Concurrency: 5}
	ctrl.mu.Lock()
	ctrl.progress.Status = ScanRunning
	ctrl.mu.Unlock()
	go ctrl.run(opts, nodes)

	// 等端口分配完成（progress.Total 已置）后再占用 socks 端口：让探针 socks 等待立即就绪、
	// 走到 api 等待（api 端口无人监听 → 阻塞在已登记状态），便于停止时并发中断。
	if !waitCond(t, 3*time.Second, func() bool { return ctrl.Snapshot().Total == n }) {
		t.Fatalf("ports not allocated: %+v", ctrl.Snapshot())
	}
	var listeners []net.Listener
	for w := 0; w < opts.Concurrency; w++ {
		listeners = append(listeners, occupyPort(t, uint16(26100+w)))
	}
	defer func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()

	if !waitCond(t, 5*time.Second, func() bool {
		ctrl.activeMu.Lock()
		defer ctrl.activeMu.Unlock()
		return len(ctrl.activeProbes) > 0
	}) {
		t.Fatalf("no probe registered before stop")
	}
	snap := ctrl.RequestStop()
	if snap.StoppingCount < 1 || snap.StoppedCount != snap.StoppingCount {
		t.Fatalf("stop counts = stopping %d / stopped %d", snap.StoppingCount, snap.StoppedCount)
	}

	// 扫描收尾：worker 全部退出 → run() defer 置 Done（停止后不再干等到单节点超时）。
	if !waitCond(t, 8*time.Second, func() bool { return ctrl.Snapshot().Status == ScanDone }) {
		t.Fatalf("scan not finished after stop: %+v", ctrl.Snapshot())
	}
	// 所有 spawn 的探针进程都被 kill（sweep 与 probeNode defer 幂等，至少一次）。
	got := run.killedSet()
	for pid := range run.spawnedSet() {
		if !got[pid] {
			t.Fatalf("pid %d spawned but never killed", pid)
		}
	}
	// 停止阻止了后续 spawn（5 个 worker × 2 探针进程封顶）。
	if got := run.startCount(); got > 10 {
		t.Fatalf("spawned %d probe processes, want ≤ 10 (5 workers × 2)", got)
	}
	// 停止后登记清空。
	ctrl.activeMu.Lock()
	left := len(ctrl.activeProbes)
	ctrl.activeMu.Unlock()
	if left != 0 {
		t.Fatalf("activeProbes not empty after scan: %d", left)
	}
}

// V1: 已停止状态下 probeNode 不 spawn、直接返回 stopped，登记保持为空。
func TestProbeNodeSkipsAfterStop(t *testing.T) {
	m := newTestManager(t)
	run := &stopRunner{}
	ctrl := NewScanController(m, run)
	ctrl.mu.Lock()
	ctrl.progress.Status = ScanStopping
	ctrl.mu.Unlock()
	node := ClashNode{Name: "x", NodeType: "trojan", Server: "1.2.3.4", Port: 443, Password: "p"}
	res := ctrl.probeNode(0, ScanOptions{TimeoutSec: 3}, node, portPair{api: 25100, socks: 26100}, t.TempDir())
	if res.Category != "stopped" {
		t.Fatalf("category = %q, want stopped", res.Category)
	}
	if got := run.startCount(); got != 0 {
		t.Fatalf("spawned %d probe processes before stop check, want 0", got)
	}
	ctrl.activeMu.Lock()
	left := len(ctrl.activeProbes)
	ctrl.activeMu.Unlock()
	if left != 0 {
		t.Fatalf("activeProbes not empty: %d", left)
	}
}
