// 自定义模型源向子进程配置传播的测试。
package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readProviders(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg struct {
		Providers []map[string]any `json:"providers"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatalf("bad json in %s", path)
	}
	return cfg.Providers
}

func hasProvider(ps []map[string]any, id string) bool {
	for _, p := range ps {
		if p["id"] == id {
			return true
		}
	}
	return false
}

// TestPropagateCustomProviders：实例（已有 providers）替换 custom 保留其余；
// 网关（无 providers）物化内建 + custom；无变化不重写。
func TestPropagateCustomProviders(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	customs := []map[string]any{{
		"id": "src1", "type": "custom",
		"params": map[string]any{"base_url": "https://u", "protocol": "openai"},
	}}
	cfg := m.loadConfig()
	cfg.Providers = append([]map[string]any{
		{"id": "opencode", "type": "opencode", "enabled": true},
	}, customs...)
	if err := m.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	instCfg := filepath.Join(dir, "runtime", "inst-1", "opencode2api.json")
	writeJSONFile(t, instCfg, map[string]any{
		"port": 1234,
		"providers": []map[string]any{
			{"id": "opencode", "type": "opencode"},
			{"id": "oldsrc", "type": "custom", "params": map[string]any{"base_url": "https://old"}},
		},
	})
	gwCfg := filepath.Join(dir, "runtime", "_unified-gateway", "opencode2api.json")
	writeJSONFile(t, gwCfg, map[string]any{"active_socks5": "__round_robin__"})

	if err := m.PropagateCustomProviders(); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	ps := readProviders(t, instCfg)
	if !hasProvider(ps, "opencode") || !hasProvider(ps, "src1") || hasProvider(ps, "oldsrc") {
		t.Fatalf("instance providers = %v (want opencode+src1, oldsrc replaced)", ps)
	}
	// 其它配置键保留。
	idata, _ := os.ReadFile(instCfg)
	var ic map[string]any
	_ = json.Unmarshal(idata, &ic)
	if ic["port"].(float64) != 1234 {
		t.Fatalf("instance config keys lost: %v", ic)
	}

	gps := readProviders(t, gwCfg)
	if !hasProvider(gps, "opencode") || !hasProvider(gps, "windsurf") || !hasProvider(gps, "src1") {
		t.Fatalf("gateway providers = %v (want builtins materialized + src1)", gps)
	}

	// 幂等：无变化不重写（mtime/内容不变）。
	before, _ := os.ReadFile(instCfg)
	_ = m.PropagateCustomProviders()
	after, _ := os.ReadFile(instCfg)
	if string(before) != string(after) {
		t.Fatalf("second propagate rewrote unchanged file:\n%s\n%s", before, after)
	}

	// customs 清空：传播应移除实例里的 custom 条目。
	cfg2 := m.loadConfig()
	cfg2.Providers = []map[string]any{{"id": "opencode", "type": "opencode"}}
	_ = m.saveConfig(cfg2)
	if err := m.PropagateCustomProviders(); err != nil {
		t.Fatal(err)
	}
	ps2 := readProviders(t, instCfg)
	if hasProvider(ps2, "src1") || !hasProvider(ps2, "opencode") {
		t.Fatalf("after clearing customs = %v", ps2)
	}
}

// TestPatchProvidersFileEmptyKeepsAbsence：两边都空时不写 providers 键（保持自动注册语义）。
func TestPatchProvidersFileEmptyKeepsAbsence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode2api.json")
	writeJSONFile(t, path, map[string]any{"port": 1})
	if err := patchProvidersFile(path, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	if _, ok := cfg["providers"]; ok {
		t.Fatalf("providers key must stay absent when both sides empty: %v", cfg)
	}
}
