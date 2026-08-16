// Anthropic Messages 协议出站适配：OpenAI Chat 形态 ⇄ Anthropic /v1/messages。
// 请求：system 抽取、多模态图片转 base64 source、tool_calls/tool 消息转 tool_use/tool_result。
// 响应/SSE：转回 OpenAI Chat 形态（含 usage 与 finish_reason 映射）。
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

const anthropicVersion = "2023-06-01"

// defaultAnthropicMaxTokens Anthropic 必填 max_tokens 的兜底值（请求未带时）。
const defaultAnthropicMaxTokens = 8192

type anthropicProto struct{}

func (anthropicProto) headers(v *Vendor, stream bool) map[string]string {
	h := map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         v.cfg.APIKey,
		"anthropic-version": anthropicVersion,
	}
	if stream {
		h["Accept"] = "text/event-stream"
	}
	return h
}

func (anthropicProto) listModels(ctx context.Context, v *Vendor) ([]string, error) {
	resp, _, err := v.do(ctx, http.MethodGet, v.cfg.BaseURL+"/models",
		map[string]string{"x-api-key": v.cfg.APIKey, "anthropic-version": anthropicVersion}, nil, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("list models: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return nil, fmt.Errorf("custom %s: list models HTTP %d", v.cfg.ID, resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("custom %s: bad models response: %w", v.cfg.ID, err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// 请求：OpenAI → Anthropic
// ---------------------------------------------------------------------------

// openAIToAnthropicRequest 把 OpenAI Chat 请求体转为 Anthropic Messages 请求。
func openAIToAnthropicRequest(raw []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("bad openai body: %w", err)
	}
	out := map[string]any{"stream": false}
	if s, ok := req["stream"].(bool); ok {
		out["stream"] = s
	}
	if m, _ := req["model"].(string); m != "" {
		out["model"] = m
	}
	// max_tokens Anthropic 必填：透传请求值，缺省用兜底。
	maxTok := defaultAnthropicMaxTokens
	if n, ok := protocol.NumberAsFloat(req["max_tokens"]); ok && n > 0 {
		maxTok = int(n)
	}
	out["max_tokens"] = maxTok
	for _, k := range []string{"temperature", "top_p", "stop_sequences", "metadata", "thinking"} {
		if val, ok := req[k]; ok {
			out[k] = val
		}
	}
	if s, ok := req["stop"].(string); ok && s != "" {
		out["stop_sequences"] = []string{s}
	} else if arr, ok := req["stop"].([]any); ok && len(arr) > 0 {
		out["stop_sequences"] = arr
	}

	var systemParts []string
	msgs, _ := req["messages"].([]any)
	anthMsgs := make([]map[string]any, 0, len(msgs))
	// 连续 tool 消息并入同一条 user 消息（Anthropic tool_result 语义）。
	var pendingToolResults []any
	flushTools := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		anthMsgs = append(anthMsgs, map[string]any{"role": "user", "content": pendingToolResults})
		pendingToolResults = nil
	}
	for _, rawMsg := range msgs {
		m, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "developer":
			flushTools()
			if s := contentToText(m["content"]); s != "" {
				systemParts = append(systemParts, s)
			}
		case "user":
			flushTools()
			anthMsgs = append(anthMsgs, map[string]any{"role": "user", "content": openAIContentToAnthropic(m["content"])})
		case "assistant":
			flushTools()
			content := openAIContentToAnthropic(m["content"])
			// tool_calls → tool_use 块。
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
					id, _ := tc["id"].(string)
					if id == "" {
						id = "call_" + name
					}
					content = append(content, map[string]any{
						"type": "tool_use", "id": id, "name": name, "input": args,
					})
				}
			}
			if len(content) > 0 {
				anthMsgs = append(anthMsgs, map[string]any{"role": "assistant", "content": content})
			}
		case "tool":
			// OpenAI tool 结果消息 → Anthropic tool_result 块（并入 user 消息）。
			toolID, _ := m["tool_call_id"].(string)
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolID,
				"content":     []map[string]any{{"type": "text", "text": contentToText(m["content"])}},
			})
		}
	}
	flushTools()
	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
	}
	out["messages"] = anthMsgs

	// tools → Anthropic 格式（input_schema）。
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		anthTools := make([]map[string]any, 0, len(tools))
		for _, rawTool := range tools {
			t, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := t["function"].(map[string]any)
			if fn == nil {
				continue
			}
			at := map[string]any{"name": fn["name"]}
			if d, ok := fn["description"].(string); ok {
				at["description"] = d
			}
			if p, ok := fn["parameters"]; ok {
				at["input_schema"] = protocol.CleanJSONSchema(p)
			}
			anthTools = append(anthTools, at)
		}
		if len(anthTools) > 0 {
			out["tools"] = anthTools
		}
	}
	switch tc := req["tool_choice"].(type) {
	case string:
		switch tc {
		case "auto":
			out["tool_choice"] = map[string]any{"type": "auto"}
		case "required":
			out["tool_choice"] = map[string]any{"type": "any"}
		case "none":
			// Anthropic 无 none：不传 tool_choice 即可。
		}
	case map[string]any:
		if fn, ok := tc["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				out["tool_choice"] = map[string]any{"type": "tool", "name": n}
			}
		}
	}
	return json.Marshal(out)
}

