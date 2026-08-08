// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

func claudeToOpenAIMessages(claudeMsgs []ClaudeMessage, system any) []Message {
	var messages []Message
	if sysText := extractClaudeSystemText(system); sysText != "" {
		messages = append(messages, Message{Role: "system", Content: sysText})
	}
	for _, msg := range claudeMsgs {
		switch content := msg.Content.(type) {
		case string:
			messages = append(messages, Message{Role: msg.Role, Content: content})
		case []any:
			var orderedContent []any
			var reasoningParts []string
			var toolCalls []ToolCall
			var toolResults []Message
			for _, item := range content {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := block["type"].(string)
				switch blockType {
				case "text":
					if text, ok := block["text"].(string); ok && text != "" {
						orderedContent = append(orderedContent, map[string]any{"type": "text", "text": text})
					}
				case "image":
					source, _ := block["source"].(map[string]any)
					if source != nil {
						srcType, _ := source["type"].(string)
						mediaType, _ := source["media_type"].(string)
						data, _ := source["data"].(string)
						url, _ := source["url"].(string)
						if srcType == "url" && url != "" {
							orderedContent = append(orderedContent, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
						}
						if srcType == "base64" && data != "" {
							if mediaType == "" {
								mediaType = "image/png"
							}
							orderedContent = append(orderedContent, map[string]any{
								"type": "image_url",
								"image_url": map[string]string{
									"url": "data:" + mediaType + ";base64," + data,
								},
							})
						}
					}
				case "thinking":
					if thinking, ok := block["thinking"].(string); ok && thinking != "" {
						reasoningParts = append(reasoningParts, thinking)
					}
				case "tool_use":
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					var args string
					switch input := block["input"].(type) {
					case string:
						args = input
					default:
						if input != nil {
							b, _ := json.Marshal(input)
							args = string(b)
						}
					}
					if args == "" {
						args = "{}"
					}
					toolCalls = append(toolCalls, ToolCall{
						ID:   id,
						Type: "function",
						Function: FunctionCall{
							Name:      name,
							Arguments: args,
						},
					})
				case "tool_result":
					toolUseID, _ := block["tool_use_id"].(string)
					var resultText string
					switch c := block["content"].(type) {
					case string:
						resultText = c
					case []any:
						var parts []string
						for _, p := range c {
							if pb, ok := p.(map[string]any); ok && pb["type"] == "text" {
								if t, ok := pb["text"].(string); ok {
									parts = append(parts, t)
								}
							}
						}
						resultText = strings.Join(parts, "\n")
					default:
						if c != nil {
							b, _ := json.Marshal(c)
							resultText = string(b)
						}
					}
					if isError, _ := block["is_error"].(bool); isError {
						resultText = "Error: " + resultText
					}
					toolResults = append(toolResults, Message{
						Role:       "tool",
						ToolCallID: toolUseID,
						Content:    resultText,
					})
				}
			}
			om := Message{Role: msg.Role}
			if len(orderedContent) > 0 {
				om.Content = orderedContent
			} else if len(toolCalls) == 0 {
				om.Content = ""
			}
			if len(reasoningParts) > 0 {
				rc := strings.Join(reasoningParts, "\n")
				om.ReasoningContent = &rc
			}
			if len(toolCalls) > 0 {
				om.ToolCalls = toolCalls
			}
			// Anthropic requires tool_result blocks to precede ordinary user
			// content. Preserve that order when translating them to Chat
			// Completions' separate tool messages.
			if msg.Role == "user" {
				messages = append(messages, toolResults...)
			}
			if len(orderedContent) > 0 || len(reasoningParts) > 0 || len(toolCalls) > 0 || len(toolResults) == 0 {
				messages = append(messages, om)
			}
			if msg.Role != "user" {
				messages = append(messages, toolResults...)
			}
		default:
			b, _ := json.Marshal(content)
			messages = append(messages, Message{Role: msg.Role, Content: string(b)})
		}
	}
	return messages
}

func claudeToOpenAITools(claudeTools []ClaudeTool) []Tool {
	tools := make([]Tool, 0, len(claudeTools))
	for _, ct := range claudeTools {
		params := ct.InputSchema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		params = cleanJsonSchema(params)
		paramsMap, ok := params.(map[string]any)
		if !ok {
			paramsMap = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        ct.Name,
				Description: ct.Description,
				Parameters:  paramsMap,
			},
		})
	}
	return tools
}

