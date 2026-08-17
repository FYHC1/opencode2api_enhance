// 清空调用日志（ClearCallLog）修复回归测试：
// 1) 运行中网关/实例走 HTTP 清空（其进程持有日志 fd，直删会撞 Windows 占用）；
// 2) 未运行进程直删文件（含 .1 旧段）；
// 3) 失败按来源如实上报。
package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClearCallLogGatewayHTTP：网关端口存活（env 生效）→ HTTP 清空，不动磁盘。
// 旧实现直接 os.Remove，运行中网关文件必被「文件被占用」拦截。
func TestClearCallLogGatewayHTTP(t *testing.T) {
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 200, tr)
	defer stop()
	m := newTestManager(t)
	t.Setenv("OPCODE2API_GATEWAY_PORT", itoa(port))
	logPath := m.CallLogPath()
	writeGateWayLog(t, m, []string{`{"req_id":"g1","ts":"t","status":"ok"}`})

	if err := m.ClearCallLog(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if tr.total() != 1 {
		t.Fatalf("gateway deletes = %d, want 1（运行中 → HTTP）", tr.total())
	}
	// HTTP 路径不触碰磁盘：文件保持原样（由网关进程自己清）。
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("网关日志文件被管理器直删（应走 HTTP 不动盘）: %v", err)
	}
}

// TestClearCallLogRunningInstanceHTTP：实例状态非 Running 但端口存活 → HTTP 清空。
func TestClearCallLogRunningInstanceHTTP(t *testing.T) {
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 200, tr)
	defer stop()
	m := newTestManager(t)
	_ = m.AddInstance(Instance{Name: "live-but-stopped", Port: port, Node: "n", Password: "sk"})
	instPath := m.InstanceCallLogPath("live-but-stopped")
	writeInstanceLog(t, m, "live-but-stopped", []string{`{"req_id":"a1","ts":"t","status":"ok"}`})

	if err := m.ClearCallLog(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if tr.total() != 1 {
		t.Fatalf("instance deletes = %d, want 1（探测存活 → HTTP）", tr.total())
	}
	if _, err := os.Stat(instPath); err != nil {
		t.Fatalf("实例日志文件被管理器直删（应走 HTTP 不动盘）: %v", err)
	}
}

// TestClearCallLogStoppedRemovesFiles：未运行实例直删日志文件（含 .1 旧段）。
func TestClearCallLogStoppedRemovesFiles(t *testing.T) {
	m := newTestManager(t)
	_ = m.AddInstance(Instance{Name: "stopped1", Port: 59991, Node: "n", Password: "sk"})
	instPath := m.InstanceCallLogPath("stopped1")
	if err := os.MkdirAll(filepath.Dir(instPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instPath, []byte(`{"req_id":"a1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instPath+".1", []byte(`{"req_id":"a0"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.ClearCallLog(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(instPath); err == nil {
		t.Fatal("实例日志主文件应被删除")
	}
	if _, err := os.Stat(instPath + ".1"); err == nil {
		t.Fatal("实例日志 .1 旧段应被删除")
	}
}

// TestClearCallLogGatewayFailureSurfaced：网关 HTTP 清空失败（500）如实上报。
func TestClearCallLogGatewayFailureSurfaced(t *testing.T) {
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 500, tr)
	defer stop()
	m := newTestManager(t)
	t.Setenv("OPCODE2API_GATEWAY_PORT", itoa(port))

	err := m.ClearCallLog()
	if err == nil {
		t.Fatal("网关 500 应上报错误")
	}
	if !strings.Contains(err.Error(), "统一网关") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("错误未含网关来源/状态码: %v", err)
	}
}
