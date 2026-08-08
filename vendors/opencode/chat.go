// 聊天实现：迁移自 package main 的 upstream.go / convert.go（Anthropic 兼容部分）。
// 适配点：会话由 Vendor 实例持有；HTTP 客户端经 contract.Transport 注入；
// 认证路由（public/auto/zen/go）与最大重试次数从 contract.Message.Options 读取。
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// Options 键（补充）：最大上游重试次数。
const KeyMaxRetries = "_oc_max_retries"

const (
	maxUpstreamRetries = 3
	max401Retries      = 3
)

// authMode 与 authT 是 opencode 上游的认证路由语义（public / auto / zen / go）。
type authMode int

const (
	authPublic authMode = iota
	authAuto
	authZen
	authGo
)

type authT struct {
	token string
	mode  authMode
}

func (a authT) tier() contract.Tier {
	if a.mode == authPublic {
		return contract.TierFree
	}
	return contract.TierPaid
}

func (a authT) authHeader() string {
	if a.mode == authPublic {
		return "Bearer public"
	}
	return "Bearer " + a.token
}

// parseAuth 从 Message.Options 还原上游认证路由。
func parseAuth(msg *contract.Message) authT {
	mode := authAuto
	switch s, _ := msg.Options[KeyAuthMode].(string); s {
	case "public":
		mode = authPublic
	case "auto":
		mode = authAuto
	case "zen":
		mode = authZen
	case "go":
		mode = authGo
	}
	token, _ := msg.Options[KeyAuthToken].(string)
	if mode == authAuto && token == "" {
		mode = authPublic
	}
	return authT{token: token, mode: mode}
}

func maxRetriesOf(msg *contract.Message) int {
	if n, ok := msg.Options[KeyMaxRetries].(int); ok && n > 0 {
		return n
	}
	return maxUpstreamRetries
}

// ---------------------------------------------------------------- 模型集合

// modelIDsOnSurface 返回指定 surface 的模型 ID 列表（来自厂商目录缓存）。
func (v *Vendor) modelIDsOnSurface(surface string) []string {
	v.modelMu.RLock()
	defer v.modelMu.RUnlock()
	var out []string
	for _, m := range v.cacheAll {
		if m.Meta != nil && m.Meta["surface"] == surface {
			out = append(out, m.ID)
		}
	}
	return out
}

func (v *Vendor) hasModelOnSurface(modelID, surface string) bool {
	v.modelMu.RLock()
	defer v.modelMu.RUnlock()
	for _, m := range v.cacheAll {
		if m.ID == modelID && m.Meta != nil && m.Meta["surface"] == surface {
			return true
		}
	}
	return false
}

func (v *Vendor) goOnlyModel(modelID string) bool {
	return v.hasModelOnSurface(modelID, surfaceGo) && !v.hasModelOnSurface(modelID, surfaceZen)
}

func (a authT) useGoEndpoint(v *Vendor, modelID string) bool {
	switch a.mode {
	case authGo:
		return v.hasModelOnSurface(modelID, surfaceGo)
	case authAuto:
		return v.goOnlyModel(modelID)
	default:
		return false
	}
}

// ---------------------------------------------------------------- 请求构造