// openAIContentToAnthropic OpenAI content（string | parts 数组）→ Anthropic content 块数组。
func openAIContentToAnthropic(content any) []map[string]any {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": c}}
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
				out = append(out, map[string]any{"type": "text", "text": s})
			case "image_url", "input_image":
				var u string
				switch iu := p["image_url"].(type) {
				case string:
					u = iu
				case map[string]any:
					u, _ = iu["url"].(string)
				}
				if media, data, ok := parseDataURL(u); ok {
					out = append(out, map[string]any{
						"type":   "image",
						"source": map[string]any{"type": "base64", "media_type": media, "data": data},
					})
				}
			}
		}
		return out
	}
	return nil
}

// parseDataURL 解析 data:<mime>;base64,<payload>。
func parseDataURL(u string) (media, data string, ok bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", "", false
	}
	rest := u[len("data:"):]
	comma := strings.Index(rest, ",")
	if comma < 0 {
		return "", "", false
	}
	header, payload := rest[:comma], rest[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return "", "", false
	}
	media = strings.TrimSuffix(header, ";base64")
	if media == "" {
		media = "image/png"
	}
	return media, payload, true
}

// contentToText OpenAI content 的纯文本摘要（system/tool 消息用）。
func contentToText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, rawPart := range c {
			if p, ok := rawPart.(map[string]any); ok {
				if s, ok := p["text"].(string); ok {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	}
	return ""
}

// ---------------------------------------------------------------------------
// 响应：Anthropic → OpenAI
// ---------------------------------------------------------------------------

// anthropicToOpenAIResponse 把 Anthropic Messages 响应转为 OpenAI Chat 响应体。
// 解析失败返回 nil（调用方原样透传由上层报错）。
func anthropicToOpenAIResponse(body []byte) []byte {
	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		StopReason string         `json:"stop_reason"`
		Usage      map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var text strings.Builder
	var toolCalls []map[string]any
	for _, blk := range resp.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "tool_use":
			args, _ := json.Marshal(blk.Input)
			toolCalls = append(toolCalls, map[string]any{
				"id":       blk.ID,
				"type":     "function",
				"function": map[string]any{"name": blk.Name, "arguments": string(args)},
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
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": nowUnix(),
		"model":   resp.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": anthropicFinishReason(resp.StopReason),
		}},
		"usage": protocol.AnthropicUsageToChat(resp.Usage),
	}
	out, err := json.Marshal(openai)
	if err != nil {
		return nil
	}
	return out
}

// anthropicFinishReason stop_reason → finish_reason。
func anthropicFinishReason(r string) string {
	switch r {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "":
		return "stop"
	}
	return "stop"
}

// ---------------------------------------------------------------------------
// Chat / ChatStream
// ---------------------------------------------------------------------------

