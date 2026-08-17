// 统计重置（ResetStats）修复回归测试：
// 1) 网关端口取 managerGatewayPort（env 生效）而非硬编码 40080；
// 2) 状态非 Running 但端口存活的实例走 HTTP 复位（探测兜底），不再磁盘直写；
// 3) 网关 HTTP 复位失败如实上报（不再静默吞错）。
package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResetStatsGatewayUsesEffectivePort：网关端口按 env（OPCODE2API_GATEWAY_PORT）
// 生效——旧实现硬编码 40080，dev/便携/web-dev 槽位下 DELETE 落空并回退磁盘直写。
func TestResetStatsGatewayUsesEffectivePort(t *testing.T) {
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 200, tr)
	defer stop()
	m := newTestManager(t)
	// 覆盖 newTestManager 注入的随机端口：ResetStats 在调用时读取 env。
	t.Setenv("OPCODE2API_GATEWAY_PORT", itoa(port))

	res := m.ResetStats(false)
	if res.ResetCount != 1 {
		t.Fatalf("reset_count = %d, want 1（网关走生效端口 HTTP 复位）; failed=%v", res.ResetCount, res.Failed)
	}
	if tr.total() != 1 {
		t.Fatalf("gateway deletes = %d, want 1", tr.total())
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed = %v", res.Failed)
	}
}

// TestResetStatsProbeLiveStoppedInstance：实例状态陈旧（非 Running）但端口存活 →
// 探测兜底判定运行中 → HTTP 复位，不磁盘直写（磁盘直写会与子进程写盘器撞「占用」）。
func TestResetStatsProbeLiveStoppedInstance(t *testing.T) {
	m := newTestManager(t)
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 200, tr)
	defer stop()
	_ = m.AddInstance(Instance{Name: "live-but-stopped", Port: port, Node: "n", Password: "sk"})
	statsPath := filepath.Join(m.paths.RuntimeDir, "live-but-stopped", "stats.json")
	writeStatsFile(t, m, "live-but-stopped", `{"total_requests":9,"models":{"m":{"request_count":9}}}`)

	res := m.ResetStats(false)
	if res.ResetCount != 1 {
		t.Fatalf("reset_count = %d, want 1; failed=%v", res.ResetCount, res.Failed)
	}
	if tr.total() != 1 {
		t.Fatalf("deletes = %d, want 1（探测存活 → HTTP 复位）", tr.total())
	}
	// HTTP 路径不触碰磁盘：文件保持原样（旧实现按状态直写 → 占用风险 + 复位被吞）。
	data, err := os.ReadFile(statsPath)
	if err != nil || !strings.Contains(string(data), `"total_requests":9`) {
		t.Fatalf("stats.json 被磁盘覆写（应走 HTTP 不动盘）: err=%v content=%s", err, string(data))
	}
}

// TestResetStatsGatewayFailureSurfaced：网关 HTTP 复位失败（500）如实进入 Failed——
// 旧实现回退磁盘直写且静默吞错，用户无法得知网关统计未复位。
func TestResetStatsGatewayFailureSurfaced(t *testing.T) {
	tr := &deleteTracker{}
	port, stop := concurrentDeleteServer(t, 0, 500, tr)
	defer stop()
	m := newTestManager(t)
	t.Setenv("OPCODE2API_GATEWAY_PORT", itoa(port))

	res := m.ResetStats(false)
	if res.ResetCount != 0 {
		t.Fatalf("reset_count = %d, want 0", res.ResetCount)
	}
	found := false
	for _, f := range res.Failed {
		if strings.Contains(f, "统一网关") && strings.Contains(f, "HTTP 500") {
			found = true
		}
	}
	if !found {
		t.Fatalf("网关 500 未上报: %v", res.Failed)
	}
}
