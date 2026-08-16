// auto 虚拟模型选择器：策略/权重/上下文护栏/SWRR 分布/降级链/反馈/估算 的单元行为。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/core/manager"
	chatRouter "github.com/6Kmfi6HP/opencode2api/core/router"
)

// withAutoGlobals 测试期替换全局状态（autoCfg/目录缓存/反馈/学习/SWRR/路由器），结束恢复。
func withAutoGlobals(t *testing.T, cfg manager.AutoModelCfg, models []ModelInfo) {
	t.Helper()
	oldCfg := autoCfg
	autoCfg = cfg
	modelMu.Lock()
	oldCache, oldGo, oldLoaded := modelsCache, goModelsCache, modelsLoaded
	modelsCache, goModelsCache, modelsLoaded = models, nil, true
	modelMu.Unlock()
	autoLearnedMu.Lock()
	oldLearned := autoLearnedUpper
	autoLearnedUpper = map[string]int{}
	autoLearnedMu.Unlock()
	modelFbMu.Lock()
	oldFb := modelFb
	modelFb = map[string][]modelFbSample{}
	modelFbMu.Unlock()
	autoSWRRMu.Lock()
	oldSWRR := autoSWRRCur
	autoSWRRCur = map[string]int{}
	autoSWRRMu.Unlock()
	t.Cleanup(func() {
		autoCfg = oldCfg
		modelMu.Lock()
		modelsCache, goModelsCache, modelsLoaded = oldCache, oldGo, oldLoaded
		modelMu.Unlock()
		autoLearnedMu.Lock()
		autoLearnedUpper = oldLearned
		autoLearnedMu.Unlock()
		modelFbMu.Lock()
		modelFb = oldFb
		modelFbMu.Unlock()
		autoSWRRMu.Lock()
		autoSWRRCur = oldSWRR
		autoSWRRMu.Unlock()
	})
}

func smallAutoBody() []byte {
	return []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
}

func TestPrepareAutoDisabledPassthrough(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{Strategy: manager.AutoStrategyBalanced},
		[]ModelInfo{{ID: "a-free"}})
	ctx, model, dec := prepareAuto(context.Background(), "auto", smallAutoBody())
	if dec != nil || model != "auto" || ctx != context.Background() {
		t.Fatalf("disabled auto must pass through: model=%q dec=%v", model, dec)
	}
}

func TestAutoWeightExcludeAndQualityOrder(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{
		Enabled:  true,
		Strategy: manager.AutoStrategyQuality,
		Weights:  map[string]int{"a": 0, "b": 10, "c": 5},
	}, []ModelInfo{{ID: "a-free"}, {ID: "b-free"}, {ID: "c-free"}})

	ctx, model, dec := prepareAuto(context.Background(), "auto", smallAutoBody())
	if dec == nil {
		t.Fatal("dec = nil")
	}
	if model != "b-free" {
		t.Fatalf("quality pick = %q, want b-free (weight 10)", model)
	}
	for _, c := range dec.Chain {
		if c.Upstream == "a-free" {
			t.Fatal("weight-0 model must be excluded from chain")
		}
	}
	if len(dec.Chain) != 2 {
		t.Fatalf("chain len = %d, want 2", len(dec.Chain))
	}
	_ = ctx
}

func TestAutoSWRRDistribution(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{
		Enabled:  true,
		Strategy: manager.AutoStrategyBalanced,
		Weights:  map[string]int{"a": 10, "b": 1},
	}, []ModelInfo{{ID: "a-free"}, {ID: "b-free"}})

	counts := map[string]int{}
	const rounds = 220
	for i := 0; i < rounds; i++ {
		_, model, dec := prepareAuto(context.Background(), "auto", smallAutoBody())
		if dec == nil {
			t.Fatal("dec = nil")
		}
		counts[model]++
	}
	// 权重 10:1 → 期望约 200:20（±15 容差）。
	if counts["a-free"] < 185 || counts["a-free"] > 215 {
		t.Fatalf("a-free picks = %d, want ~200", counts["a-free"])
	}
	if counts["b-free"] < 5 || counts["b-free"] > 35 {
		t.Fatalf("b-free picks = %d, want ~20", counts["b-free"])
	}
}

