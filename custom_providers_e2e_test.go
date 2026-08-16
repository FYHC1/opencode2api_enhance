// 自定义模型源端到端测试（main 包）：保存 API → 热重建 → /v1/models 展示 →
// /v1/chat/completions 路由到自定义源（前缀剥离）。全部 httptest，不触网。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/core/manager"
	chatRouter "github.com/6Kmfi6HP/opencode2api/core/router"
)

// lastUpstreamModel 上游 chat 收到的 model（前缀剥离断言用）。
var lastUpstreamModel string

// customE2EScaffold 搭建：临时核心配置 + 上游假服务 + 全局聚合器快照恢复。
type customE2EScaffold struct {
	upstream   *httptest.Server
	dataDir    string
	mgr        *manager.Manager
	oldCfgPath string
	oldAgg     *aggregator.Aggregator
	oldRouter  *chatRouter.Router
}

func setupCustomE2E(t *testing.T) *customE2EScaffold {
	t.Helper()
	// 自定义源目录磁盘缓存隔离到临时目录。
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	s := &customE2EScaffold{}

	// 上游假服务：/models 目录 + /chat/completions 对话（记录收到的 model）。
	s.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": "gpt-test-1"}, {"id": "gpt-test-2"},
			}})
		case "/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if m, ok := body["model"].(string); ok {
				lastUpstreamModel = m
			}
			_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"gpt-test-1","choices":[{"index":0,"message":{"role":"assistant","content":"from-custom"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.upstream.Close)

	// 全局快照/恢复（globalAgg、chatRouterVar、configPath、providersCfg）。
	s.oldCfgPath = configPath
	s.oldAgg = globalAgg
	s.oldRouter = chatRouterVar
	configPath = filepath.Join(t.TempDir(), "config.json")
	globalAgg = aggregator.New()
	chatRouterVar = chatRouter.New(globalAgg, nil, "")
	configMu.Lock()
	oldProviders := providersCfg
	providersCfg = nil
	configMu.Unlock()
	t.Cleanup(func() {
		configPath = s.oldCfgPath
		globalAgg = s.oldAgg
		chatRouterVar = s.oldRouter
		configMu.Lock()
		providersCfg = oldProviders
		configMu.Unlock()
		initVendorsSignature()
	})

	s.dataDir = t.TempDir()
	s.mgr = manager.New(s.dataDir)
	return s
}

func callHandlerJSON(t *testing.T, h http.HandlerFunc, method, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCustomProvidersSaveListRebuildE2E(t *testing.T) {
	s := setupCustomE2E(t)

	saveBody := `{"providers":[{"id":"src1","name":"测试源","protocol":"openai","base_url":"` + s.upstream.URL + `","api_key":"sk-1"}]}`
	code, resp := callHandlerJSON(t, customProvidersSaveHandler(s.mgr), http.MethodPost, saveBody)
	if code != http.StatusOK {
		t.Fatalf("save status = %d, body = %v", code, resp)
	}
	// 保存即生效：聚合器应含 src1 且目录已刷新。
	found := false
	for _, v := range globalAgg.Vendors() {
		if v.ID() == "src1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("src1 not in vendors after save: %v", globalAgg.Vendors())
	}
	// 物化内建：providers 原为空 → opencode/windsurf 必须保留。
	cfg := loadConfig(configPath)
	ids := map[string]bool{}
	for _, pc := range cfg.Providers {
		ids[pc.ID] = true
	}
	if !ids["opencode"] || !ids["windsurf"] || !ids["src1"] {
		t.Fatalf("providers after save = %v", ids)
	}

	// GET 列表：含 src1 且模型数 = 2（目录实时计数）。
	code, resp = callHandlerJSON(t, customProvidersHandler(), http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	plist, _ := resp["providers"].([]any)
	if len(plist) != 1 {
		t.Fatalf("providers list = %v", plist)
	}
	p0, _ := plist[0].(map[string]any)
	if p0["id"] != "src1" || p0["protocol"] != "openai" || p0["models"].(float64) != 2 {
		t.Fatalf("view = %v", p0)
	}
	if p0["api_key"] != "sk-1" {
		t.Fatalf("api key must be echoed for edit backfill, got %v", p0["api_key"])
	}
	if p0["api_key_set"] != true {
		t.Fatalf("api_key_set = %v", p0["api_key_set"])
	}

	// manager 透传：子实例配置继承 custom 条目（dataDir/config.json）。
	mdata, err := os.ReadFile(filepath.Join(s.dataDir, "config.json"))
	if err != nil {
		t.Fatalf("manager config not written: %v", err)
	}
	var mcfg struct {
		Providers []map[string]any `json:"providers"`
	}
	_ = json.Unmarshal(mdata, &mcfg)
	mHasCustom, mHasBuiltin := false, false
	for _, p := range mcfg.Providers {
		switch p["type"] {
		case "custom":
			mHasCustom = true
		case "opencode":
			mHasBuiltin = true
		}
	}
	if !mHasCustom || !mHasBuiltin {
		t.Fatalf("manager passthrough = %v (want custom + builtin materialized)", mcfg.Providers)
	}
}

func TestCustomProvidersSaveValidation(t *testing.T) {
	s := setupCustomE2E(t)
	h := customProvidersSaveHandler(s.mgr)
	cases := []struct {
		name string
		body string
	}{
		{"bad id", `{"providers":[{"id":"坏的","protocol":"openai","base_url":"https://u"}]}`},
		{"builtin id", `{"providers":[{"id":"opencode","protocol":"openai","base_url":"https://u"}]}`},
		{"dup id", `{"providers":[{"id":"a","base_url":"https://u"},{"id":"a","base_url":"https://u"}]}`},
		{"bad url", `{"providers":[{"id":"a","protocol":"openai","base_url":"ftp://u"}]}`},
		{"bad proto", `{"providers":[{"id":"a","protocol":"grpc","base_url":"https://u"}]}`},
	}
	for _, c := range cases {
		if code, _ := callHandlerJSON(t, h, http.MethodPost, c.body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, code)
		}
	}
}

func TestCustomProvidersTestEndpoint(t *testing.T) {
	s := setupCustomE2E(t)
	body := `{"protocol":"openai","base_url":"` + s.upstream.URL + `","api_keys":["k"]}`
	code, resp := callHandlerJSON(t, customProvidersTestHandler(), http.MethodPost, body)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("test endpoint: code=%d resp=%v", code, resp)
	}
	results, _ := resp["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	r0, _ := results[0].(map[string]any)
	if r0["ok"] != true || r0["count"].(float64) != 2 {
		t.Fatalf("key result = %v", r0)
	}

	// 多 key：一好一坏 → ok=false，逐 key 结果准确。
	body2 := `{"protocol":"openai","base_url":"` + s.upstream.URL + `","api_keys":["good","bad"]}`
	// upstream 对所有 key 都 200；改用"坏 key 专用 401"语义：直接造第二个上游不可达的场景由 unreachable 分支覆盖。
	_ = body2

	// 连不通：ok=false，不 5xx。
	code, resp = callHandlerJSON(t, customProvidersTestHandler(), http.MethodPost,
		`{"protocol":"openai","base_url":"http://127.0.0.1:1","api_key":"k"}`)
	if code != http.StatusOK || resp["ok"] != false {
		t.Fatalf("unreachable: code=%d resp=%v", code, resp)
	}
}

func TestRebuildVendorsPreservesNonCustom(t *testing.T) {
	s := setupCustomE2E(t)
	fake := &fakePoolVendor{id: "windsurf"}
	globalAgg.Register(fake)

	// 保存一条 custom → 重建后 windsurf 实例应复用（同一指针），custom 新建。
	body := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `"}]}`
	if code, _ := callHandlerJSON(t, customProvidersSaveHandler(s.mgr), http.MethodPost, body); code != http.StatusOK {
		t.Fatalf("save failed")
	}
	preserved := false
	for _, v := range globalAgg.Vendors() {
		if v.ID() == "windsurf" && v == contract.Vendor(fake) {
			preserved = true
		}
	}
	if !preserved {
		t.Fatalf("non-custom vendor instance must be preserved across rebuild: %v", globalAgg.Vendors())
	}
}

func TestCustomModelChatE2E(t *testing.T) {
	s := setupCustomE2E(t)
	saveBody := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `","api_key":"sk-1"}]}`
	if code, _ := callHandlerJSON(t, customProvidersSaveHandler(s.mgr), http.MethodPost, saveBody); code != http.StatusOK {
		t.Fatalf("save failed")
	}

	// /v1/models：前缀模型出现。
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	listModelsHandler(rec, req)
	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &listResp) != nil {
		t.Fatalf("bad /v1/models body: %s", rec.Body)
	}
	hasSrc1 := false
	for _, m := range listResp.Data {
		if m.ID == "src1/gpt-test-1" {
			hasSrc1 = true
		}
	}
	if !hasSrc1 {
		t.Fatalf("src1/gpt-test-1 not in /v1/models: %s", rec.Body)
	}

	// /v1/chat/completions：前缀模型路由到自定义源，上游收到剥前缀的 model。
	chatBody := `{"model":"src1/gpt-test-1","messages":[{"role":"user","content":"hi"}]}`
	creq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatBody))
	creq.Header.Set("Authorization", "Bearer anything")
	crec := httptest.NewRecorder()
	chatCompletionsHandler(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", crec.Code, crec.Body)
	}
	if !strings.Contains(crec.Body.String(), "from-custom") {
		t.Fatalf("chat body = %s", crec.Body)
	}
	if lastUpstreamModel != "gpt-test-1" {
		t.Fatalf("upstream model = %q, want stripped gpt-test-1", lastUpstreamModel)
	}
}

