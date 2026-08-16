// Google Gemini 协议出站适配：OpenAI Chat 形态 ⇄ Gemini generateContent。
// 请求：system → systemInstruction、assistant tool_calls → functionCall、
// tool 消息 → functionResponse（名字回查自对应 tool_call）。
// 响应/SSE：转回 OpenAI Chat 形态（usage/finishReason 映射）。
package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/core/protocol"
)

type geminiProto struct{}

func (geminiProto) headers(v *Vendor, stream bool) map[string]string {
	h := map[string]string{
		"Content-Type":   "application/json",
		"x-goog-api-key": v.cfg.APIKey,
	}
	if stream {
		h["Accept"] = "text/event-stream"
	}
	return h
}

// geminiModelsPath 模型目录端点。
func (geminiProto) listModels(ctx context.Context, v *Vendor) ([]string, error) {
	resp, _, err := v.do(ctx, http.MethodGet, v.cfg.BaseURL+"/models?pageSize=1000",
		map[string]string{"x-goog-api-key": v.cfg.APIKey}, nil, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("list models: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return nil, fmt.Errorf("custom %s: list models HTTP %d", v.cfg.ID, resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("custom %s: bad models response: %w", v.cfg.ID, err)
	}
	ids := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		// name 形如 "models/gemini-2.5-flash" → 取模型名。
		id := strings.TrimPrefix(m.Name, "models/")
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// 请求：OpenAI → Gemini
// ---------------------------------------------------------------------------

// openAIToGeminiRequest 把 OpenAI Chat 请求体转为 Gemini generateContent 请求。
func openAIToGeminiRequest(raw []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("bad openai body: %w", err)
	}
	out := map[string]any{}
	genCfg := map[string]any{}
	if m, _ := req["model"].(string); m != "" {
		out["model"] = m
	}
	if n, ok := protocol.NumberAsFloat(req["temperature"]); ok {
		genCfg["temperature"] = n
	}
	if n, ok := protocol.NumberAsFloat(req["top_p"]); ok {
		genCfg["topP"] = n
	}
	if n, ok := protocol.NumberAsFloat(req["max_tokens"]); ok && n > 0 {
		genCfg["maxOutputTokens"] = int(n)
	}
	if s, ok := req["stop"].(string); ok && s != "" {
		genCfg["stopSequences"] = []string{s}
	} else if arr, ok := req["stop"].([]any); ok && len(arr) > 0 {
		genCfg["stopSequences"] = arr
	}
	if len(genCfg) > 0 {
		out["generationConfig"] = genCfg
	}

	var systemParts []string
	msgs, _ := req["messages"].([]any)
	contents := make([]map[string]any, 0, len(msgs))
	// tool_call_id → function 名（tool 消息的 functionResponse 需要名字）。
	toolNames := map[string]string{}
	for _, rawMsg := range msgs {
		m, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "developer":
			if s := contentToText(m["content"]); s != "" {
				systemParts = append(systemParts, s)
			}
		case "user":
			contents = append(contents, map[string]any{"role": "user", "parts": openAIContentToGeminiParts(m["content"])})
		case "assistant":
			parts := openAIContentToGeminiParts(m["content"])
			if tcs, ok := m["tool_calls"].([]any); ok {
				for _, rawTC := range tcs {
					tc, ok := rawTC.(map[string]any)
					if !ok {
						continue
					}
					fn, _ := tc["function"].(map[string]any)
					if fn == nil {
						continue
					}
					name, _ := fn["name"].(string)
					argsStr, _ := fn["arguments"].(string)
					var args any = map[string]any{}
					if argsStr != "" {
						var parsed any
						if json.Unmarshal([]byte(argsStr), &parsed) == nil {
							args = parsed
						}
					}
					if id, _ := tc["id"].(string); id != "" {
						toolNames[id] = name
					}
					parts = append(parts, map[string]any{"functionCall": map[string]any{"name": name, "args": args}})
				}
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case "tool":
			toolID, _ := m["tool_call_id"].(string)
			name := toolNames[toolID]
			if name == "" {
				name = toolID
			}
			var result any = contentToText(m["content"])
			if s, ok := m["content"].(string); ok && s != "" {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) == nil {
					result = parsed
				}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{{
				"functionResponse": map[string]any{"name": name, "response": result},
			}}})
		}
	}
	if len(systemParts) > 0 {
		out["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": strings.Join(systemParts, "\n\n")}}}
	}
	out["contents"] = contents

	// tools → functionDeclarations。
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		fds := make([]map[string]any, 0, len(tools))
		for _, rawTool := range tools {
			t, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := t["function"].(map[string]any)
			if fn == nil {
				continue
			}
			fd := map[string]any{"name": fn["name"]}
			if d, ok := fn["description"].(string); ok {
				fd["description"] = d
			}
			if p, ok := fn["parameters"]; ok {
				fd["parameters"] = protocol.CleanJSONSchema(p)
			}
			fds = append(fds, fd)
		}
		if len(fds) > 0 {
			out["tools"] = []map[string]any{{"functionDeclarations": fds}}
			// tool_choice → Gemini mode。
			switch tc := req["tool_choice"].(type) {
			case string:
				if tc == "required" {
					out["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}
				}
			case map[string]any:
				if fn, ok := tc["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok {
						out["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{
							"mode": "ANY", "allowedFunctionNames": []string{n},
						}}
					}
				}
			}
		}
	}
	return json.Marshal(out)
}

