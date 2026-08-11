package manager

import (
	"encoding/json"
	"os"
	"testing"
)

// TestVendorConfigInjected 验证：管理器配置含多厂商时，实例子进程与网关子进程
// 生成的 opencode2api.json 透传 providers/routing（多厂商在 exe 实例/网关形态可用）。
func TestVendorConfigInjected(t *testing.T) {
	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		dir = "C:\\Users\\ASUS\\AppData\\Roaming\\oc2api-clean-test"
	}
	m := New(dir)

	// 构造含 windsurf 的管理器配置
	cfg := Config{
		ShowNodePrefix: false,
		Providers: []map[string]any{
			{"id": "opencode", "type": "opencode", "name": "OpenCode", "enabled": true},
			{"id": "windsurf", "type": "windsurf", "name": "Windsurf", "enabled": true},
		},
		Routing: map[string]any{
			"model_provider_map": map[string]any{"swe-1-6-slow": "windsurf"},
			"default_provider":   "opencode",
		},
	}
	if err := m.saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	defer os.Remove(m.configPath())

	// 1. 实例子进程配置
	instanceCfg, err := m.buildOpenCodeCfg(40001)
	if err != nil {
		t.Fatalf("buildOpenCodeCfg: %v", err)
	}
	var inst map[string]any
	if err := json.Unmarshal(instanceCfg, &inst); err != nil {
		t.Fatalf("unmarshal instance cfg: %v", err)
	}
	provs, _ := inst["providers"].([]any)
	if len(provs) != 2 {
		t.Fatalf("instance cfg providers = %d, want 2 (content: %s)", len(provs), string(instanceCfg))
	}
	routing, _ := inst["routing"].(map[string]any)
	if routing["default_provider"] != "opencode" {
		t.Fatalf("instance cfg routing missing default_provider: %s", string(instanceCfg))
	}
	t.Logf("instance cfg contains providers: OK")

	// 2. 网关子进程配置
	gwCfg, err := m.buildRouterCfg([]uint16{40001, 40002}, map[uint16]string{40001: "a", 40002: "b"}, "smart")
	if err != nil {
		t.Fatalf("buildRouterCfg: %v", err)
	}
	var gw map[string]any
	if err := json.Unmarshal(gwCfg, &gw); err != nil {
		t.Fatalf("unmarshal gw cfg: %v", err)
	}
	provs2, _ := gw["providers"].([]any)
	if len(provs2) != 2 {
		t.Fatalf("gw cfg providers = %d, want 2 (content: %s)", len(provs2), string(gwCfg))
	}
	t.Logf("gw cfg contains providers: OK")

	// 3. 无 providers 时（默认）不注入（保持与基线一致）
	m2 := New(dir + "-empty")
	_ = m2.saveConfig(Config{})
	defer os.Remove(m2.configPath() + "-empty")
	emptyCfg, err := m2.buildOpenCodeCfg(40001)
	if err != nil {
		t.Fatalf("buildOpenCodeCfg(empty): %v", err)
	}
	var empty map[string]any
	if err := json.Unmarshal(emptyCfg, &empty); err != nil {
		t.Fatalf("unmarshal empty cfg: %v", err)
	}
	if _, ok := empty["providers"]; ok {
		t.Fatalf("empty cfg should NOT contain providers: %s", string(emptyCfg))
	}
	t.Logf("empty cfg (default) no providers: OK")
}
