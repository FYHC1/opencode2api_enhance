package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGateWayLog 写统一网关 call_log.jsonl。
func writeGateWayLog(t *testing.T, m *Manager, lines []string) {
	t.Helper()
	dir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "call_log.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadCallLogParsesAndCaps(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{
		`{"req_id":"r1","ts":"2026-08-05T10:00:00+08:00","status":"ok","events":[{"type":"connect_ok","node":"127.0.0.1:28100"}]}`,
		`{"req_id":"r2","ts":"2026-08-05T10:01:00+08:00","status":"fail","err_msg":"boom","events":[{"type":"ttft_timeout","detail":"no first token"}]}`,
	})
	recs := m.ReadCallLog(10)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if recs[0].ReqID != "r1" || recs[0].StatusText() != "【成功】" || recs[0].HasIssue() {
		t.Fatalf("rec0 = %+v", recs[0])
	}
	if recs[1].ReqID != "r2" || recs[1].StatusText() != "【失败】" || !recs[1].HasIssue() {
		t.Fatalf("rec1 = %+v", recs[1])
	}
	if recs[1].Events[0].Type != "ttft_timeout" {
		t.Fatalf("event = %+v", recs[1].Events[0])
	}
	// 环形截断：最新 max 条
	capped := m.ReadCallLog(1)
	if len(capped) != 1 || capped[0].ReqID != "r2" {
		t.Fatalf("capped = %+v", capped)
	}
}

func TestReadCallLogMissingFile(t *testing.T) {
	m := newTestManager(t)
	if recs := m.ReadCallLog(10); len(recs) != 0 {
		t.Fatalf("missing file should give empty, got %d", len(recs))
	}
}

func TestClearCallLog(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{`{"req_id":"r1","ts":"t","status":"ok"}`})
	if err := m.ClearCallLog(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(m.CallLogPath()); err == nil {
		t.Fatal("log file should be gone")
	}
}

// markRunning 把实例置为 Running（AddInstance 默认 Stopped）。
func markRunning(t *testing.T, m *Manager, name string) {
	t.Helper()
	for _, inst := range m.ListInstances() {
		if inst.Name == name {
			inst.Status = StatusRunning()
			if err := m.UpdateInstance(inst); err != nil {
				t.Fatalf("mark running %s: %v", name, err)
			}
			return
		}
	}
	t.Fatalf("instance %s not found", name)
}

// writeInstanceLog 写某实例运行目录下的 call_log.jsonl。
func writeInstanceLog(t *testing.T, m *Manager, name string, lines []string) {
	t.Helper()
	dir := filepath.Join(m.paths.RuntimeDir, sanitizeInstanceName(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "call_log.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// S4：多目录聚合读取 + 按时间合并排序 + 实例名标注。
func TestReadCallLogAggregatesInstances(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{
		`{"req_id":"g1","ts":"2026-08-05T10:00:00+08:00","status":"ok","model":"gw"}`,
		`{"req_id":"g2","ts":"2026-08-05T10:02:00+08:00","status":"ok","model":"gw"}`,
	})
	// Running 独享实例：日志时间穿插在网关记录之间
	_ = m.AddInstance(Instance{Name: "solo-a", Port: 14400, Node: "n1", Password: "sk"})
	markRunning(t, m, "solo-a")
	writeInstanceLog(t, m, "solo-a", []string{
		`{"req_id":"a1","ts":"2026-08-05T10:01:00+08:00","status":"fail","err_msg":"boom"}`,
	})

	recs := m.ReadCallLog(10)
	if len(recs) != 3 {
		t.Fatalf("len=%d, want 3 (gw 2 + instance 1)", len(recs))
	}
	// 按时间升序合并：g1(10:00) → a1(10:01) → g2(10:02)
	for i, id := range []string{"g1", "a1", "g2"} {
		if recs[i].ReqID != id {
			t.Fatalf("order[%d]=%s, want %s", i, recs[i].ReqID, id)
		}
	}
	if recs[0].Source != "" {
		t.Fatalf("gateway source=%q, want empty", recs[0].Source)
	}
	if recs[1].Source != "solo-a" || recs[1].StatusText() != "【失败】" || !recs[1].HasIssue() {
		t.Fatalf("instance record = %+v, want source=solo-a/fail", recs[1])
	}
	if recs[2].Source != "" {
		t.Fatalf("gateway source=%q, want empty", recs[2].Source)
	}
}

// S4：Stopped 实例的日志不参与聚合。
func TestReadCallLogSkipsStoppedInstanceLogs(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{`{"req_id":"g1","ts":"2026-08-05T10:00:00+08:00","status":"ok"}`})
	_ = m.AddInstance(Instance{Name: "stopped-a", Port: 14400, Node: "n1", Password: "sk"}) // 默认 Stopped
	writeInstanceLog(t, m, "stopped-a", []string{
		`{"req_id":"a1","ts":"2026-08-05T10:01:00+08:00","status":"fail"}`,
	})

	recs := m.ReadCallLog(10)
	if len(recs) != 1 || recs[0].ReqID != "g1" {
		t.Fatalf("stopped instance log must be excluded, got %+v", recs)
	}
}

// S4：聚合后总数仍按 max 截断（取最新 max 条）。
func TestReadCallLogAggregateTotalCap(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{
		`{"req_id":"g1","ts":"2026-08-05T10:00:00+08:00","status":"ok"}`,
		`{"req_id":"g4","ts":"2026-08-05T10:03:00+08:00","status":"ok"}`,
	})
	_ = m.AddInstance(Instance{Name: "solo-a", Port: 14400, Node: "n1", Password: "sk"})
	markRunning(t, m, "solo-a")
	writeInstanceLog(t, m, "solo-a", []string{
		`{"req_id":"a2","ts":"2026-08-05T10:01:00+08:00","status":"ok"}`,
		`{"req_id":"a3","ts":"2026-08-05T10:02:00+08:00","status":"ok"}`,
	})

	recs := m.ReadCallLog(3) // 共 4 条 → 只留最新 3 条
	if len(recs) != 3 {
		t.Fatalf("len=%d, want 3", len(recs))
	}
	want := []string{"a2", "a3", "g4"}
	for i, id := range want {
		if recs[i].ReqID != id {
			t.Fatalf("cap order[%d]=%s, want %s", i, recs[i].ReqID, id)
		}
	}
}

