package manager

import (
	"os"
	"testing"
)

// TestGatewayModelsErrWhenNoMembers 验证：池成员全停（memberCount=0）时，
// 网关状态返回的 free_models_error 应为友好提示而非残留 dial tcp 错误。
func TestGatewayModelsErrWhenNoMembers(t *testing.T) {
	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		dir = "C:\\Users\\ASUS\\AppData\\Roaming\\oc2api-clean-test"
	}
	m := New(dir)
	m.SetSeams(&SeamFuncs{})
	m.SetDeps(NewRealRunner(), NewGateway(m, 0), nil)

	gw := m.Gateway()

	// 模拟残留错误（实例之前跑过、探测失败留下的）
	gw.mu.Lock()
	gw.modelsErr = `Get "http://127.0.0.1:21080/v1/models": dial tcp ... refused`
	gw.mu.Unlock()

	// 无成员（实例全停）时的状态
	st := gw.Status(m.Run())
	if st.ModelsErr == "" {
		t.Fatalf("expected friendly hint, got empty (memberCount=%d)", gw.memberCount())
	}
	if st.ModelsErr != "请先启动下方节点实例" {
		t.Fatalf("expected '请先启动下方节点实例', got %q", st.ModelsErr)
	}
	t.Logf("free_models_error = %q", st.ModelsErr)
}
