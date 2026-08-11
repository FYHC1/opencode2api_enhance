package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func jsonPost(t *testing.T, target string, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, target, bytes.NewReader(b))
}

func TestInstancesAddListRemoveHTTP(t *testing.T) {
	m := newTestManager(t)
	add := m.InstancesAddHandler()
	w := httptest.NewRecorder()
	add.ServeHTTP(w, jsonPost(t, "/", map[string]any{
		"name": "i1", "port": 20501, "node": "node-a", "password": "sk-x",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", w.Code, w.Body.String())
	}
	if len(m.ListInstances()) != 1 {
		t.Fatalf("list = %v", m.ListInstances())
	}
	w2 := httptest.NewRecorder()
	add.ServeHTTP(w2, jsonPost(t, "/", map[string]any{
		"name": "i1", "port": 20502, "node": "node-a",
	}))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("dup add status = %d, want 400", w2.Code)
	}
	w3 := httptest.NewRecorder()
	m.InstancesRemoveHandler().ServeHTTP(w3, jsonPost(t, "/", map[string]any{"name": "i1"}))
	if w3.Code != http.StatusOK {
		t.Fatalf("remove status=%d", w3.Code)
	}
	if len(m.ListInstances()) != 0 {
		t.Fatal("instance should be removed")
	}
}

func TestBatchAddHTTPShape(t *testing.T) {
	m := newTestManager(t)
	h := m.BatchAddHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, jsonPost(t, "/", map[string]any{
		"nodes": []map[string]any{{"node": "n1"}, {"node": "n2"}},
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("batch add status=%d body=%s", w.Code, w.Body.String())
	}
	var res BatchAddHTTPResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.AddedCount != 2 {
		t.Fatalf("added_count=%d, want 2", res.AddedCount)
	}
	if res.Added[0].Node != "n1" {
		t.Fatalf("added[0] = %+v", res.Added[0])
	}
	// 重复节点 → error
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, jsonPost(t, "/", map[string]any{
		"nodes": []map[string]any{{"node": "n1"}},
	}))
	var res2 BatchAddHTTPResult
	_ = json.Unmarshal(w2.Body.Bytes(), &res2)
	if res2.ErrorCount == 0 {
		t.Fatalf("dup should error: %+v", res2)
	}
}

func TestDataCleanHandlerViaHTTP(t *testing.T) {
	m := newTestManager(t)
	_ = os.MkdirAll(m.Paths().RuntimeDir, 0o755)
	h := m.DataCleanHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, jsonPost(t, "/", map[string]any{"level": 2}))
	if w.Code != http.StatusOK {
		t.Fatalf("clean status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(m.Paths().RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("runtime should be gone")
	}
	data, _ := os.ReadFile(m.Paths().Instances)
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("instances = %q, want []", string(data))
	}
}

func TestPortCheckAndNodesHandlers(t *testing.T) {
	m := newTestManager(t)
	// port/check?port=23 → 不可用
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/port/check?port=23", nil)
	m.PortCheckHandler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("port check status=%d", w.Code)
	}
	var pc PortCheckResult
	_ = json.Unmarshal(w.Body.Bytes(), &pc)
	if pc.Available {
		t.Fatalf("port 23 must be unavailable: %+v", pc)
	}
	// nodes（未装配接缝 → 空数组）
	w2 := httptest.NewRecorder()
	m.NodesHandler().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if w2.Code != http.StatusOK || strings.TrimSpace(w2.Body.String()) != "[]" {
		t.Fatalf("nodes=%s", w2.Body.String())
	}
	// gateway status（无成员）应返回 JSON 且不 panic
	w3 := httptest.NewRecorder()
	m.GatewayStatusHandler().ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/", nil))
	if w3.Code != http.StatusOK {
		t.Fatalf("gateway status=%d", w3.Code)
	}
}