func (p anthropicProto) chat(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Reply, error) {
	anthBody, err := openAIToAnthropicRequest(rawBody)
	if err != nil {
		return nil, fmt.Errorf("custom %s: %w", v.cfg.ID, err)
	}
	resp, addr, err := v.do(ctx, http.MethodPost, v.cfg.BaseURL+"/messages", p.headers(v, false), anthBody, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("chat: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return &contract.Reply{Body: body, Status: resp.StatusCode, NodeAddr: addr}, nil
	}
	converted := anthropicToOpenAIResponse(body)
	if converted == nil {
		v.markErr("chat: bad anthropic response body")
		return &contract.Reply{Body: body, Status: http.StatusBadGateway, NodeAddr: addr}, nil
	}
	v.markOK()
	return &contract.Reply{Body: converted, Status: resp.StatusCode, NodeAddr: addr}, nil
}

func (p anthropicProto) chatStream(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Stream, error) {
	anthBody, err := openAIToAnthropicRequest(rawBody)
	if err != nil {
		return nil, fmt.Errorf("custom %s: %w", v.cfg.ID, err)
	}
	resp, addr, err := v.do(ctx, http.MethodPost, v.cfg.BaseURL+"/messages", p.headers(v, true), anthBody, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := readBody(resp)
		v.markErr(fmt.Sprintf("chat stream: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return &contract.Stream{ReadCloser: nopCloser{bytes.NewReader(body)}, Status: resp.StatusCode, NodeAddr: addr}, nil
	}
	v.markOK()
	conv := newAnthropicStreamConverter(resp.Body)
	return &contract.Stream{ReadCloser: conv, Status: resp.StatusCode, NodeAddr: addr}, nil
}

// ---------------------------------------------------------------------------
// 流：Anthropic SSE → OpenAI Chat SSE
// ---------------------------------------------------------------------------

// anthropicToolBlock 一个 tool_use 块的流内累积状态。
type anthropicToolBlock struct {
	tcIndex int // OpenAI tool_calls 序号（按块出现顺序）
	id      string
	name    string
	args    strings.Builder // input_json_delta 分片拼接
}

// anthropicStreamState 流转换状态（usage 聚合 + tool 参数分片拼接）。
type anthropicStreamState struct {
	cc         chunkCtx
	promptTok  float64
	outputTok  float64
	stopReason string
	toolBlocks map[int]*anthropicToolBlock // Anthropic block index → 块
	toolOrder  []int                       // 块出现顺序（OpenAI tool index 即位次）
}

// newAnthropicStreamConverter 构造 Anthropic SSE → OpenAI chunk 转换器。
func newAnthropicStreamConverter(rc io.ReadCloser) *sseConverter {
	st := &anthropicStreamState{}
	transform := func(ev *sseEvent, w *bytes.Buffer) { st.transform(ev, w) }
	finish := func(w *bytes.Buffer) { st.finish(w) }
	return newSSEConverter(rc, transform, finish)
}

func (st *anthropicStreamState) transform(ev *sseEvent, w *bytes.Buffer) {
	switch ev.event {
	case "message_start":
		var d struct {
			Message struct {
				ID    string         `json:"id"`
				Model string         `json:"model"`
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(ev.data, &d) == nil {
			st.cc = chunkCtx{id: d.Message.ID, model: d.Message.Model}
			if n, ok := protocol.NumberAsFloat(d.Message.Usage["input_tokens"]); ok {
				st.promptTok = n
			}
		}
	case "content_block_start":
		var d struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal(ev.data, &d) != nil || d.ContentBlock.Type != "tool_use" {
			return
		}
		if st.toolBlocks == nil {
			st.toolBlocks = map[int]*anthropicToolBlock{}
		}
		blk := &anthropicToolBlock{
			tcIndex: len(st.toolOrder),
			id:      d.ContentBlock.ID,
			name:    d.ContentBlock.Name,
		}
		st.toolBlocks[d.Index] = blk
		st.toolOrder = append(st.toolOrder, d.Index)
		writeChunk(w, &st.cc, map[string]any{
			"tool_calls": []map[string]any{{
				"index": blk.tcIndex, "id": blk.id, "type": "function",
				"function": map[string]any{"name": blk.name, "arguments": ""},
			}},
		}, nil)
	case "content_block_delta":
		var d struct {
			Index int `json:"index"`
			Delta struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Think string `json:"thinking"`
				Part  string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal(ev.data, &d) != nil {
			return
		}
		switch d.Delta.Type {
		case "text_delta":
			if d.Delta.Text != "" {
				writeChunk(w, &st.cc, map[string]any{"content": d.Delta.Text}, nil)
			}
		case "thinking_delta":
			if d.Delta.Think != "" {
				writeChunk(w, &st.cc, map[string]any{"reasoning_content": d.Delta.Think}, nil)
			}
		case "input_json_delta":
			if blk, ok := st.toolBlocks[d.Index]; ok {
				blk.args.WriteString(d.Delta.Part)
			}
		}
	case "message_delta":
		var d struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal(ev.data, &d) == nil {
			if d.Delta.StopReason != "" {
				st.stopReason = d.Delta.StopReason
			}
			if n, ok := protocol.NumberAsFloat(d.Usage["output_tokens"]); ok {
				st.outputTok = n
			}
		}
	}
}

// finish 流结束：补 tool 参数增量、finish chunk、usage chunk 与 [DONE]。
func (st *anthropicStreamState) finish(w *bytes.Buffer) {
	for _, blockIdx := range st.toolOrder {
		blk := st.toolBlocks[blockIdx]
		if blk.args.Len() == 0 {
			continue
		}
		writeChunk(w, &st.cc, map[string]any{
			"tool_calls": []map[string]any{{
				"index": blk.tcIndex, "type": "function",
				"function": map[string]any{"arguments": blk.args.String()},
			}},
		}, nil)
	}
	finish := anthropicFinishReason(st.stopReason)
	writeChunk(w, &st.cc, map[string]any{}, &finish)
	writeUsageDone(w, &st.cc, st.promptTok, st.outputTok)
}