// openAIContentToGeminiParts OpenAI content → Gemini parts（text / inlineData）。
func openAIContentToGeminiParts(content any) []map[string]any {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]any{{"text": c}}
	case []any:
		out := make([]map[string]any, 0, len(c))
		for _, rawPart := range c {
			p, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			switch t, _ := p["type"].(string); t {
			case "text", "input_text":
				s, _ := p["text"].(string)
				out = append(out, map[string]any{"text": s})
			case "image_url", "input_image":
				var u string
				switch iu := p["image_url"].(type) {
				case string:
					u = iu
				case map[string]any:
					u, _ = iu["url"].(string)
				}
				if media, data, ok := parseDataURL(u); ok {
					out = append(out, map[string]any{"inlineData": map[string]any{"mimeType": media, "data": data}})
				}
			}
		}
		return out
	}
	return nil
}

// ---------------------------------------------------------------------------
// 响应：Gemini → OpenAI
// ---------------------------------------------------------------------------

// geminiFinishReason finishReason → finish_reason。
func geminiFinishReason(r string) string {
	switch r {
	case "STOP", "":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST":
		return "content_filter"
	case "FunctionCall", "functionCall":
		return "tool_calls"
	}
	return "stop"
}

// geminiToOpenAIResponse 把 Gemini generateContent 响应转为 OpenAI Chat 响应体。
// 解析失败返回 nil（调用方原样透传由上层报错）。
func geminiToOpenAIResponse(body []byte) []byte {
	var resp struct {
		Candidates []struct {
			Content struct {
				Role  string `json:"role"`
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata map[string]any `json:"usageMetadata"`
		Model         string         `json:"modelVersion"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Candidates) == 0 {
		return nil
	}
	cand := resp.Candidates[0]
	var text strings.Builder
	var toolCalls []map[string]any
	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, map[string]any{
				"id":       "call_" + part.FunctionCall.Name,
				"type":     "function",
				"function": map[string]any{"name": part.FunctionCall.Name, "arguments": string(args)},
			})
		}
	}
	msg := map[string]any{"role": "assistant"}
	if text.Len() > 0 {
		msg["content"] = text.String()
	} else {
		msg["content"] = nil
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	openai := map[string]any{
		"id":      "gemini-" + randomID(),
		"object":  "chat.completion",
		"created": nowUnix(),
		"model":   resp.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": geminiFinishReason(cand.FinishReason),
		}},
		"usage": geminiUsageToChat(resp.UsageMetadata),
	}
	out, err := json.Marshal(openai)
	if err != nil {
		return nil
	}
	return out
}

// geminiUsageToChat usageMetadata → OpenAI usage。
func geminiUsageToChat(u map[string]any) map[string]any {
	if u == nil {
		return map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	}
	pt, _ := protocol.NumberAsFloat(u["promptTokenCount"])
	ct, _ := protocol.NumberAsFloat(u["candidatesTokenCount"])
	tt, _ := protocol.NumberAsFloat(u["totalTokenCount"])
	if tt == 0 {
		tt = pt + ct
	}
	return map[string]any{
		"prompt_tokens":     int64(pt),
		"completion_tokens": int64(ct),
		"total_tokens":      int64(tt),
	}
}

// randomID 短随机串（响应 id 兜底）。
func randomID() string {
	return fmt.Sprintf("%d", nowUnix())
}

// ---------------------------------------------------------------------------
// Chat / ChatStream
// ---------------------------------------------------------------------------

func (p geminiProto) chat(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Reply, error) {
	gemBody, err := openAIToGeminiRequest(rawBody)
	if err != nil {
		return nil, fmt.Errorf("custom %s: %w", v.cfg.ID, err)
	}
	url := v.cfg.BaseURL + "/models/" + model + ":generateContent"
	resp, addr, err := v.do(ctx, http.MethodPost, url, p.headers(v, false), gemBody, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("chat: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return &contract.Reply{Body: body, Status: resp.StatusCode, NodeAddr: addr}, nil
	}
	converted := geminiToOpenAIResponse(body)
	if converted == nil {
		v.markErr("chat: bad gemini response body")
		return &contract.Reply{Body: body, Status: http.StatusBadGateway, NodeAddr: addr}, nil
	}
	v.markOK()
	return &contract.Reply{Body: converted, Status: resp.StatusCode, NodeAddr: addr}, nil
}

func (p geminiProto) chatStream(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Stream, error) {
	gemBody, err := openAIToGeminiRequest(rawBody)
	if err != nil {
		return nil, fmt.Errorf("custom %s: %w", v.cfg.ID, err)
	}
	url := v.cfg.BaseURL + "/models/" + model + ":streamGenerateContent?alt=sse"
	resp, addr, err := v.do(ctx, http.MethodPost, url, p.headers(v, true), gemBody, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := readBody(resp)
		v.markErr(fmt.Sprintf("chat stream: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return &contract.Stream{ReadCloser: nopCloser{bytes.NewReader(body)}, Status: resp.StatusCode, NodeAddr: addr}, nil
	}
	v.markOK()
	return &contract.Stream{ReadCloser: newGeminiStreamConverter(resp.Body), Status: resp.StatusCode, NodeAddr: addr}, nil
}

// ---------------------------------------------------------------------------
// 流：Gemini SSE → OpenAI Chat SSE
// ---------------------------------------------------------------------------

// geminiStreamState 流转换状态。
type geminiStreamState struct {
	cc         chunkCtx
	promptTok  float64
	outputTok  float64
	stopReason string
	toolCount  int // 已发 functionCall chunk 数（OpenAI tool index）
}

// newGeminiStreamConverter 构造 Gemini SSE → OpenAI chunk 转换器。
func newGeminiStreamConverter(rc io.ReadCloser) *sseConverter {
	st := &geminiStreamState{}
	transform := func(ev *sseEvent, w *bytes.Buffer) { st.transform(ev, w) }
	finish := func(w *bytes.Buffer) { st.finish(w) }
	return newSSEConverter(rc, transform, finish)
}

func (st *geminiStreamState) transform(ev *sseEvent, w *bytes.Buffer) {
	var d struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata map[string]any `json:"usageMetadata"`
		ModelVersion  string         `json:"modelVersion"`
	}
	if json.Unmarshal(ev.data, &d) != nil {
		return
	}
	if d.ModelVersion != "" {
		st.cc.model = d.ModelVersion
	}
	if n, ok := protocol.NumberAsFloat(d.UsageMetadata["promptTokenCount"]); ok {
		st.promptTok = n
	}
	if n, ok := protocol.NumberAsFloat(d.UsageMetadata["candidatesTokenCount"]); ok {
		st.outputTok = n
	}
	if len(d.Candidates) == 0 {
		return
	}
	cand := d.Candidates[0]
	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			writeChunk(w, &st.cc, map[string]any{"content": part.Text}, nil)
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			writeChunk(w, &st.cc, map[string]any{
				"tool_calls": []map[string]any{{
					"index": st.toolCount, "id": "call_" + part.FunctionCall.Name, "type": "function",
					"function": map[string]any{"name": part.FunctionCall.Name, "arguments": string(args)},
				}},
			}, nil)
			st.toolCount++
		}
	}
	if cand.FinishReason != "" {
		st.stopReason = cand.FinishReason
	}
}

// finish 流结束：补 finish chunk、usage chunk 与 [DONE]。
func (st *geminiStreamState) finish(w *bytes.Buffer) {
	finish := geminiFinishReason(st.stopReason)
	writeChunk(w, &st.cc, map[string]any{}, &finish)
	writeUsageDone(w, &st.cc, st.promptTok, st.outputTok)
}
