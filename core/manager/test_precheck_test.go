package manager

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestTestInstanceNotRunning 验证：实例未启动时 test 返回友好提示，而非裸 TCP 错误。
func TestTestInstanceNotRunning(t *testing.T) {
	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		dir = "C:\\Users\\ASUS\\AppData\\Roaming\\oc2api-clean-test"
	}
	m := New(dir)
	m.SetSeams(&SeamFuncs{})
	m.SetDeps(NewRealRunner(), NewGateway(m, 0), nil)

	if err := m.AddInstance(Instance{Name: "not-running", Port: 30011, Node: "n", Password: "sk-x", SingboxPort: 40011}); err != nil {
		t.Fatalf("AddInstance: %v", err)
	}
	defer m.RemoveInstance("not-running")

	// 走 HTTP handler 完整链路
	req := httptest.NewRequest("POST", "/api/admin/instances/test", strings.NewReader(`{"name":"not-running"}`))
	w := httptest.NewRecorder()
	m.InstancesTestHandler()(w, req)

	var res TestResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	if res.OK {
		t.Fatalf("expected OK=false for stopped instance, got %+v", res)
	}
	if res.Message == "" {
		t.Fatalf("expected friendly message, got empty")
	}
	if strings.Contains(res.Message, "dial tcp") || strings.Contains(res.Message, "connectex") {
		t.Fatalf("expected friendly message, got raw tcp error: %q", res.Message)
	}
	if !strings.Contains(res.Message, "请先启动") {
		t.Fatalf("expected '请先启动' hint, got: %q", res.Message)
	}
	t.Logf("message: %s", res.Message)
}
