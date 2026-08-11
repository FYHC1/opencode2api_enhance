package manager

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeDeleteHandlersHTTP(t *testing.T) {
	m := New(t.TempDir())
	mk := func(name string) SubscribeNode {
		return SubscribeNode{Name: name, Server: "1.2.3.4", Port: 443, NodeType: "trojan", Password: "pw", TLS: true}
	}
	if err := m.saveSubscriptionCache([]SubscribeNode{mk("A"), mk("B")}); err != nil {
		t.Fatal(err)
	}

	// 单删
	h := m.NodeDeleteHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/nodes/delete", strings.NewReader(`{"name":"A"}`))
	h(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("delete code=%d body=%s", rec.Code, rec.Body.String())
	}
	// 已删 → 0
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/admin/nodes/delete", strings.NewReader(`{"name":"A"}`))
	h(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `"removed":0`) {
		t.Fatalf("re-delete body=%s", rec2.Body.String())
	}

	// 批量
	h2 := m.NodeDeleteBatchHandler()
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/api/admin/nodes/delete-batch", strings.NewReader(`{"names":["B","X"]}`))
	h2(rec3, req3)
	if rec3.Code != 200 || !strings.Contains(rec3.Body.String(), `"removed":1`) {
		t.Fatalf("batch code=%d body=%s", rec3.Code, rec3.Body.String())
	}
	if len(m.loadSubscriptionCache()) != 0 {
		t.Fatalf("cache = %+v", m.loadSubscriptionCache())
	}

	// 缺参 → 400
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest("POST", "/api/admin/nodes/delete", strings.NewReader(`{}`))
	h(rec4, req4)
	if rec4.Code != 400 {
		t.Fatalf("empty name code=%d", rec4.Code)
	}
}
