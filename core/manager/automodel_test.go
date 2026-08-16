// auto 虚拟模型配置：类型规范 / 保存读取 / 子进程传播 / 管理端 HTTP 往返。
package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAutoModelNormalize(t *testing.T) {
	cfg := AutoModelCfg{
		Enabled:  true,
		Strategy: "fast??",
		Weights:  map[string]int{"a": -3, "b": 7, "c": 15},
		ContextWindows: map[string]int{
			"a": 0, "b": -100, "big": 1000000,
		},
	}
	cfg.Normalize()
	if cfg.Strategy != AutoStrategyBalanced {
		t.Fatalf("strategy = %q, want balanced", cfg.Strategy)
	}
	if cfg.Weights["a"] != 0 || cfg.Weights["b"] != 7 || cfg.Weights["c"] != 10 {
		t.Fatalf("weights clamp failed: %v", cfg.Weights)
	}
	if _, exists := cfg.ContextWindows["a"]; exists {
		t.Fatal("context a (0) should be removed")
	}
	if _, exists := cfg.ContextWindows["b"]; exists {
		t.Fatal("context b (-100) should be removed")
	}
	if cfg.ContextWindows["big"] != 1000000 {
		t.Fatalf("context big = %d", cfg.ContextWindows["big"])
	}
	// 空策略与合法策略保持。
	for s, want := range map[string]string{"": AutoStrategyBalanced, "speed": "speed", "quality": "quality", "balanced": "balanced"} {
		c := AutoModelCfg{Strategy: s}
		c.Normalize()
		if c.Strategy != want {
			t.Fatalf("strategy %q normalized to %q, want %q", s, c.Strategy, want)
		}
	}
}

func TestAutoModelSetGetRoundtrip(t *testing.T) {
	m := newTestManager(t)
	if got := m.AutoModel(); got.Enabled || got.Strategy != AutoStrategyBalanced {
		t.Fatalf("default = %+v, want disabled+balanced", got)
	}
	in := AutoModelCfg{
		Enabled:        true,
		Strategy:       AutoStrategySpeed,
		Weights:        map[string]int{"deepseek-v4-flash": 9, "big-pickle": 3},
		ContextWindows: map[string]int{"deepseek-v4-flash": 200000, "big-pickle": 1000000},
	}
	if err := m.SetAutoModel(in); err != nil {
		t.Fatal(err)
	}
	got := m.AutoModel()
	if !got.Enabled || got.Strategy != AutoStrategySpeed || got.Weights["deepseek-v4-flash"] != 9 ||
		got.ContextWindows["big-pickle"] != 1000000 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// 全空保存 = 未配置语义（键清除）。
	if err := m.SetAutoModel(AutoModelCfg{}); err != nil {
		t.Fatal(err)
	}
	if got := m.AutoModel(); got.Enabled || len(got.Weights) != 0 {
		t.Fatalf("empty save should reset, got %+v", got)
	}
}

func TestPatchAutoModelFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode2api.json")
	seed := `{"model_alias":{"x":"y"},"route_mode":"smart","auto_model":{"enabled":true,"strategy":"quality"}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &AutoModelCfg{
		Enabled:        true,
		Strategy:       AutoStrategyBalanced,
		Weights:        map[string]int{"m1": 8},
		ContextWindows: map[string]int{"m1": 200000},
	}
	if err := patchAutoModelFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(readFileT(t, path), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["model_alias"]; !ok {
		t.Fatal("patch dropped model_alias")
	}
	if doc["route_mode"] != "smart" {
		t.Fatal("patch dropped route_mode")
	}
	am, _ := doc["auto_model"].(map[string]any)
	if am == nil || am["strategy"] != "balanced" {
		t.Fatalf("auto_model not replaced: %v", doc["auto_model"])
	}
	// 幂等：同配置再 patch 不改文件内容。
	before := readFileT(t, path)
	if err := patchAutoModelFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readFileT(t, path)) {
		t.Fatal("idempotent patch rewrote file")
	}
	// 语义等价（结构体字段序 vs 文件 map 键序）也不重写。
 reordered := `{"auto_model":{"weights":{"m1":8},"strategy":"balanced","enabled":true,"context_windows":{"m1":200000}},"model_alias":{"x":"y"},"route_mode":"smart"}`
	if err := os.WriteFile(path, []byte(reordered), 0o644); err != nil {
		t.Fatal(err)
	}
	before = readFileT(t, path)
	if err := patchAutoModelFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readFileT(t, path)) {
		t.Fatal("semantically-equal patch rewrote file")
	}
	// nil = 删除键，其余保留。
	if err := patchAutoModelFile(path, nil); err != nil {
		t.Fatal(err)
	}
	doc = nil
	if err := json.Unmarshal(readFileT(t, path), &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["auto_model"]; exists {
		t.Fatal("nil cfg should remove auto_model")
	}
	if _, ok := doc["model_alias"]; !ok {
		t.Fatal("nil patch dropped model_alias")
	}
}

func TestSetAutoModelPropagatesToChildren(t *testing.T) {
	m := newTestManager(t)
	// 伪造两个子进程配置（实例 + 网关）。
	instDir := filepath.Join(m.paths.RuntimeDir, "inst-a")
	gwDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	for _, d := range []string{instDir, gwDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "opencode2api.json"), []byte(`{"route_mode":"smart"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetAutoModel(AutoModelCfg{Enabled: true, Weights: map[string]int{"m1": 6}}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{instDir, gwDir} {
		var doc map[string]any
		if err := json.Unmarshal(readFileT(t, filepath.Join(d, "opencode2api.json")), &doc); err != nil {
			t.Fatal(err)
		}
		am, _ := doc["auto_model"].(map[string]any)
		if am == nil || am["enabled"] != true {
			t.Fatalf("%s missing auto_model: %v", d, doc)
		}
	}
}

func TestAutoModelConfigHandlerRoundtrip(t *testing.T) {
	m := newTestManager(t)
	h := m.AutoModelConfigHandler()

	// GET 默认值。
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d", w.Code)
	}
	var cur AutoModelCfg
	if json.Unmarshal(w.Body.Bytes(), &cur) != nil || cur.Enabled {
		t.Fatalf("get body = %s", w.Body.String())
	}

	// POST 保存。
	body, _ := json.Marshal(AutoModelCfg{
		Enabled:        true,
		Strategy:       "weird",
		Weights:        map[string]int{"m1": 99},
		ContextWindows: map[string]int{"m1": 300000},
	})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("post status=%d body=%s", w.Code, w.Body.String())
	}
	got := m.AutoModel()
	if !got.Enabled || got.Strategy != AutoStrategyBalanced || got.Weights["m1"] != 10 {
		t.Fatalf("saved cfg not normalized: %+v", got)
	}

	// 非 GET/POST → 405。
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete status=%d, want 405", w.Code)
	}
}

func readFileT(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
