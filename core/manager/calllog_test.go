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
