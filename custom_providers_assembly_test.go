// 自定义模型源装配链路测试（main 包）：providers[].params 合并、custom 条目装配、
// /v1/models 展示层并入。全部 httptest，不触网。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// setProvidersCfg 临时替换 providersCfg（测试结束恢复）。
func setProvidersCfg(t *testing.T, cfgs []ProviderCfg) {
	t.Helper()
	configMu.Lock()
	old := providersCfg
	providersCfg = cfgs
	configMu.Unlock()
	t.Cleanup(func() {
		configMu.Lock()
		providersCfg = old
		configMu.Unlock()
	})
}

func TestMergeVendorParamsUnderscoreProtected(t *testing.T) {
	merged := mergeVendorParams("custom", map[string]any{
		"base_url":   "https://u",
		"_transport": "FAKE", // 运行时注入键不可被配置覆盖
	})
	if merged["base_url"] != "https://u" {
		t.Fatalf("entry param missing: %v", merged)
	}
	if merged["_transport"] == "FAKE" {
		t.Fatal("underscore runtime key must not be overridable from config")
	}
	if _, ok := merged["_transport"].(contract.Transport); !ok {
		t.Fatalf("custom type must inject Transport, got %T", merged["_transport"])
	}
}

func TestNewAggregatorCustomEntry(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir()) // 自定义源磁盘缓存隔离
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "glm-4.7"}}})
	}))
	defer srv.Close()

	setProvidersCfg(t, []ProviderCfg{
		{ID: "customtest", Type: "custom", Params: map[string]any{
			"base_url": srv.URL,
			"api_key":  "k",
			"protocol": "openai",
		}},
	})
	agg := newAggregator()
	var found contract.Vendor
	for _, v := range agg.Vendors() {
		if v.ID() == "customtest" {
			found = v
		}
	}
	if found == nil {
		t.Fatalf("custom vendor not assembled: %v", agg.Vendors())
	}
	models, err := found.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "customtest/glm-4.7" {
		t.Fatalf("ListModels = %v, err = %v", models, err)
	}
}

func TestNewAggregatorAutoRegisterSkipsCustom(t *testing.T) {
	setProvidersCfg(t, nil)
	agg := newAggregator()
	for _, v := range agg.Vendors() {
		if v.ID() == "custom" || strings.Contains(v.ID(), "custom") && v.ID() != "opencode" {
			// 显式列表里没有 custom 条目时不应出现无参 custom 实例
			if _, ok := v.(interface{ IsFree(string) bool }); ok {
				t.Fatalf("auto-register must skip custom, got %q", v.ID())
			}
		}
	}
}

func TestAppendOtherFreeModelsIncludesCustom(t *testing.T) {
	agg := aggregator.New()
	agg.Register(&stubCustomVendor{id: "src9"})
	if err := agg.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	out := appendOtherFreeModels([]ModelInfo{{ID: "opencode-free-model"}}, agg)
	found := false
	for _, m := range out {
		if m.ID == "src9/my-model" {
			found = true
			if m.OwnedBy != "src9" {
				t.Fatalf("owned_by = %q", m.OwnedBy)
			}
		}
	}
	if !found {
		t.Fatalf("custom model not in /v1/models list: %v", out)
	}
}

// stubCustomVendor 目录返回带前缀模型的 custom 桩。
type stubCustomVendor struct{ id string }

func (s *stubCustomVendor) ID() string   { return s.id }
func (s *stubCustomVendor) Name() string { return s.id }
func (s *stubCustomVendor) ListModels(context.Context) ([]contract.Model, error) {
	return []contract.Model{{ID: s.id + "/my-model", Provider: s.id, Free: true}}, nil
}
func (s *stubCustomVendor) IsFree(string) bool { return true }
func (s *stubCustomVendor) Chat(ctx context.Context, m *contract.Message) (*contract.Reply, error) {
	return &contract.Reply{Status: http.StatusOK, Body: []byte(`{"object":"chat.completion"}`)}, nil
}
func (s *stubCustomVendor) ChatStream(ctx context.Context, m *contract.Message) (*contract.Stream, error) {
	return nil, nil
}
func (s *stubCustomVendor) Auth(*http.Request) string       { return "" }
func (s *stubCustomVendor) ErrSemantics() contract.ErrRules { return contract.ErrRules{} }
func (s *stubCustomVendor) Health() contract.VendorHealth   { return contract.VendorHealth{} }