func TestAutoContextGuardrail(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{
		Enabled:        true,
		Strategy:       manager.AutoStrategyQuality,
		Weights:        map[string]int{"a": 10, "b": 1},
		ContextWindows: map[string]int{"a": 200000, "b": 300000},
	}, []ModelInfo{{ID: "a-free"}, {ID: "b-free"}})

	// 中文 est≈0.75 tok/字：字×320000 → est≈240k。a(200k×0.9=180k) 超限被过滤，b 命中。
	big := []byte(`{"model":"auto","messages":[{"role":"user","content":"` + strings.Repeat("字", 320000) + `"}]}`)
	_, model, dec := prepareAuto(context.Background(), "auto", big)
	if model != "b-free" || dec.ContextFallback {
		t.Fatalf("pick=%q fallback=%v, want b-free without fallback", model, dec.ContextFallback)
	}
	for _, c := range dec.Chain {
		if c.Upstream == "a-free" {
			t.Fatal("context-over model must be excluded from whole chain")
		}
	}

	// est≈600k：全部超限 → 兜底最大上下文（b），并标记 fallback。
	huge := []byte(`{"model":"auto","messages":[{"role":"user","content":"` + strings.Repeat("字", 800000) + `"}]}`)
	_, model, dec = prepareAuto(context.Background(), "auto", huge)
	if model != "b-free" || !dec.ContextFallback {
		t.Fatalf("pick=%q fallback=%v, want b-free WITH fallback", model, dec.ContextFallback)
	}

	// 学习值收紧：a 配置 1M 但实测 150k 失败 → 有效上限 150k，est≈225k 时 a 被过滤。
	learnContextFailure("a", 150000)
	med := []byte(`{"model":"auto","messages":[{"role":"user","content":"` + strings.Repeat("字", 300000) + `"}]}`)
	autoCfgMu.Lock()
	autoCfg.ContextWindows = map[string]int{"a": 1000000, "b": 1000000}
	autoCfgMu.Unlock()
	_, model, _ = prepareAuto(context.Background(), "auto", med)
	if model != "b-free" {
		t.Fatalf("learned limit not applied: pick=%q, want b-free", model)
	}
}

func TestEstimateRequestTokens(t *testing.T) {
	small := estimateRequestTokens(smallAutoBody())
	if small == 0 {
		t.Fatal("small body est = 0")
	}
	// 图像部件不计入（base64 膨胀不得推高估算），仅 text 计。
	mixed := []byte(`{"model":"auto","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"hello world"},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", 100000) + `"}}]}]}`)
	est := estimateRequestTokens(mixed)
	if est > 100 {
		t.Fatalf("image part leaked into estimate: est=%d", est)
	}
	// 中文按 ~0.5-0.75 tok/字 保守估算。
	cn := estimateRequestTokens([]byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("好", 1000) + `"}]}`))
	if cn < 500 || cn > 800 {
		t.Fatalf("chinese est = %d, want 500..800", cn)
	}
}

func TestModelFeedbackStats(t *testing.T) {
	recordModelFeedback("m-free", "n1", true, 100)
	recordModelFeedback("m-free", "n2", false, 0)
	recordModelFeedback("m-free", "n2", true, 300)
	recordModelFeedback("other-free", "n1", false, 0)
	sr, avg := modelFeedbackStats("m-free")
	if sr != 2.0/3.0 {
		t.Fatalf("sr = %v, want 2/3", sr)
	}
	if avg != 200 { // 成功样本均延迟 (100+300)/2
		t.Fatalf("avg = %d, want 200", avg)
	}
	if sr2, _ := modelFeedbackStats("cold-free"); sr2 != 1.0 {
		t.Fatalf("cold model sr = %v, want 1.0 (不惩罚冷启动)", sr2)
	}
	// auto 虚拟名不记录。
	recordModelFeedback("auto", "n1", true, 10)
	if _, ok := modelFbLoad()["auto\x1fn1"]; ok {
		t.Fatal("auto virtual model must not be recorded")
	}
}

func modelFbLoad() map[string][]modelFbSample {
	modelFbMu.Lock()
	defer modelFbMu.Unlock()
	out := make(map[string][]modelFbSample, len(modelFb))
	for k, v := range modelFb {
		out[k] = v
	}
	return out
}