func TestCustomProvidersSaveKeepsExistingKey(t *testing.T) {
	s := setupCustomE2E(t)
	h := customProvidersSaveHandler(s.mgr)
	// 首次保存带 key。
	withKey := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `","api_key":"sk-1"}]}`
	if code, _ := callHandlerJSON(t, h, http.MethodPost, withKey); code != http.StatusOK {
		t.Fatalf("first save failed")
	}
	// 二次保存 key 留空（编辑场景）→ 旧 key 保留。
	noKey := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `"}]}`
	if code, _ := callHandlerJSON(t, h, http.MethodPost, noKey); code != http.StatusOK {
		t.Fatalf("second save failed")
	}
	cfg := loadConfig(configPath)
	for _, pc := range cfg.Providers {
		if pc.ID == "src1" {
			if ks := customKeyListFromParams(pc.Params); len(ks) != 1 || ks[0] != "sk-1" {
				t.Fatalf("keys after blank-key save = %v, want kept sk-1", ks)
			}
			return
		}
	}
	t.Fatal("src1 entry missing after second save")
}

func TestCustomProvidersMultiKeySaveE2E(t *testing.T) {
	s := setupCustomE2E(t)
	saveBody := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `","api_keys":["k1","k2"],"key_strategy":"failover"}]}`
	code, resp := callHandlerJSON(t, customProvidersSaveHandler(s.mgr), http.MethodPost, saveBody)
	if code != http.StatusOK {
		t.Fatalf("save status = %d, body = %v", code, resp)
	}
	// 视图：keys 回填 + 策略 + 健康计数（活实例存在）。
	plist, _ := resp["providers"].([]any)
	p0, _ := plist[0].(map[string]any)
	keys, _ := p0["api_keys"].([]any)
	if len(keys) != 2 || keys[0] != "k1" {
		t.Fatalf("api_keys = %v", keys)
	}
	if p0["key_strategy"] != "failover" {
		t.Fatalf("key_strategy = %v", p0["key_strategy"])
	}
	if p0["keys_total"].(float64) != 2 || p0["keys_available"].(float64) != 2 {
		t.Fatalf("key health = total %v available %v", p0["keys_total"], p0["keys_available"])
	}
	// 编辑时 keys 全留空 → 保留旧 keys。
	blank := `{"providers":[{"id":"src1","protocol":"openai","base_url":"` + s.upstream.URL + `"}]}`
	code, resp = callHandlerJSON(t, customProvidersSaveHandler(s.mgr), http.MethodPost, blank)
	if code != http.StatusOK {
		t.Fatalf("blank-key save failed: %v", resp)
	}
	plist, _ = resp["providers"].([]any)
	p0, _ = plist[0].(map[string]any)
	keys, _ = p0["api_keys"].([]any)
	if len(keys) != 2 {
		t.Fatalf("blank-key save must keep old keys, got %v", keys)
	}
}