// S4：单文件 tail 截断 + 来源标注 + 缺失文件容错。
func TestReadCallLogFileTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "call_log.jsonl")
	lines := []string{
		`{"req_id":"r1","ts":"t1","status":"ok"}`,
		`{"req_id":"r2","ts":"t2","status":"ok"}`,
		`{"req_id":"r3","ts":"t3","status":"ok"}`,
		`not-json-line`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs := readCallLogFileTail(path, "inst-x", 2)
	if len(recs) != 2 || recs[0].ReqID != "r2" || recs[1].ReqID != "r3" {
		t.Fatalf("tail recs = %+v, want [r2 r3]", recs)
	}
	if recs[0].Source != "inst-x" {
		t.Fatalf("source=%q, want inst-x", recs[0].Source)
	}
	if got := readCallLogFileTail(filepath.Join(t.TempDir(), "nope.jsonl"), "", 2); got != nil {
		t.Fatalf("missing file should be nil, got %+v", got)
	}
}

// S4：非 RFC3339 时间戳解析失败归零，不影响读取。
func TestCallLogTimeParse(t *testing.T) {
	if callLogTime("2026-08-05T10:00:00+08:00").IsZero() {
		t.Fatal("RFC3339 ts should parse")
	}
	if !callLogTime("garbage").IsZero() {
		t.Fatal("garbage ts should fall back to zero")
	}
}

func TestPidsOnPortParsesNetstat(t *testing.T) {
	sample := `TCP    127.0.0.1:18100    0.0.0.0:0    LISTENING    1234
TCP    127.0.0.1:18101    0.0.0.0:0    LISTENING    5678
TCP    127.0.0.1:18100    127.0.0.1:51234    ESTABLISHED    999
  0.0.0.0:0   0.0.0.0:0    LISTENING    0`
	old := netstatCmd
	netstatCmd = func() ([]byte, error) { return []byte(sample), nil }
	defer func() { netstatCmd = old }()
	pids := pidsOnPort(18100)
	if len(pids) != 2 {
		t.Fatalf("pids = %v, want [1234 999]", pids)
	}
	if pids[0] != 1234 || pids[1] != 999 {
		t.Fatalf("pids = %v", pids)
	}
	// PID 0（系统占用）跳过
	pids = pidsOnPort(1)
	if len(pids) != 0 {
		t.Fatalf("pid 0 must be skipped: %v", pids)
	}
}