func TestIsContextLimitError(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, true},
		{`{"error":{"message":"too many tokens in request"}}`, true},
		{`{"error":{"code":"context_length_exceeded"}}`, true},
		{`{"error":{"message":"upstream unavailable"}}`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isContextLimitError([]byte(c.body)); got != c.want {
			t.Fatalf("isContextLimitError(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

// autoScriptVendor 按模型脚本化的测试厂商（auto 降级链端到端）。
type autoScriptVendor struct {
	mu       sync.Mutex
	models   []string
	perModel map[string][]contract.Reply
	calls    map[string]int
}

func (v *autoScriptVendor) ID() string   { return "opencode" }
func (v *autoScriptVendor) Name() string { return "OpenCode" }
func (v *autoScriptVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	out := make([]contract.Model, 0, len(v.models))
	for _, m := range v.models {
		out = append(out, contract.Model{ID: m, Provider: "opencode", Free: true})
	}
	return out, nil
}
func (v *autoScriptVendor) IsFree(string) bool        { return true }
func (v *autoScriptVendor) Auth(*http.Request) string { return "" }
func (v *autoScriptVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{Switchable: []int{http.StatusTooManyRequests}}
}
func (v *autoScriptVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{Available: true}
}

func (v *autoScriptVendor) script(model string, replies ...contract.Reply) {
	v.perModel[model] = replies
}

func (v *autoScriptVendor) next(model string) contract.Reply {
	rs := v.perModel[model]
	if len(rs) == 0 {
		return contract.Reply{Status: http.StatusInternalServerError}
	}
	c := v.calls[model]
	if c >= len(rs) {
		return rs[len(rs)-1]
	}
	return rs[c]
}

func (v *autoScriptVendor) Chat(_ context.Context, msg *contract.Message) (*contract.Reply, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls[msg.Model]++
	r := v.next(msg.Model)
	if r.Status >= 200 && r.Status < 300 {
		return &r, nil
	}
	return &r, errors.New("upstream error")
}

func (v *autoScriptVendor) ChatStream(_ context.Context, msg *contract.Message) (*contract.Stream, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls[msg.Model]++
	r := v.next(msg.Model)
	st := &contract.Stream{ReadCloser: io.NopCloser(strings.NewReader(string(r.Body))), Status: r.Status}
	if r.Status >= 200 && r.Status < 300 {
		return st, nil
	}
	return st, errors.New("upstream error")
}

func TestAutoDegradeChainEndToEnd(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{
		Enabled:  true,
		Strategy: manager.AutoStrategyQuality,
		Weights:  map[string]int{"a": 10, "b": 1},
	}, []ModelInfo{{ID: "a-free"}, {ID: "b-free"}})

	v := &autoScriptVendor{
		models:   []string{"a-free", "b-free"},
		perModel: map[string][]contract.Reply{},
		calls:    map[string]int{},
	}
	// a 权重高为主选但 429；b 为降级候选，成功。
	v.script("a-free", contract.Reply{Status: http.StatusTooManyRequests, Body: []byte(`{"error":"rate limited"}`)})
	v.script("b-free", contract.Reply{Status: http.StatusOK, Body: []byte(`{"id":"ok","choices":[]}`)})

	agg := aggregator.New()
	agg.Register(v)
	if err := agg.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldRouter := chatRouterVar
	chatRouterVar = chatRouter.New(agg, nil, "")
	t.Cleanup(func() { chatRouterVar = oldRouter })

	ctx, model, dec := prepareAuto(context.Background(), "auto", smallAutoBody())
	if model != "a-free" || dec == nil {
		t.Fatalf("pick = %q, want a-free", model)
	}

	body, status, _, _, err := callOpenCodeAPI(ctx, smallAutoBody(), model, UpstreamAuth{Mode: AuthRoutePublic})
	if err != nil || status != http.StatusOK {
		t.Fatalf("degraded call: status=%d err=%v", status, err)
	}
	if string(body) != `{"id":"ok","choices":[]}` {
		t.Fatalf("body = %s", string(body))
	}
	if v.calls["a-free"] != 1 || v.calls["b-free"] != 1 {
		t.Fatalf("calls a=%d b=%d, want 1/1", v.calls["a-free"], v.calls["b-free"])
	}
	if dec.FinalModel != "b-free" {
		t.Fatalf("FinalModel = %q, want b-free", dec.FinalModel)
	}
	// 尝试级反馈：a 失败、b 成功（scripted vendor 无 NodeAddr → 不记录，验证无 panic 即可）。
	if sr, _ := modelFeedbackStats("a-free"); sr != 1.0 {
		t.Fatalf("a-free sr = %v (no-addr attempts should not record)", sr)
	}
}

func TestListModelsAutoBadge(t *testing.T) {
	withAutoGlobals(t, manager.AutoModelCfg{Enabled: true},
		[]ModelInfo{{ID: "a-free", Object: "model", OwnedBy: "opencode"}})

	w := httptest.NewRecorder()
	listModelsHandler(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var list struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) == 0 || list.Data[0].ID != "auto" {
		t.Fatalf("auto should be first, got %v", list.Data)
	}
	if len(list.Data) != 2 || list.Data[1].ID != "a" {
		t.Fatalf("second model should be stripped name 'a', got %v", list.Data)
	}

	// 关闭后不出现。
	autoCfgMu.Lock()
	autoCfg.Enabled = false
	autoCfgMu.Unlock()
	w2 := httptest.NewRecorder()
	listModelsHandler(w2, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if strings.Contains(w2.Body.String(), `"id":"auto"`) {
		t.Fatal("auto must be hidden when disabled")
	}
}
