package manager

import (
	"net"
	"strconv"
	"testing"
)

// fakeRunner 记录 ExecSpec 并按序返回 pid；Kill 计数。
type fakeRunner struct {
	starts []ExecSpec
	pids   []int
	killed []int
}

func (f *fakeRunner) Start(spec ExecSpec) (int, error) {
	f.starts = append(f.starts, spec)
	pid := 100 + len(f.starts)
	f.pids = append(f.pids, pid)
	return pid, nil
}

func (f *fakeRunner) Kill(pid int) error {
	f.killed = append(f.killed, pid)
	return nil
}

// occupyPort 在给定端口起监听（使 waitForPort 立即成功）。
func occupyPort(t *testing.T, port uint16) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(int(port)))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	return ln
}

// joinSeams 装配测试用 fake 接缝。
func joinSeams(m *Manager) {
	m.SetSeams(&SeamFuncs{
		ResolveNode: func(name string) (ClashNode, bool) {
			if name != "node-a" {
				return ClashNode{}, false
			}
			return ClashNode{Name: "node-a", Server: "1.2.3.4", Port: 443, NodeType: "trojan", Password: "p"}, true
		},
		BuildSingbox: func(_ ClashNode, _ uint16) ([]byte, error) { return []byte(`{"outbounds":[]}`), nil },
		BuildOpenCfg: func(_ uint16) ([]byte, error) { return []byte(`{"models":{}}`), nil },
	})
}

func TestStartInstanceLifecycle(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &fakeRunner{}

	inst := Instance{Name: "i1", Port: 19901, Node: "node-a", Password: "sk-1", SingboxPort: 29901}
	if err := m.AddInstance(inst); err != nil {
		t.Fatalf("add: %v", err)
	}
	// 先占口，等 waitForPort 立即成功
	lnAPI := occupyPort(t, inst.Port)
	defer lnAPI.Close()
	lnSB := occupyPort(t, inst.SingboxPort)
	defer lnSB.Close()

	if err := m.StartInstance(run, "i1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(run.starts) != 2 {
		t.Fatalf("starts = %d, want 2 (sing-box + opencode)", len(run.starts))
	}
	if run.starts[0].Bin != m.binPath("sing-box") || run.starts[1].Bin != m.binPath("opencode2api") {
		t.Fatalf("bin order: %q %q", run.starts[0].Bin, run.starts[1].Bin)
	}
	// U5-②：独享/池成员 opencode2api 进程必须带 -call-log，日志页才能聚合读到实例日志
	ocArgs := run.starts[1].Args
	hasCallLog := false
	for _, a := range ocArgs {
		if a == "-call-log" {
			hasCallLog = true
		}
	}
	if !hasCallLog {
		t.Fatalf("opencode2api args = %v, want -call-log", ocArgs)
	}
	got, ok := m.FindInstance("i1")
	if !ok || got.Status.State != "Running" || got.PID == nil || got.SingboxPID == nil {
		t.Fatalf("after start = %+v", got)
	}

	if err := m.StopInstance(run, "i1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, _ = m.FindInstance("i1")
	if got.Status.State != "Stopped" || got.PID != nil {
		t.Fatalf("after stop = %+v", got)
	}
	if len(run.killed) != 2 {
		t.Fatalf("killed = %d, want 2", len(run.killed))
	}
}

func TestStartInstanceNodeMissing(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	_ = m.AddInstance(Instance{Name: "i2", Port: 19902, Node: "nope", SingboxPort: 29902})
	if err := m.StartInstance(&fakeRunner{}, "i2"); err == nil {
		t.Fatal("missing node should error")
	}
	got, _ := m.FindInstance("i2")
	if got.Status.State != "Error" {
		t.Fatalf("status = %+v", got.Status)
	}
}

// 回归：启动失败（sing-box 等口超时，进程已 Kill）后不得把死 PID 写回注册表——
// Stop 对 Error 态实例按快照 PID 直接 Kill，PID 被系统复用后会误杀无关进程。
func TestStartInstanceFailureClearsPID(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &fakeRunner{}
	_ = m.AddInstance(Instance{Name: "i9", Port: 19906, Node: "node-a", Password: "sk-1", SingboxPort: 29906})
	// 故意不占任何端口：waitForPort 必然超时 → sing-box 被 Kill → 启动失败
	if err := m.StartInstance(run, "i9"); err == nil {
		t.Fatal("start should fail (sing-box port never listens)")
	}
	got, ok := m.FindInstance("i9")
	if !ok || got.Status.State != "Error" {
		t.Fatalf("status = %+v", got.Status)
	}
	if got.PID != nil || got.SingboxPID != nil {
		t.Fatalf("failed start must not persist dead PIDs: pid=%v singbox=%v", got.PID, got.SingboxPID)
	}
	// 后续 Stop 不得对残留 PID 发起 Kill
	before := len(run.killed)
	if err := m.StopInstance(run, "i9"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(run.killed) != before {
		t.Fatalf("stop must not kill stale pids, killed=%v", run.killed)
	}
}

func TestStartSeamUnwired(t *testing.T) {
	m := newTestManager(t)
	_ = m.AddInstance(Instance{Name: "i3", Port: 19903, Node: "node-a", SingboxPort: 29903})
	err := m.StartInstance(&fakeRunner{}, "i3")
	if err == nil {
		t.Fatal("unwired seams should error")
	}
}

func TestRemoveInstanceAliveKillsChildren(t *testing.T) {
	m := newTestManager(t)
	joinSeams(m)
	run := &fakeRunner{}
	_ = m.AddInstance(Instance{Name: "i4", Port: 19904, Node: "node-a", SingboxPort: 29904})
	ln1, ln2 := occupyPort(t, 19904), occupyPort(t, 29904)
	defer ln1.Close()
	defer ln2.Close()
	if err := m.StartInstance(run, "i4"); err != nil {
		t.Fatalf("start: %v", err)
	}
	run.killed = nil
	if err := m.RemoveInstanceAlive(run, "i4"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := m.FindInstance("i4"); ok {
		t.Fatal("instance should be gone")
	}
	if len(run.killed) != 2 {
		t.Fatalf("killed = %d, want 2", len(run.killed))
	}
}

func TestReconcileNoProcessKeepsRunning(t *testing.T) {
	// 不真正跑进程：Reconcile 依赖 pidAlive（平台真实现），此处只验证对 Stopped 无副作用。
	m := newTestManager(t)
	_ = m.AddInstance(Instance{Name: "i5", Port: 19905, Node: "node-a", SingboxPort: 29905})
	list := m.ReconcileStates(&fakeRunner{})
	if list[0].Status.State != "Stopped" {
		t.Fatalf("stopped instance must stay stopped: %+v", list[0])
	}
}

func TestPortSuggestRange(t *testing.T) {
	m := newTestManager(t)
	p, err := m.PortSuggest()
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if p < 10000 || p > 39999 {
		t.Fatalf("port out of range: %d", p)
	}
}

func TestPortCheck(t *testing.T) {
	m := newTestManager(t)
	_ = m.AddInstance(Instance{Name: "p1", Port: 18123, Node: "n", SingboxPort: 28123})
	if r := m.PortCheck(999); r.Available {
		t.Fatalf("port <1024 should be unavailable: %+v", r)
	}
	if r := m.PortCheck(18123); r.Available {
		t.Fatalf("instance port should be unavailable: %+v", r)
	}
	if r := m.PortCheck(39998); !r.Available {
		t.Fatalf("free port should be available: %+v", r)
	}
}
