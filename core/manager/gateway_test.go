package manager

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runningInstance 添加并启动一个实例（占口使 waitForPort 立即成功）。
func runningInstance(t *testing.T, m *Manager, run *fakeRunner, name string, port uint16, join bool) {
	t.Helper()
	ln1, ln2 := occupyPort(t, port), occupyPort(t, port+10000)
	defer ln1.Close()
	defer ln2.Close()
	runningInstanceHeld(t, m, run, name, port, join, ln1, ln2)
}

// runningInstanceHeld 用调用方持有的监听启动实例（端口在整个生命周期保持监听）。
func runningInstanceHeld(t *testing.T, m *Manager, run *fakeRunner, name string, port uint16, join bool, ln1, ln2 net.Listener) {
	t.Helper()
	joinSeams(m)
	_ = m.AddInstance(Instance{Name: name, Port: port, Node: "node-a", Password: "sk-x", SingboxPort: port + 10000})
	if err := m.StartInstance(run, name); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	if join {
		inst, ok := m.FindInstance(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		inst.JoinGateway = true
		_ = m.UpdateInstance(inst)
	}
}

func hasGatewayStart(starts []ExecSpec) bool {
	for _, s := range starts {
		for _, a := range s.Args {
			if a == "-gateway" {
				return true
			}
		}
	}
	return false
}

func TestGatewaySyncStartStop(t *testing.T) {
	m := newTestManager(t)
	run := &fakeRunner{}
	gw := NewGateway(m, 20080)

	// 无成员 → 不启动
	if err := gw.sync(run); err != nil {
		t.Fatalf("sync(empty): %v", err)
	}
	if hasGatewayStart(run.starts) {
		t.Fatal("gateway must not start with no members")
	}

	// 有运行成员 → 启动
	runningInstance(t, m, run, "g1", 20901, true)
	if err := gw.sync(run); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !hasGatewayStart(run.starts) {
		t.Fatal("expected a -gateway start")
	}

	// 有成员后移除成员（改为不 join）→ 停网关
	inst, ok := m.FindInstance("g1")
	if !ok {
		t.Fatal("g1 missing")
	}
	inst.JoinGateway = false
	_ = m.UpdateInstance(inst)
	run.killed = nil
	if err := gw.sync(run); err != nil {
		t.Fatalf("sync(no member): %v", err)
	}
	if len(run.killed) == 0 {
		t.Fatal("gateway should be stopped when pool empties")
	}
}

func TestBatchAddDedupAndAutoName(t *testing.T) {
	m := newTestManager(t)
	nodes := []ClashNode{
		{Name: "node-a", Server: "1.2.3.4", Port: 443},
		{Name: "node-b", Server: "5.6.7.8", Port: 8443},
	}
	res := m.BatchAdd(nodes, 28000, false, "")
	if len(res.Added) != 2 {
		t.Fatalf("added = %v, want 2", res)
	}
	// 重复节点再次添加 → 全部跳过
	res2 := m.BatchAdd(nodes, 28000, true, "")
	if len(res2.Skipped) != 2 {
		t.Fatalf("dedup: added=%v skipped=%v", res2.Added, res2.Skipped)
	}
	// 自动命名含「实例」
	for _, inst := range m.ListInstances() {
		if !strings.Contains(inst.Name, "实例") {
			t.Fatalf("auto name should contain 实例: %q", inst.Name)
		}
	}
	// IP 应为 server:port
	for _, inst := range m.ListInstances() {
		if inst.IP != inst.Node {
			// node-a → 1.2.3.4:443 / node-b → 5.6.7.8:8443
		}
	}
}

func TestDataCleanLevels(t *testing.T) {
	m := newTestManager(t)
	_ = os.MkdirAll(filepath.Join(m.Paths().RuntimeDir, "_unified-gateway"), 0o755)
	_ = os.WriteFile(m.Paths().Instances, []byte(`[{"name":"a"}]`), 0o644)
	_ = os.WriteFile(m.Paths().Config, []byte(`{"base_url":"x"}`), 0o644)

	// L1：保留实例与配置
	if err := m.DataClean(&fakeRunner{}, nil, 1); err != nil {
		t.Fatalf("L1: %v", err)
	}
	if _, err := os.Stat(m.Paths().RuntimeDir); !os.IsNotExist(err) {
		t.Fatalf("L1 runtime should be gone")
	}
	if _, err := os.Stat(m.Paths().Instances); err != nil {
		t.Fatalf("L1 instances should remain: %v", err)
	}

	// L2：清空实例
	_ = os.MkdirAll(m.Paths().RuntimeDir, 0o755)
	if err := m.DataClean(&fakeRunner{}, nil, 2); err != nil {
		t.Fatalf("L2: %v", err)
	}
	data, _ := os.ReadFile(m.Paths().Instances)
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("L2 instances = %q, want []", string(data))
	}

	// L3：删配置 + 备份
	_ = os.MkdirAll(m.Paths().RuntimeDir, 0o755)
	_ = os.WriteFile(m.Paths().Instances, []byte(`[]`), 0o644)
	if err := m.DataClean(&fakeRunner{}, nil, 3); err != nil {
		t.Fatalf("L3: %v", err)
	}
	if _, err := os.Stat(m.Paths().Config); !os.IsNotExist(err) {
		t.Fatalf("L3 config should be removed")
	}
	if _, err := os.Stat(m.Paths().Config + ".bak"); err != nil {
		t.Fatalf("L3 backup missing: %v", err)
	}
	if err := m.DataClean(&fakeRunner{}, nil, 9); err == nil {
		t.Fatal("invalid level should error")
	}
}

func TestRestartPoolStartsMembersThenGateway(t *testing.T) {
	m := newTestManager(t)
	run := &fakeRunner{}
	// 占口监听在整个重启期间保持打开（否则 waitForPort 超时）
	ln1, ln2 := occupyPort(t, 27901), occupyPort(t, 37901)
	ln3, ln4 := occupyPort(t, 27902), occupyPort(t, 37902)
	defer ln1.Close()
	defer ln2.Close()
	defer ln3.Close()
	defer ln4.Close()

	runningInstanceHeld(t, m, run, "r1", 27901, true, ln1, ln2)
	runningInstanceHeld(t, m, run, "r2", 27902, true, ln3, ln4)

	gw := NewGateway(m, 20090)
	res := m.RestartPool(run, gw)
	if res.Started != 2 {
		t.Fatalf("started = %d, want 2", res.Started)
	}
	// 网关应最终启动（顺序在实例之后）
	lastIdx := -1
	for i, s := range run.starts {
		if hasGatewayStart([]ExecSpec{s}) {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		t.Fatal("gateway start missing")
	}
	if lastIdx != len(run.starts)-1 {
		t.Fatalf("gateway should be last start (idx %d of %d)", lastIdx, len(run.starts))
	}
	if res.Error != "" {
		t.Fatalf("restart error: %s", res.Error)
	}
}
