package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCleanLevel3PreservesVendors：三级清理（删除配置）必须保留 providers/routing
// ——自定义模型源定义存在 config.json，不能被"清除记录"连带清空。
func TestCleanLevel3PreservesVendors(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	cfgPath := filepath.Join(dir, "config.json")
	orig := `{
  "base_url": "https://x",
  "clash_external_url": "http://127.0.0.1:9097",
  "providers": [
    {"id": "opencode", "type": "opencode", "enabled": true},
    {"id": "src1", "type": "custom", "params": {"base_url": "https://u", "api_keys": ["k"]}}
  ],
  "routing": {"default_provider": "opencode"}
}`
	if err := os.WriteFile(cfgPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.cleanDataAt(3); err != nil {
		t.Fatalf("cleanDataAt: %v", err)
	}
	// 备份存在
	if _, err := os.Stat(cfgPath + ".bak"); err != nil {
		t.Fatal("backup .bak missing")
	}
	// 清理后配置只剩 providers/routing，且自定义源完整幸存
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not rewritten: %v", err)
	}
	var after map[string]any
	if json.Unmarshal(data, &after) != nil {
		t.Fatalf("bad json: %s", data)
	}
	if _, ok := after["base_url"]; ok {
		t.Fatalf("manager keys must be reset: %v", after)
	}
	ps, _ := after["providers"].([]any)
	if len(ps) != 2 {
		t.Fatalf("providers must survive: %v", after)
	}
	customKept := false
	for _, p := range ps {
		pm, _ := p.(map[string]any)
		if pm["type"] == "custom" && pm["id"] == "src1" {
			customKept = true
		}
	}
	if !customKept {
		t.Fatalf("custom provider lost: %v", after)
	}
	if after["routing"] == nil {
		t.Fatal("routing must survive")
	}
}
