package manager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatsByDay：按日期过滤 + 聚合正确性 + 空日期=全量。
func TestStatsByDay(t *testing.T) {
	m := New(t.TempDir())
	gwDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	if err := os.MkdirAll(gwDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"req_id":"r1","ts":"2026-08-11T10:00:00Z","model":"m1","status":"ok","prompt_tokens":10,"completion_tokens":20,"nodes":["n1"]}`,
		`{"req_id":"r2","ts":"2026-08-12T10:00:00Z","model":"m1","status":"ok","prompt_tokens":30,"completion_tokens":40,"nodes":["n2"]}`,
		`{"req_id":"r3","ts":"2026-08-12T11:00:00Z","model":"m2","status":"fail","prompt_tokens":5,"completion_tokens":0,"nodes":["n2"]}`,
		`{"req_id":"r4","ts":"2026-08-13T10:00:00Z","model":"m1","status":"ok","prompt_tokens":100,"completion_tokens":200}`,
	}
	writeCallLogFile(t, m, lines)

	// 8-12：2 请求，1 ok 1 fail，token 聚合。
	d := m.StatsByDay("2026-08-12")
	if d.TotalRequests != 2 || d.OkRequests != 1 || d.FailRequests != 1 {
		t.Fatalf("2026-08-12 reqs=%d ok=%d fail=%d, want 2/1/1", d.TotalRequests, d.OkRequests, d.FailRequests)
	}
	if d.TotalPromptTokens != 35 || d.TotalCompletionTokens != 40 || d.TotalTokens != 75 {
		t.Fatalf("2026-08-12 tokens p=%d c=%d t=%d, want 35/40/75", d.TotalPromptTokens, d.TotalCompletionTokens, d.TotalTokens)
	}
	if len(d.ByModel) != 2 {
		t.Fatalf("by_model=%d, want 2", len(d.ByModel))
	}
	if len(d.ByNode) != 1 || d.ByNode[0].Name != "n2" || d.ByNode[0].Requests != 2 {
		t.Fatalf("by_node=%+v, want [n2 req=2]", d.ByNode)
	}

	// 空日期 = 全量 4 条。
	all := m.StatsByDay("")
	if all.TotalRequests != 4 || all.TotalTokens != 405 {
		t.Fatalf("all reqs=%d tokens=%d, want 4/405", all.TotalRequests, all.TotalTokens)
	}

	// 无数据日期 → 空视图。
	empty := m.StatsByDay("2026-01-01")
	if empty.TotalRequests != 0 || empty.ByModel == nil || len(empty.ByModel) != 0 {
		t.Fatalf("empty day=%+v, want zero view", empty)
	}
}

// TestStatsByDayHandler：方法守卫 + date 参数。
func TestStatsByDayHandler(t *testing.T) {
	m := New(t.TempDir())
	h := m.StatsByDayHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/stats/by-day?date=2026-08-12", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"day":"2026-08-12"`) {
		t.Fatalf("body=%s, want day echo", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/stats/by-day", nil)
	h(rec2, req2)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", rec2.Code)
	}
}

// TestStatsByDayHandlerNoDate：无 date → 全量空日。
func TestStatsByDayHandlerNoDate(t *testing.T) {
	m := New(t.TempDir())
	h := m.StatsByDayHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/stats/by-day", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"day":""`) {
		t.Fatalf("body=%s, want empty day", rec.Body.String())
	}
}