func openAIToClaudeResponse(chatBody []byte, model string, wantReasoning bool) []byte {
	var chat struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Message struct {
				Content          string     `json:"content"`
				ReasoningContent string     `json:"reasoning_content"`
				ToolCalls        []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		slog.Warn("openAIToClaudeResponse unmarshal failed", "error", err)
	}

	content := []ClaudeContent{}
	stopReason := "end_turn"

	if len(chat.Choices) > 0 {
		msg := chat.Choices[0].Message
		fr := chat.Choices[0].FinishReason
		if wantReasoning && msg.ReasoningContent != "" {
			content = append(content, ClaudeContent{
				Type:     "thinking",
				Thinking: msg.ReasoningContent,
			})
		}
		if msg.Content != "" {
			content = append(content, ClaudeContent{
				Type: "text",
				Text: msg.Content,
			})
		}
		for _, tc := range msg.ToolCalls {
			var input any
			json.Unmarshal([]byte(tc.Function.Arguments), &input)
			if input == nil {
				input = map[string]any{}
			}
			content = append(content, ClaudeContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
		switch fr {
		case "stop":
			stopReason = "end_turn"
		case "length":
			stopReason = "max_tokens"
		case "tool_calls", "function_call":
			stopReason = "tool_use"
		case "content_filter":
			stopReason = "refusal"
		}
	}

	if len(content) == 0 {
		content = append(content, ClaudeContent{Type: "text", Text: ""})
	}

	resp := ClaudeResponse{
		ID:         fmt.Sprintf("msg_%s", randomString(24)),
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: stopReason,
	}
	if chat.Usage != nil {
		resp.Usage = buildClaudeMessageUsage(chat.Usage)
	}
	result, _ := json.Marshal(resp)
	return result
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func usageIntField(fields map[string]any, key string) (int, bool) {
	if fields == nil {
		return 0, false
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0, false
	}
	return int(toFloat64(value)), true
}

func usageMapField(fields map[string]any, key string) (map[string]any, bool) {
	if fields == nil {
		return nil, false
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return nil, false
	}
	mapped, ok := value.(map[string]any)
	return mapped, ok
}

func buildClaudeUsageCore(upstreamUsage map[string]any) ClaudeUsage {
	if len(upstreamUsage) == 0 {
		return nil
	}

	usage := ClaudeUsage{}
	if value, ok := usageIntField(upstreamUsage, "prompt_tokens"); ok {
		usage["input_tokens"] = value
	}
	if value, ok := usageIntField(upstreamUsage, "input_tokens"); ok {
		if _, exists := usage["input_tokens"]; !exists {
			usage["input_tokens"] = value
		}
	}
	if value, ok := usageIntField(upstreamUsage, "completion_tokens"); ok {
		usage["output_tokens"] = value
	}
	if value, ok := usageIntField(upstreamUsage, "output_tokens"); ok {
		if _, exists := usage["output_tokens"]; !exists {
			usage["output_tokens"] = value
		}
	}
	if value, ok := usageIntField(upstreamUsage, "cache_creation_input_tokens"); ok {
		usage["cache_creation_input_tokens"] = value
	}
	if value, ok := usageIntField(upstreamUsage, "cache_read_input_tokens"); ok {
		usage["cache_read_input_tokens"] = value
	} else if promptDetails, ok := usageMapField(upstreamUsage, "prompt_tokens_details"); ok {
		if value, ok := usageIntField(promptDetails, "cached_tokens"); ok {
			usage["cache_read_input_tokens"] = value
		}
	}
	if outputDetails, ok := usageMapField(upstreamUsage, "output_tokens_details"); ok {
		usage["output_tokens_details"] = outputDetails
	} else if outputDetails, ok := usageMapField(upstreamUsage, "completion_tokens_details"); ok {
		usage["output_tokens_details"] = outputDetails
	}
	if serverToolUse, ok := usageMapField(upstreamUsage, "server_tool_use"); ok {
		usage["server_tool_use"] = serverToolUse
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func buildClaudeMessageUsage(upstreamUsage map[string]any) ClaudeUsage {
	usage := buildClaudeUsageCore(upstreamUsage)
	if usage == nil {
		usage = ClaudeUsage{}
	}
	if cacheCreation, ok := usageMapField(upstreamUsage, "cache_creation"); ok {
		usage["cache_creation"] = cacheCreation
	}
	if serviceTier, ok := upstreamUsage["service_tier"].(string); ok && serviceTier != "" {
		usage["service_tier"] = serviceTier
	}
	if inferenceGeo, ok := upstreamUsage["inference_geo"].(string); ok && inferenceGeo != "" {
		usage["inference_geo"] = inferenceGeo
	}
	if _, exists := usage["input_tokens"]; !exists {
		usage["input_tokens"] = 0
	}
	if _, exists := usage["output_tokens"]; !exists {
		usage["output_tokens"] = 0
	}
	return usage
}

func buildClaudeDeltaUsage(upstreamUsage map[string]any) ClaudeUsage {
	usage := buildClaudeUsageCore(upstreamUsage)
	if usage == nil {
		usage = ClaudeUsage{}
	}
	if _, exists := usage["output_tokens"]; !exists {
		usage["output_tokens"] = 0
	}
	return usage
}

func claudeMessagesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	auth := extractUpstreamAuth(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	cnt := requestCount.Add(1)
	slog.Debug("claude messages request body", "count", cnt, "body", string(body))

	var claudeReq ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	claudeReq.Model = resolveModel(claudeReq.Model)
	if claudeReq.Model == "" {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"model is required"}}`, http.StatusBadRequest)
		return
	}

	// 多模态路由

	chatReq := convertClaudeRequest(claudeReq)
	chatReq.Messages = fixToolCallGaps(chatReq.Messages)
	if claudeReq.Stream {
		if chatReq.ExtraBody == nil {
			chatReq.ExtraBody = map[string]any{}
		}
		chatReq.ExtraBody["stream_options"] = map[string]any{"include_usage": true}
	}

	wantReasoning := !getForceDisableThinking()
	if claudeReq.Thinking != nil {
		if isThinkingDisabled(claudeReq.Thinking) {
			wantReasoning = false
		}
	}
	keepReasoning := wantReasoning
	chatReq.Messages = ensureReasoningContent(chatReq.Messages, keepReasoning)

	upstreamBody := buildUpstreamBody(&chatReq)

	if claudeReq.Stream {
		upResp, status, _, proxyAddr, err := callOpenCodeAPIStream(upstreamBody, chatReq.Model, auth)
		if err != nil || status < 200 || status >= 300 {
			errResp := map[string]any{
				"type":  "error",
				"error": map[string]string{"type": "api_error", "message": "upstream error"},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(errResp)
			return
		}
		defer upResp.Close()
		claudeStreamHandler(w, upResp, claudeReq.Model, keepReasoning, proxyAddr)
		return
	}

	respBody, status, _, proxyAddr, err := callOpenCodeAPI(upstreamBody, chatReq.Model, auth)
	if err != nil || status < 200 || status >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if len(respBody) > 0 {
			w.Write(respBody)
		} else {
			json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": "upstream error"}})
		}
		return
	}

	claudeRespBody := openAIToClaudeResponse(respBody, claudeReq.Model, wantReasoning)

	// Record token usage
	var usageResp map[string]any
	if json.Unmarshal(respBody, &usageResp) == nil {
		if u, ok := usageResp["usage"].(map[string]any); ok {
			pt, _ := u["prompt_tokens"].(float64)
			ct, _ := u["completion_tokens"].(float64)
			tt, _ := u["total_tokens"].(float64)
			if tt > 0 {
				recordTokenUsage(claudeReq.Model, int64(pt), int64(ct), int64(tt), proxyAddr)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	slog.Debug("claude response body", "body", string(claudeRespBody))
	w.Write(claudeRespBody)
}

func claudeStreamHandler(w http.ResponseWriter, respBody io.ReadCloser, model string, keepReasoning bool, proxyAddr string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(respBody)

	msgID := fmt.Sprintf("msg_%s", randomString(24))
	blockIndex := 0
	thinkingBlockOpen := false
	textBlockOpen := false
	toolCallAccumulator := map[int]map[string]string{}
	toolBlockIndices := map[int]int{}
	toolCallOrder := []int{}
	messageStartSent := false
	fullUsage := map[string]any{}
	defer func() {
		if len(fullUsage) > 0 {
			pt, _ := fullUsage["prompt_tokens"].(float64)
			ct, _ := fullUsage["completion_tokens"].(float64)
			tt, _ := fullUsage["total_tokens"].(float64)
			if tt > 0 {
				recordTokenUsage(model, int64(pt), int64(ct), int64(tt), proxyAddr)
			}
		}
	}()

	emitClaudeEvent := func(event string, data any) {
		jsonData, err := json.Marshal(data)
		if err != nil {
			slog.Error("marshal SSE event failed", "error", err)
			return
		}
		w.Write([]byte("event: " + event + "\n"))
		w.Write([]byte("data: " + string(jsonData) + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}

	closeThinkingBlock := func() {
		if !thinkingBlockOpen {
			return
		}
		emitClaudeEvent("content_block_stop", map[string]any{
			"type":          "content_block_stop",
			"index":         blockIndex - 1,
			"content_block": map[string]any{"type": "thinking"},
		})
		thinkingBlockOpen = false
	}

	closeTextBlock := func() {
		if !textBlockOpen {
			return
		}
		emitClaudeEvent("content_block_stop", map[string]any{
			"type":          "content_block_stop",
			"index":         blockIndex - 1,
			"content_block": map[string]any{"type": "text"},
		})
		textBlockOpen = false
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Error("stream read error", "error", err)
			break
		}
		if strings.HasPrefix(line, "data: ") {
			slog.Debug("upstream raw chunk", "data", strings.TrimSpace(line[6:]))
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "data: [DONE]" || trimmed == "[DONE]" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(line[6:]), &chunk); err != nil {
			continue
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			fullUsage = usage
		}

		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			continue
		}

		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		finishReason, _ := choice["finish_reason"].(string)

		if !messageStartSent {
			messageStartSent = true
			emitClaudeEvent("message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":          msgID,
					"type":        "message",
					"role":        "assistant",
					"content":     []any{},
					"model":       model,
					"stop_reason": nil,
					"usage":       buildClaudeMessageUsage(fullUsage),
				},
			})
			emitClaudeEvent("ping", map[string]any{"type": "ping"})
		}

		if rc, ok := delta["reasoning_content"]; ok && keepReasoning {
			rcStr, _ := rc.(string)
			if rcStr != "" {
				closeTextBlock()
				if !thinkingBlockOpen {
					emitClaudeEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]any{
							"type":     "thinking",
							"thinking": "",
						},
					})
					thinkingBlockOpen = true
					blockIndex++
				}
				emitClaudeEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": blockIndex - 1,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": rcStr,
					},
				})
			}
		}

		if c, ok := delta["content"]; ok && c != nil {
			contentStr, _ := c.(string)
			if contentStr != "" {
				closeThinkingBlock()
				if !textBlockOpen {
					emitClaudeEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]any{
							"type": "text",
							"text": "",
						},
					})
					textBlockOpen = true
					blockIndex++
				}
				emitClaudeEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": blockIndex - 1,
					"delta": map[string]any{
						"type": "text_delta",
						"text": contentStr,
					},
				})
			}
		}

		if rawToolCalls, ok := delta["tool_calls"].([]any); ok {
			for _, rawTC := range rawToolCalls {
				tc, ok := rawTC.(map[string]any)
				if !ok {
					continue
				}
				idxFloat, _ := tc["index"].(float64)
				upstreamIndex := int(idxFloat)

				closeThinkingBlock()
				closeTextBlock()

				if _, exists := toolCallAccumulator[upstreamIndex]; !exists {
					callID, _ := tc["id"].(string)
					if callID == "" {
						callID = "toolu_" + randomString(12)
					}
					fn, _ := tc["function"].(map[string]any)
					name, _ := fn["name"].(string)
					toolCallAccumulator[upstreamIndex] = map[string]string{
						"id":   callID,
						"name": name,
						"args": "",
					}
					toolCallOrder = append(toolCallOrder, upstreamIndex)
					toolBlockIndices[upstreamIndex] = blockIndex
					emitClaudeEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    callID,
							"name":  name,
							"input": map[string]any{},
						},
					})
					blockIndex++
				}

				fn, _ := tc["function"].(map[string]any)
				if argDelta, ok := fn["arguments"].(string); ok && argDelta != "" {
					toolCallAccumulator[upstreamIndex]["args"] += argDelta
					emitClaudeEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": toolBlockIndices[upstreamIndex],
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": argDelta,
						},
					})
				}
			}
		}

		if finishReason == "stop" || finishReason == "length" || finishReason == "tool_calls" || finishReason == "function_call" || finishReason == "content_filter" {
			closeThinkingBlock()
			closeTextBlock()

			for _, idx := range toolCallOrder {
				acc := toolCallAccumulator[idx]
				emitClaudeEvent("content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": toolBlockIndices[idx],
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    acc["id"],
						"name":  acc["name"],
						"input": map[string]any{},
					},
				})
			}

			stopReason := "end_turn"
			switch finishReason {
			case "length":
				stopReason = "max_tokens"
			case "tool_calls", "function_call":
				stopReason = "tool_use"
			case "content_filter":
				stopReason = "refusal"
			}

			emitClaudeEvent("message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": stopReason,
				},
				"usage": buildClaudeDeltaUsage(fullUsage),
			})
			emitClaudeEvent("message_stop", map[string]any{
				"type": "message_stop",
			})
			return
		}
	}

	closeThinkingBlock()
	closeTextBlock()
	emitClaudeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn"},
		"usage": buildClaudeDeltaUsage(nil),
	})
	emitClaudeEvent("message_stop", map[string]any{"type": "message_stop"})
}

func indexOfInt(slice []int, val int) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return 0
}

// ======================== Responses API ========================
