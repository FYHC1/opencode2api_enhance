// 暴露白名单与活性探测测试。
package custom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowedModelsFiltersCatalog(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "m1"}, {"id": "m2"}, {"id": "m3"},
		}})
	}))
	defer srv.Close()

	v, err := New(Config{
		ID: "src1", BaseURL: srv.URL, Protocol: ProtoOpenAI,
		AllowedModels: []string{"m2", "m3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	models, err := v.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "src1/m2" {
		t.Fatalf("filtered models = %v", models)
	}
	// 全量清单不受过滤影响（编辑勾选用）。
	full := v.FullModelIDs()
	if len(full) != 3 || full[0] != "m1" {
		t.Fatalf("full ids = %v", full)
	}
}

func TestAllowedModelsFallbackAlsoFiltered(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "a"}, {"id": "b"}}})
	}))
	defer srv.Close()
	v, _ := New(Config{ID: "src1", BaseURL: srv.URL, Protocol: ProtoOpenAI, AllowedModels: []string{"b"}})
	if _, err := v.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail = true
	models, err := v.ListModels(context.Background()) // 缓存兜底，仍按白名单过滤
	if err != nil || len(models) != 1 || models[0].ID != "src1/b" {
		t.Fatalf("fallback filtered = %v, err = %v", models, err)
	}
}

func TestProbeOKAndFail(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	down := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m"}}})
	}))
	defer srv.Close()
	v, _ := New(Config{ID: "src1", BaseURL: srv.URL, APIKeys: []string{"k"}, Protocol: ProtoOpenAI})

	ok, latency, errStr := v.Probe(context.Background())
	if !ok || errStr != "" || latency < 0 {
		t.Fatalf("probe ok = %v %v %v", ok, latency, errStr)
	}
	if v.Health().LastSuccess == "" {
		t.Fatal("probe must refresh LastSuccess")
	}

	down = true
	ok, _, errStr = v.Probe(context.Background())
	if ok || errStr == "" {
		t.Fatalf("probe fail = %v %q", ok, errStr)
	}
	if v.Health().LastError == "" {
		t.Fatal("probe must refresh LastError")
	}
	_ = time.Second
}
