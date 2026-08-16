// vendors/custom 单元测试：全部基于 httptest / 内存流，不触网。
package custom

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

func newTestVendor(t *testing.T, protocol, baseURL string) *Vendor {
	t.Helper()
	v, err := New(Config{ID: "src1", Name: "Source1", BaseURL: baseURL, APIKey: "sk-test", Protocol: protocol})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want error for missing id")
	}
	if _, err := New(Config{ID: "x"}); err == nil {
		t.Fatal("want error for missing base_url")
	}
	if _, err := New(Config{ID: "x", BaseURL: "https://u"}); err != nil {
		t.Fatalf("openai should be default protocol: %v", err)
	}
	if _, err := New(Config{ID: "x", BaseURL: "https://u", Protocol: "grpc"}); err == nil {
		t.Fatal("want error for unknown protocol")
	}
}

func TestListModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-4o"}, {"id": "gpt-4o-mini"}}})
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)

	models, err := v.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if models[0].ID != "src1/gpt-4o" || models[0].Provider != "src1" || !models[0].Free {
		t.Fatalf("model[0] = %+v", models[0])
	}
}

func TestListModelsFallbackCache(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}}})
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)

	if _, err := v.ListModels(context.Background()); err != nil {
		t.Fatalf("first ListModels: %v", err)
	}
	fail = true
	models, err := v.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "src1/m1" {
		t.Fatalf("fallback cache: models=%v err=%v", models, err)
	}
}

func TestChatOpenAI(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)

	raw := `{"model":"src1/gpt-4o","messages":[{"role":"user","content":"hello"}],"temperature":0.7}`
	msg := &contract.Message{Model: "src1/gpt-4o", Extra: map[string]any{keyRawBody: []byte(raw)}}
	reply, err := v.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != http.StatusOK {
		t.Fatalf("status = %d", reply.Status)
	}
	// 上游收到的 model 必须已剥掉本源前缀。
	if m, _ := gotBody["model"].(string); m != "gpt-4o" {
		t.Fatalf("upstream model = %q, want gpt-4o", m)
	}
	if temp, _ := gotBody["temperature"].(float64); temp != 0.7 {
		t.Fatalf("temperature passthrough = %v", gotBody["temperature"])
	}
	if s, _ := gotBody["stream"].(bool); s {
		t.Fatal("non-stream chat must set stream=false")
	}
	if !strings.Contains(string(reply.Body), "chat.completion") {
		t.Fatalf("reply body passthrough: %s", reply.Body)
	}
}

func TestChatOpenAIErrorPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)

	msg := &contract.Message{Model: "src1/x", Extra: map[string]any{keyRawBody: []byte(`{"model":"src1/x","messages":[]}`)}}
	reply, err := v.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != http.StatusUnauthorized || !strings.Contains(string(reply.Body), "bad key") {
		t.Fatalf("error passthrough: status=%d body=%s", reply.Status, reply.Body)
	}
}

func TestChatStreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoOpenAI, srv.URL)

	msg := &contract.Message{Model: "src1/x", Stream: true, Extra: map[string]any{keyRawBody: []byte(`{"model":"src1/x","messages":[]}`)}}
	st, err := v.ChatStream(context.Background(), msg)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer st.Close()
	all, _ := io.ReadAll(st)
	if !strings.Contains(string(all), "[DONE]") || !strings.Contains(string(all), "\"content\":\"a\"") {
		t.Fatalf("sse passthrough: %q", all)
	}
}

func TestBuildBodyFallbackFromMessages(t *testing.T) {
	v := newTestVendor(t, ProtoOpenAI, "https://example.invalid")
	body, err := v.buildBody(&contract.Message{
		Model:    "src1/x",
		Messages: []contract.Msg{{Role: "user", Content: "hi"}},
	}, true)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		t.Fatalf("bad json: %s", body)
	}
	if m["model"] != "x" || m["stream"] != true {
		t.Fatalf("fallback body = %s", body)
	}
	msgs, _ := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", m["messages"])
	}
}

// ---------------------------------------------------------------------------
// Anthropic 协议
// ---------------------------------------------------------------------------

