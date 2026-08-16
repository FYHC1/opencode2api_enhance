// Responses 出站协议与传播逻辑测试（httptest / 内存流，不触网）。
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

func TestOpenAIToResponsesRequest(t *testing.T) {
	raw := `{
		"model": "gpt-x",
		"messages": [
			{"role": "system", "content": "be nice"},
			{"role": "user", "content": "hi"},
			{"role": "user", "content": [
				{"type": "text", "text": "img"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,QUJD"}}
			]},
			{"role": "assistant", "content": "ok", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "f", "arguments": "{\"a\":1}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "42"}
		],
		"max_tokens": 100,
		"temperature": 0.3,
		"tools": [{"type": "function", "function": {"name": "f", "description": "d", "parameters": {"type": "object"}}}],
		"tool_choice": "auto",
		"stream": false
	}`
	out, err := openAIToResponsesRequest([]byte(raw))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var req map[string]any
	if json.Unmarshal(out, &req) != nil {
		t.Fatalf("bad json: %s", out)
	}
	if req["instructions"] != "be nice" {
		t.Fatalf("instructions = %v", req["instructions"])
	}
	if m, _ := req["max_output_tokens"].(float64); m != 100 {
		t.Fatalf("max_output_tokens = %v", req["max_output_tokens"])
	}
	input, _ := req["input"].([]any)
	// user 文本、user 图片、assistant 文本+function_call、function_call_output = 5 项。
	if len(input) != 5 {
		t.Fatalf("input len = %d: %s", len(input), out)
	}
	fc, _ := input[3].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "f" {
		t.Fatalf("function_call item = %v", fc)
	}
	fco, _ := input[4].(map[string]any)
	if fco["type"] != "function_call_output" || fco["output"] != "42" {
		t.Fatalf("function_call_output = %v", fco)
	}
	tools, _ := req["tools"].([]any)
	t0, _ := tools[0].(map[string]any)
	if t0["type"] != "function" || t0["name"] != "f" {
		t.Fatalf("tool = %v", t0)
	}
	if req["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", req["tool_choice"])
	}
}

func TestResponsesToOpenAIResponse(t *testing.T) {
	body := `{"id":"resp_1","object":"response","model":"gpt-x","status":"completed","output":[
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello "},{"type":"output_text","text":"world"}]},
		{"type":"function_call","call_id":"fc1","name":"f","arguments":"{\"a\":1}"}
	],"usage":{"input_tokens":5,"output_tokens":6,"total_tokens":11}}`
	out := responsesToOpenAIResponse([]byte(body))
	if out == nil {
		t.Fatal("nil conversion")
	}
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	choices, _ := resp["choices"].([]any)
	c0, _ := choices[0].(map[string]any)
	if c0["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v", c0["finish_reason"])
	}
	msg, _ := c0["message"].(map[string]any)
	if msg["content"] != "hello world" {
		t.Fatalf("content = %v", msg["content"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 11 {
		t.Fatalf("usage = %v", usage)
	}

	// incomplete → length。
	inc := `{"id":"r","model":"m","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"x"}]}],"usage":{"input_tokens":1,"output_tokens":2}}`
	out2 := responsesToOpenAIResponse([]byte(inc))
	var resp2 map[string]any
	_ = json.Unmarshal(out2, &resp2)
	ch, _ := resp2["choices"].([]any)
	if ch[0].(map[string]any)["finish_reason"] != "length" {
		t.Fatalf("incomplete finish = %v", ch[0])
	}
}

func TestResponsesStreamConversion(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_9","model":"gpt-x","usage":{"input_tokens":8}}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"he"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"llo"}`,
		"",
		`data: {"type":"response.reasoning_text.delta","delta":"think"}`,
		"",
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"fc1","name":"f"}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"a\""}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":":1}"}`,
		"",
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":8,"output_tokens":9,"total_tokens":17}}}`,
		"",
	}, "\n")
	conv := newResponsesStreamConverter(io.NopCloser(strings.NewReader(sse)))
	all, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(all)
	for _, want := range []string{
		`"content":"he"`, `"content":"llo"`, `"reasoning_content":"think"`,
		`"id":"resp_9"`,
		`"arguments":""`,          // added：tool name chunk
		`"arguments":"{\"a\":1}"`, // finish：完整参数
		`"finish_reason":"tool_calls"`,
		`"prompt_tokens":8`, `"completion_tokens":9`, `"total_tokens":17`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream output missing %s\ngot: %s", want, out)
		}
	}
}

func TestResponsesChatRoundTrip(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-x"}}})
		case "/responses":
			if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
				t.Errorf("Authorization = %q", got)
			}
			_ = json.NewDecoder(r.Body).Decode(&gotReq)
			_, _ = w.Write([]byte(`{"id":"resp_2","object":"response","model":"gpt-x","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	v := newTestVendor(t, ProtoResponses, srv.URL)

	// listModels 继承自 openai 协议。
	ids, err := v.proto.listModels(context.Background(), v)
	if err != nil || len(ids) != 1 || ids[0] != "gpt-x" {
		t.Fatalf("listModels = %v, err = %v", ids, err)
	}

	raw := `{"model":"src1/gpt-x","messages":[{"role":"system","content":"s"},{"role":"user","content":"q"}]}`
	msg := &contract.Message{Model: "src1/gpt-x", Extra: map[string]any{keyRawBody: []byte(raw)}}
	reply, err := v.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != http.StatusOK {
		t.Fatalf("status = %d body=%s", reply.Status, reply.Body)
	}
	if gotReq["model"] != "gpt-x" || gotReq["instructions"] != "s" || gotReq["input"] == nil {
		t.Fatalf("upstream request = %v", gotReq)
	}
	var resp map[string]any
	if json.Unmarshal(reply.Body, &resp) != nil || resp["object"] != "chat.completion" {
		t.Fatalf("converted body = %s", reply.Body)
	}
}

func TestResponsesValidationInNew(t *testing.T) {
	if _, err := New(Config{ID: "x", BaseURL: "https://u", Protocol: ProtoResponses}); err != nil {
		t.Fatalf("responses protocol must construct: %v", err)
	}
}
