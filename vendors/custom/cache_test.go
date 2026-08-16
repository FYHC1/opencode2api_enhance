// 自定义模型源目录磁盘缓存测试（stale-while-revalidate 语义）。
package custom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestModelsDiskCacheSurvivesRestart：成功拉取落盘 → 模拟重启（新实例 + 上游已死）
// → ListModels 直接给出缓存目录。
func TestModelsDiskCacheSurvivesRestart(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}, {"id": "m2"}}})
	}))
	defer srv.Close()

	v1 := newTestVendor(t, ProtoOpenAI, srv.URL)
	if _, err := v1.ListModels(context.Background()); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := os.Stat(v1.cachePath()); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	// 模拟重启：全新实例（New 预热磁盘缓存），上游不可用。
	fail = true
	v2, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKey: "k", Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	models, err := v2.ListModels(context.Background())
	if err != nil {
		t.Fatalf("restart fetch should fall back to disk cache: %v", err)
	}
	if len(models) != 2 || models[0].ID != "src1/m1" {
		t.Fatalf("cached models = %v", models)
	}
}

// TestModelsDiskCacheRejectsTampered：缓存内容非本源前缀/厂商标识时丢弃。
func TestModelsDiskCacheRejectsTampered(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	v, err := New(Config{ID: "src1", BaseURL: "https://u", Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(v.cachePath())
	_ = os.MkdirAll(dir, 0o755)
	tampered := `{"saved_at":"2026-01-01T00:00:00Z","models":[
		{"id":"other/x","provider":"other","free":true},
		{"id":"src1/ok","provider":"src1","free":true}
	]}`
	if err := os.WriteFile(v.cachePath(), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	got := v.loadModelsCache()
	if len(got) != 1 || got[0].ID != "src1/ok" {
		t.Fatalf("tampered cache = %v, want only src1/ok", got)
	}
}

// TestModelsEmptyListKeepsCache：上游返回空列表（异常抖动）不清空既有目录。
func TestModelsEmptyListKeepsCache(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	empty := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if empty {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}}})
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)
	if _, err := v.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	empty = true
	models, err := v.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "src1/m1" {
		t.Fatalf("empty-list response must keep cache: models=%v err=%v", models, err)
	}
}