func TestOpenAIToAnthropicRequest(t *testing.T) {
	raw := `{
		"model": "claude-x",
		"messages": [
			{"role": "system", "content": "be nice"},
			{"role": "user", "content": "hello"},
			{"role": "user", "content": [
				{"type": "text", "text": "look"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,QUJD"}}
			]},
			{"role": "assistant", "content": "", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"sf\"}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny"}
		],
		"max_tokens": 100,
		"tools": [{"type": "function", "function": {"name": "get_weather", "description": "d", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}],
		"tool_choice": "auto"
	}`
	out, err := openAIToAnthropicRequest([]byte(raw))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var req map[string]any
	if json.Unmarshal(out, &req) != nil {
		t.Fatalf("bad json: %s", out)
	}
	if req["system"] != "be nice" {
		t.Fatalf("system = %v", req["system"])
	}
	if mt, _ := req["max_tokens"].(float64); mt != 100 {
		t.Fatalf("max_tokens = %v", req["max_tokens"])
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages len = %d: %s", len(msgs), out)
	}
	// 图片 part → base64 source。
	imgMsg, _ := msgs[1].(map[string]any)
	parts, _ := imgMsg["content"].([]any)
	second, _ := parts[1].(map[string]any)
	if second["type"] != "image" {
		t.Fatalf("image part = %v", second)
	}
	// assistant tool_calls → tool_use。
	asst, _ := msgs[2].(map[string]any)
	asstParts, _ := asst["content"].([]any)
	tu, _ := asstParts[0].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "call_1" {
		t.Fatalf("tool_use = %v", tu)
	}
	inp, _ := tu["input"].(map[string]any)
	if inp["city"] != "sf" {
		t.Fatalf("tool input = %v", tu["input"])
	}
	// tool 消息 → user 消息里的 tool_result。
	toolMsg, _ := msgs[3].(map[string]any)
	if toolMsg["role"] != "user" {
		t.Fatalf("tool result role = %v", toolMsg["role"])
	}
	trParts, _ := toolMsg["content"].([]any)
	tr, _ := trParts[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "call_1" {
		t.Fatalf("tool_result = %v", tr)
	}
	tools, _ := req["tools"].([]any)
	t0, _ := tools[0].(map[string]any)
	if _, ok := t0["input_schema"]; !ok {
		t.Fatalf("tool = %v", t0)
	}
	tc, _ := req["tool_choice"].(map[string]any)
	if tc["type"] != "auto" {
		t.Fatalf("tool_choice = %v", req["tool_choice"])
	}
}

func TestOpenAIToAnthropicRequestMaxTokensDefault(t *testing.T) {
	out, err := openAIToAnthropicRequest([]byte(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(out, &req)
	if mt, _ := req["max_tokens"].(float64); mt != defaultAnthropicMaxTokens {
		t.Fatalf("max_tokens default = %v, want %d", req["max_tokens"], defaultAnthropicMaxTokens)
	}
}

func TestAnthropicToOpenAIResponse(t *testing.T) {
	body := `{"id":"msg_1","model":"claude-x","content":[
		{"type":"text","text":"hello "},
		{"type":"text","text":"world"},
		{"type":"tool_use","id":"tu1","name":"get_weather","input":{"city":"sf"}}
	],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`
	out := anthropicToOpenAIResponse([]byte(body))
	if out == nil {
		t.Fatal("nil conversion")
	}
	var resp map[string]any
	if json.Unmarshal(out, &resp) != nil {
		t.Fatalf("bad json: %s", out)
	}
	choices, _ := resp["choices"].([]any)
	c0, _ := choices[0].(map[string]any)
	if c0["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v", c0["finish_reason"])
	}
	msg, _ := c0["message"].(map[string]any)
	if msg["content"] != "hello world" {
		t.Fatalf("content = %v", msg["content"])
	}
	tcs, _ := msg["tool_calls"].([]any)
	tc0, _ := tcs[0].(map[string]any)
	if tc0["id"] != "tu1" {
		t.Fatalf("tool_calls = %v", tcs)
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 10 || usage["total_tokens"].(float64) != 15 {
		t.Fatalf("usage = %v", usage)
	}
}

func TestAnthropicStreamConversion(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":7}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"he"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"llo"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu1","name":"w"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":":1}"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	conv := newAnthropicStreamConverter(io.NopCloser(strings.NewReader(sse)))
	all, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(all)
	for _, want := range []string{
		`"content":"he"`, `"content":"llo"`, `"reasoning_content":"hm"`,
		`"name":"w"`, `"arguments":""`,
		`"arguments":"{\"a\":1}"`, // finish 补发完整 tool 参数
		`"finish_reason":"tool_calls"`,
		`"prompt_tokens":7`, `"completion_tokens":4`, `"total_tokens":11`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream output missing %s\ngot: %s", want, out)
		}
	}
}

func TestAnthropicChatRoundTrip(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Errorf("anthropic-version = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"id":"msg_9","model":"claude-x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoAnthropic, srv.URL)

	raw := `{"model":"src1/claude-x","messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`
	msg := &contract.Message{Model: "src1/claude-x", Extra: map[string]any{keyRawBody: []byte(raw)}}
	reply, err := v.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != http.StatusOK {
		t.Fatalf("status = %d", reply.Status)
	}
	if gotReq["model"] != "claude-x" || gotReq["system"] != "s" {
		t.Fatalf("upstream request = %v", gotReq)
	}
	var resp map[string]any
	if json.Unmarshal(reply.Body, &resp) != nil {
		t.Fatalf("bad openai body: %s", reply.Body)
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v", resp["object"])
	}
}

// ---------------------------------------------------------------------------
// Gemini 协议
// ---------------------------------------------------------------------------

func TestOpenAIToGeminiRequest(t *testing.T) {
	raw := `{
		"model": "gemini-x",
		"messages": [
			{"role": "system", "content": "sys"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "", "tool_calls": [
				{"id": "c1", "type": "function", "function": {"name": "f", "arguments": "{\"k\":1}"}}
			]},
			{"role": "tool", "tool_call_id": "c1", "content": "{\"res\":\"ok\"}"}
		],
		"temperature": 0.5,
		"max_tokens": 256,
		"tools": [{"type": "function", "function": {"name": "f", "parameters": {"type": "object"}}}],
		"tool_choice": "required"
	}`
	out, err := openAIToGeminiRequest([]byte(raw))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var req map[string]any
	if json.Unmarshal(out, &req) != nil {
		t.Fatalf("bad json: %s", out)
	}
	si, _ := req["systemInstruction"].(map[string]any)
	if si == nil {
		t.Fatalf("systemInstruction missing: %s", out)
	}
	gen, _ := req["generationConfig"].(map[string]any)
	if gen["maxOutputTokens"].(float64) != 256 || gen["temperature"].(float64) != 0.5 {
		t.Fatalf("generationConfig = %v", gen)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents len = %d: %s", len(contents), out)
	}
	// assistant functionCall。
	asst, _ := contents[1].(map[string]any)
	if asst["role"] != "model" {
		t.Fatalf("assistant role = %v", asst["role"])
	}
	// tool 消息 → functionResponse（名字回查自 c1 → "f"）。
	toolMsg, _ := contents[2].(map[string]any)
	parts, _ := toolMsg["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	fr, _ := p0["functionResponse"].(map[string]any)
	if fr["name"] != "f" {
		t.Fatalf("functionResponse = %v", fr)
	}
	tc, _ := req["toolConfig"].(map[string]any)
	fcc, _ := tc["functionCallingConfig"].(map[string]any)
	if fcc["mode"] != "ANY" {
		t.Fatalf("toolConfig = %v", tc)
	}
}

func TestGeminiToOpenAIResponse(t *testing.T) {
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"},{"functionCall":{"name":"f","args":{"k":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10},"modelVersion":"gemini-x"}`
	out := geminiToOpenAIResponse([]byte(body))
	if out == nil {
		t.Fatal("nil conversion")
	}
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	choices, _ := resp["choices"].([]any)
	c0, _ := choices[0].(map[string]any)
	msg, _ := c0["message"].(map[string]any)
	if msg["content"] != "hi" {
		t.Fatalf("content = %v", msg["content"])
	}
	tcs, _ := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls = %v", tcs)
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 10 {
		t.Fatalf("usage = %v", usage)
	}
}

func TestGeminiStreamConversion(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"he"}]} }],"modelVersion":"gemini-x"}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"llo"},{"functionCall":{"name":"f","args":{"k":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`,
		"",
	}, "\n")
	conv := newGeminiStreamConverter(io.NopCloser(strings.NewReader(sse)))
	all, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(all)
	for _, want := range []string{
		`"content":"he"`, `"content":"llo"`,
		`"name":"f"`, `"arguments":"{\"k\":1}"`,
		`"finish_reason":"stop"`,
		`"prompt_tokens":2`, `"total_tokens":5`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream output missing %s\ngot: %s", want, out)
		}
	}
}

func TestGeminiListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "sk-test" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "models/gemini-2.5-flash"}}})
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoGemini, srv.URL)
	ids, err := v.proto.listModels(context.Background(), v)
	if err != nil {
		t.Fatalf("listModels: %v", err)
	}
	if len(ids) != 1 || ids[0] != "gemini-2.5-flash" {
		t.Fatalf("ids = %v", ids)
	}
}

// ---------------------------------------------------------------------------
// 注册表
// ---------------------------------------------------------------------------

func TestRegistryCreate(t *testing.T) {
	v, err := contract.Create("custom", contract.ProviderSpec{
		Type: "custom",
		ID:   "myglm",
		Name: "MyGLM",
		Params: map[string]any{
			ParamBaseURL:  "https://open.bigmodel.cn/api/paas/v4",
			ParamAPIKey:   "k",
			ParamProtocol: "openai",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.ID() != "myglm" || v.Name() != "MyGLM" {
		t.Fatalf("vendor = %s/%s", v.ID(), v.Name())
	}
}

func TestUpstreamModelPrefix(t *testing.T) {
	v := newTestVendor(t, ProtoOpenAI, "https://u")
	if got := v.upstreamModel("src1/gpt-4o"); got != "gpt-4o" {
		t.Fatalf("strip prefix = %q", got)
	}
	// 裸名（model_provider_map 强制映射场景）原样透传。
	if got := v.upstreamModel("gpt-4o"); got != "gpt-4o" {
		t.Fatalf("bare name = %q", got)
	}
}

func TestErrSemantics(t *testing.T) {
	v := newTestVendor(t, ProtoOpenAI, "https://u")
	r := v.ErrSemantics()
	found := false
	for _, s := range r.Switchable {
		if s == http.StatusUnauthorized {
			found = true
		}
	}
	if !found {
		t.Fatal("401 should be switchable")
	}
	if len(r.BadPool) != 0 {
		t.Fatalf("BadPool = %v, want empty", r.BadPool)
	}
}