func (v *Vendor) buildRequest(modelID string, bodyMap map[string]any, a authT) (*http.Request, error) {
	bodyMap["model"] = modelID
	delete(bodyMap, "reasoning_effort")
	tryBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	upstreamURL := "https://opencode.ai/zen/v1/chat/completions"
	if a.useGoEndpoint(v, modelID) {
		upstreamURL = "https://opencode.ai/zen/go/v1/chat/completions"
	}
	req, err := http.NewRequest("POST", upstreamURL, bytes.NewReader(tryBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("User-Agent", fmt.Sprintf("opencode/%s", v.ocClientVer))
	req.Header.Set("x-opencode-client", "cli")
	req.Header.Set("x-opencode-project", v.ocProjectID)
	req.Header.Set("x-opencode-session", v.ocSessionID)
	req.Header.Set("x-opencode-request", "req_"+randomString(24))
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func shouldRetryUpstreamStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return status >= 500 && status < 600
}

// ---------------------------------------------------------------- 聊天

// Chat 实现 contract.Vendor（非流式）。
func (v *Vendor) Chat(ctx context.Context, msg *contract.Message) (*contract.Reply, error) {
	v.sessionID()

	raw, ok := msg.Options[KeyRawBody].([]byte)
	if !ok {
		return nil, fmt.Errorf("opencode: missing %s in message options", KeyRawBody)
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return &contract.Reply{Status: http.StatusBadRequest}, nil
	}
	a := parseAuth(msg)
	modelID := msg.Model
	maxRetries := maxRetriesOf(msg)

	var lastErr error
	var retryCount int
	var retry401Count int
	var lastBody []byte
	var lastStatus int
	var lastHeader http.Header
	var lastProxyAddr string

	for retryCount <= maxRetries {
		up, err := v.buildRequest(modelID, bodyMap, a)
		if err != nil {
			lastErr = err
			break
		}
		tr := v.transport()
		client, proxyAddr := tr.Client(a.tier(), false)
		resp, err := client.Do(up)
		if err != nil {
			tr.Mark(proxyAddr, 0, err)
			lastErr = err
			retryCount++
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			tr.Mark(proxyAddr, resp.StatusCode, nil)
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if isAnthropicFormat(b) {
				b = convertAnthropicToOpenAI(b, modelID)
			}
			return &contract.Reply{Body: b, Status: resp.StatusCode, Headers: resp.Header, NodeAddr: proxyAddr}, nil
		}
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		tr.Mark(proxyAddr, resp.StatusCode, nil)
		slog.Error("opencode upstream error", "model", modelID, "status", resp.StatusCode, "body", string(errBody))
		lastBody = errBody
		lastStatus = resp.StatusCode
		lastHeader = resp.Header
		lastProxyAddr = proxyAddr
		lastErr = fmt.Errorf("upstream error")
		if shouldRetryUpstreamStatus(resp.StatusCode) {
			client.CloseIdleConnections()
			if resp.StatusCode == http.StatusUnauthorized {
				retry401Count++
				if retry401Count >= max401Retries {
					break
				}
			} else {
				retryCount++
				if retryCount >= maxRetries {
					break
				}
			}
			continue
		}
		break
	}
	return &contract.Reply{Body: lastBody, Status: lastStatus, Headers: lastHeader, NodeAddr: lastProxyAddr}, lastErr
}

// ChatStream 实现 contract.Vendor（流式 SSE）。
func (v *Vendor) ChatStream(ctx context.Context, msg *contract.Message) (*contract.Stream, error) {
	v.sessionID()

	raw, ok := msg.Options[KeyRawBody].([]byte)
	if !ok {
		return nil, fmt.Errorf("opencode: missing %s in message options", KeyRawBody)
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return &contract.Stream{ReadCloser: io.NopCloser(bytes.NewReader(nil)), NodeAddr: ""}, nil
	}
	a := parseAuth(msg)
	modelID := msg.Model
	maxRetries := maxRetriesOf(msg)

	var lastBody []byte
	var lastStatus int
	var lastProxyAddr string
	var retryCount int
	var retry401Count int

	for retryCount <= maxRetries {
		up, err := v.buildRequest(modelID, bodyMap, a)
		if err != nil {
			break
		}
		tr := v.transport()
		client, proxyAddr := tr.Client(a.tier(), true)
		resp, err := client.Do(up)
		if err != nil {
			tr.Mark(proxyAddr, 0, err)
			retryCount++
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			tr.Mark(proxyAddr, resp.StatusCode, nil)
			return &contract.Stream{ReadCloser: resp.Body, NodeAddr: proxyAddr}, nil
		}
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		tr.Mark(proxyAddr, resp.StatusCode, nil)
		slog.Error("opencode upstream error", "model", modelID, "status", resp.StatusCode, "body", string(errBody))
		lastBody = errBody
		lastStatus = resp.StatusCode
		lastProxyAddr = proxyAddr
		if shouldRetryUpstreamStatus(resp.StatusCode) {
			client.CloseIdleConnections()
			if resp.StatusCode == http.StatusUnauthorized {
				retry401Count++
				if retry401Count >= max401Retries {
					break
				}
			} else {
				retryCount++
				if retryCount >= maxRetries {
					break
				}
			}
			continue
		}
		return &contract.Stream{ReadCloser: io.NopCloser(bytes.NewReader(lastBody)), NodeAddr: lastProxyAddr}, nil
	}
	if lastStatus != 0 {
		return &contract.Stream{ReadCloser: io.NopCloser(bytes.NewReader(lastBody)), NodeAddr: lastProxyAddr}, nil
	}
	return nil, fmt.Errorf("all models failed")
}

// ---------------------------------------------------------------- Anthropic 兼容

func isAnthropicFormat(body []byte) bool {
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		if typ, _ := obj["type"].(string); typ == "message" {
			return true
		}
	}
	lines := bytes.Split(body, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		typ, _ := event["type"].(string)
		switch typ {
		case "message_start", "content_block_start", "content_block_delta",
			"content_block_stop", "message_delta", "message_stop", "ping":
			return true
		}
		return false
	}
	return false
}

func parseAnthropicSSE(body []byte) (map[string]any, string, []map[string]any) {
	lines := bytes.Split(body, []byte("\n"))
	var anthropicMsg map[string]any
	var textBuilder, currentToolInputBuilder strings.Builder
	var currentToolUse map[string]any
	var toolUseBlocks []map[string]any
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		typ, _ := event["type"].(string)
		switch typ {
		case "message_start":
			if m, ok := event["message"].(map[string]any); ok {
				anthropicMsg = m
			}
		case "content_block_start":
			if cb, ok := event["content_block"].(map[string]any); ok {
				if cbType, _ := cb["type"].(string); cbType == "tool_use" {
					currentToolUse = cb
					currentToolInputBuilder.Reset()
				}
			}
		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]any); ok {
				if t, ok := delta["text"].(string); ok {
					textBuilder.WriteString(t)
				}
				if dt, _ := delta["type"].(string); dt == "input_json_delta" {
					if partial, ok := delta["partial_json"].(string); ok {
						currentToolInputBuilder.WriteString(partial)
					}
				}
			}
		case "content_block_stop":
			if currentToolUse != nil {
				inputStr := currentToolInputBuilder.String()
				var input any = inputStr
				var parsed any
				if json.Unmarshal([]byte(inputStr), &parsed) == nil {
					input = parsed
				}
				currentToolUse["input"] = input
				toolUseBlocks = append(toolUseBlocks, currentToolUse)
				currentToolUse = nil
			}
		case "message_delta":
			if delta, ok := event["delta"].(map[string]any); ok {
				if anthropicMsg == nil {
					anthropicMsg = map[string]any{}
				}
				if stop, ok := delta["stop_reason"].(string); ok {
					anthropicMsg["stop_reason"] = stop
				}
				if usage, ok := delta["usage"].(map[string]any); ok {
					anthropicMsg["usage"] = usage
				}
			}
		case "message_stop":
		case "error":
			return nil, "", nil
		}
	}
	return anthropicMsg, textBuilder.String(), toolUseBlocks
}

