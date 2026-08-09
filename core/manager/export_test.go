package manager

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSVEscape(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"has,comma":   `"has,comma"`,
		`has"quote`:   `"has""quote"`,
		"has\nnewline": "\"has\nnewline\"",
	}
	for in, want := range cases {
		if got := csvEscape(in); got != want {
			t.Errorf("csvEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeCallLogFile(t *testing.T, m *Manager, lines []string) {
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

func TestExportCallLogCSV(t *testing.T) {
	m := New(t.TempDir())
	writeCallLogFile(t, m, []string{
		`{"req_id":"r1","ts":"2026-08-09T10:00:00Z","path":"/v1/chat/completions","model":"m1","status":"ok","duration_ms":100,"nodes":["n1"]}`,
		`{"req_id":"r2","ts":"2026-08-09T10:01:00Z","path":"/v1/messages","model":"m2","status":"fail","err_msg":"upstream error","duration_ms":200,"events":[{"type":"switch"}]}`,
	})
	csv := m.ExportCallLogCSV(100)
	lines := strings.Split(strings.TrimSuffix(csv, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("csv lines = %d, want 3: %s", len(lines), csv)
	}
	// header
	if !strings.HasPrefix(lines[0], "\uFEFFts,model,status,path,err_msg,nodes,duration_ms,req_id") {
		t.Errorf("header = %q", lines[0])
	}
	// r1 ok
	if !strings.Contains(lines[1], "m1,ok") {
		t.Errorf("row1 = %q", lines[1])
	}
	// r2 has switch event → status error
	if !strings.Contains(lines[2], "m2,error") {
		t.Errorf("row2 = %q", lines[2])
	}
	if !strings.Contains(lines[2], "upstream error") {
		t.Errorf("row2 err_msg missing: %q", lines[2])
	}
}

func TestExportInstancesAndStatsJSON(t *testing.T) {
	m := New(t.TempDir())
	_ = m.AddInstance(Instance{Name: "a1", Port: 18100, Node: "n1", Password: "sk"})
	ij, err := m.ExportInstancesJSON()
	if err != nil || !strings.Contains(ij, `"name": "a1"`) {
		t.Fatalf("instances json err=%v body=%s", err, ij)
	}
	sj, err := m.ExportStatsJSON()
	if err != nil || !strings.Contains(sj, "total_requests") {
		t.Fatalf("stats json err=%v body=%s", err, sj)
	}
}

func TestExportHandlersHTTP(t *testing.T) {
	m := New(t.TempDir())
	_ = m.AddInstance(Instance{Name: "a1", Port: 18100, Node: "n1", Password: "sk"})

	// CSV
	h := m.ExportCallLogCSVHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/export/call-log.csv", nil)
	h(rec, req)
	if rec.Code != 200 {
		t.Fatalf("csv code = %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "call-log.csv") {
		t.Errorf("csv disposition = %q", cd)
	}
	if !strings.Contains(rec.Body.String(), "ts,model,status") {
		t.Errorf("csv body = %q", rec.Body.String())
	}

	// instances.json
	h2 := m.ExportInstancesJSONHandler()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/admin/export/instances.json", nil)
	h2(rec2, req2)
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), `"name": "a1"`) {
		t.Fatalf("instances code=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// stats.json
	h3 := m.ExportStatsJSONHandler()
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/admin/export/stats.json", nil)
	h3(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("stats code = %d", rec3.Code)
	}
	var v map[string]any
	if json.Unmarshal(rec3.Body.Bytes(), &v) != nil {
		t.Fatalf("stats body not json: %s", rec3.Body.String())
	}
}
