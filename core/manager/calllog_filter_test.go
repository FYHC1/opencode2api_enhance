package manager

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// calllogFilterFixture 构造 3 条日志（与 main call_log.rs test_path 同构）：
// r1 ok(n1)、r2 fail/switch(n1,n2)、r3 ok(n3)。
func calllogFilterFixture(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir())
	lines := []string{
		`{"req_id":"r1","ts":"2026-08-05T10:00:00+08:00","model":"gpt-4o","path":"/v1/chat/completions","status":"ok","nodes":["n1"],"events":[{"type":"connect_ok","node":"n1"}]}`,
		`{"req_id":"r2","ts":"2026-08-05T10:01:00+08:00","model":"gpt-4o-mini","path":"/v1/chat/completions","status":"fail","err_msg":"boom","nodes":["n1","n2"],"events":[{"type":"switch","node":"n2"}]}`,
		`{"req_id":"r3","ts":"2026-08-05T10:02:00+08:00","model":"claude-3","path":"/v1/messages","status":"ok","nodes":["n3"]}`,
	}
	writeCallLogFile(t, m, lines)
	return m
}

func TestReadCallLogFilteredByKeyword(t *testing.T) {
	m := calllogFilterFixture(t)
	recs := m.ReadCallLogFiltered(&CallLogFilter{Keyword: "boom"})
	if len(recs) != 1 || recs[0].ReqID != "r2" {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestReadCallLogFilteredByNode(t *testing.T) {
	m := calllogFilterFixture(t)
	recs := m.ReadCallLogFiltered(&CallLogFilter{Node: "n1", Limit: 10})
	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2", len(recs))
	}
	if recs[0].ReqID != "r2" {
		t.Fatalf("latest first: recs[0] = %s, want r2", recs[0].ReqID)
	}
}

func TestReadCallLogFilteredByStatusAndPaging(t *testing.T) {
	m := calllogFilterFixture(t)
	recs := m.ReadCallLogFiltered(&CallLogFilter{Status: "error", Limit: 1})
	if len(recs) != 1 || recs[0].ReqID != "r2" {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestReadCallLogFilteredByTSRange(t *testing.T) {
	m := calllogFilterFixture(t)
	recs := m.ReadCallLogFiltered(&CallLogFilter{
		FromTS: "2026-08-05T10:01:00+08:00", ToTS: "2026-08-05T10:02:00+08:00",
	})
	if len(recs) != 2 || recs[0].ReqID != "r3" {
		t.Fatalf("recs = %+v", recs)
	}
}

func TestAggregateCallLog(t *testing.T) {
	m := calllogFilterFixture(t)
	agg := m.AggregateCallLog()
	if len(agg) != 3 {
		t.Fatalf("agg = %+v", agg)
	}
	var r2 *CallLogAggregate
	for i := range agg {
		if agg[i].Instance == "n1 → n2" {
			r2 = &agg[i]
		}
	}
	if r2 == nil || r2.Total != 1 || r2.Errors != 1 {
		t.Fatalf("r2 = %+v", r2)
	}
	var r1 *CallLogAggregate
	for i := range agg {
		if agg[i].Instance == "n1" {
			r1 = &agg[i]
		}
	}
	if r1 == nil || r1.Total != 1 || r1.Errors != 0 {
		t.Fatalf("r1 = %+v", r1)
	}
}

func TestCallLogFilteredHandlersHTTP(t *testing.T) {
	m := calllogFilterFixture(t)
	// filtered
	h := m.CallLogFilteredHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/call-log/filtered", strings.NewReader(`{"status":"error"}`))
	h(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"r2"`) {
		t.Fatalf("filtered code=%d body=%s", rec.Code, rec.Body.String())
	}
	// aggregate
	h2 := m.CallLogAggregateHandler()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/admin/call-log/aggregate", nil)
	h2(rec2, req2)
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), "n1 → n2") {
		t.Fatalf("aggregate code=%d body=%s", rec2.Code, rec2.Body.String())
	}
}