func buildOpenAIResponse(anthropicMsg map[string]any, text string, toolUseBlocks []map[string]any, modelID string) []byte {
	if anthropicMsg == nil {
		return nil
	}
	now := time.Now().Unix()
	role, _ := anthropicMsg["role"].(string)
	if role == "" {
		role = "assistant"
	}
	finishReason, _ := anthropicMsg["stop_reason"].(string)
	finishReason = normalizeFinishReason(finishReason)
	choice := map[string]any{
		"index":         0,
		"message":       map[string]any{"role": role, "content": text},
		"finish_reason": finishReason,
	}
	if len(toolUseBlocks) > 0 {
		var toolCalls []map[string]any
		for _, tb := range toolUseBlocks {
			toolInput := tb["input"]
			argsJSON, _ := json.Marshal(toolInput)
			toolCalls = append(toolCalls, map[string]any{
				"id":   tb["id"],
				"type": "function",
				"function": map[string]any{
					"name":      tb["name"],
					"arguments": string(argsJSON),
				},
			})
		}
		choice["message"].(map[string]any)["tool_calls"] = toolCalls
		if text == "" {
			choice["message"].(map[string]any)["content"] = nil
		}
	}
	resp := map[string]any{
		"id":      anthropicMsg["id"],
		"object":  "chat.completion",
		"created": now,
		"model":   modelID,
		"choices": []map[string]any{choice},
	}
	if usage, ok := anthropicMsg["usage"].(map[string]any); ok {
		resp["usage"] = anthropicUsageToChat(usage)
	}
	result, _ := json.Marshal(resp)
	return result
}

func convertAnthropicMessageToOpenAI(msg map[string]any, modelID string) []byte {
	if msg["model"] == nil {
		msg["model"] = modelID
	}
	var textBuilder strings.Builder
	var toolUses []map[string]any
	if content, ok := msg["content"].([]any); ok {
		for _, c := range content {
			if block, ok := c.(map[string]any); ok {
				switch block["type"] {
				case "text":
					if t, ok := block["text"].(string); ok {
						textBuilder.WriteString(t)
					}
				case "tool_use":
					toolUses = append(toolUses, block)
				}
			}
		}
	}
	return buildOpenAIResponse(msg, textBuilder.String(), toolUses, modelID)
}

func convertAnthropicToOpenAI(body []byte, modelID string) []byte {
	var singleMsg map[string]any
	if json.Unmarshal(body, &singleMsg) == nil {
		if typ, _ := singleMsg["type"].(string); typ == "message" {
			return convertAnthropicMessageToOpenAI(singleMsg, modelID)
		}
	}
	msg, text, toolUses := parseAnthropicSSE(body)
	if msg == nil {
		return body
	}
	if msg["model"] == nil {
		msg["model"] = modelID
	}
	return buildOpenAIResponse(msg, text, toolUses, modelID)
}

func normalizeFinishReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "stop":
		return "stop"
	case "max_tokens", "length":
		return "length"
	case "tool_use", "tool_calls", "function_call":
		return "tool_calls"
	case "refusal", "content_filter":
		return "content_filter"
	default:
		return reason
	}
}

func anthropicUsageToChat(usage map[string]any) map[string]any {
	if usage == nil {
		return nil
	}
	out := make(map[string]any, len(usage)+3)
	for k, v := range usage {
		out[k] = v
	}
	if v, ok := usage["input_tokens"]; ok {
		out["prompt_tokens"] = v
	}
	if v, ok := usage["output_tokens"]; ok {
		out["completion_tokens"] = v
	}
	if p, pok := numberAsFloat(out["prompt_tokens"]); pok {
		if c, cok := numberAsFloat(out["completion_tokens"]); cok {
			out["total_tokens"] = p + c
		}
	}
	delete(out, "input_tokens")
	delete(out, "output_tokens")
	return out
}

func numberAsFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
