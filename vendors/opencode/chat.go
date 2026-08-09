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

// parseAuth 从 Message.Extra 还原上游认证路由。
func parseAuth(msg *contract.Message) authT {
	mode := authAuto
	switch s, _ := msg.Extra[KeyAuthMode].(string); s {
	case "public":
		mode = authPublic
	case "auto":
		mode = authAuto
	case "zen":
		mode = authZen
	case "go":
		mode = authGo
	}
	token, _ := msg.Extra[KeyAuthToken].(string)
	if mode == authAuto && token == "" {
		mode = authPublic
	}
	return authT{token: token, mode: mode}
}

func maxRetriesOf(msg *contract.Message) int {
	if n, ok := msg.Extra[KeyMaxRetries].(int); ok && n > 0 {
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

// isRetryable 判定某状态码是否应在本厂商内重试。
// 以 ErrSemantics().Retryable 为唯一状态码来源（契约驱动），另附通用 5xx 兜底
// （历史行为：任意 5xx 一律可重试）。401 走独立计数上限（见 call）。
func (v *Vendor) isRetryable(status int) bool {
	for _, s := range v.ErrSemantics().Retryable {
		if s == status {
			return true
		}
	}
	return status >= 500 && status < 600
}

// ---------------------------------------------------------------- 聊天

// callResult 是一次上游调用的内部结果：非流式成功填 body，流式成功填 stream。
type callResult struct {
	body     []byte        // 非流式：完整响应体（成功或最后一次错误体）
	stream   io.ReadCloser // 流式：SSE 响应流
	status   int           // 最后一次 HTTP 状态（0 = 未获得响应）
	headers  http.Header   // 最后一次响应头（仅非流式路径保留）
	nodeAddr string        // 实际出口节点地址（直连为空）
}

// call 是 Chat / ChatStream 的公共实现：同一套会话/认证/重试/401/端点语义。
// 行为与历史逐项对齐（含 401 独立重试上限、可重试时 CloseIdleConnections、
// 非流式错误带体返回、流式错误体包装为流返回）。
func (v *Vendor) call(ctx context.Context, msg *contract.Message, streaming bool) (*callResult, error) {
	v.sessionID()

	raw, ok := msg.Extra[KeyRawBody].([]byte)
	if !ok {
		return nil, fmt.Errorf("opencode: missing %s in message extra", KeyRawBody)
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		// 与历史一致：请求体不可解析 → 400 响应，不视为传输失败。
		if streaming {
			return &callResult{status: http.StatusBadRequest, stream: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		return &callResult{status: http.StatusBadRequest}, nil
	}
	a := parseAuth(msg)
	modelID := msg.Model
	maxRetries := maxRetriesOf(msg)

	var lastBody []byte
	var lastStatus int
	var lastHeader http.Header
	var lastProxyAddr string
	var lastErr error
	var retryCount, retry401Count int

	for retryCount <= maxRetries {
		up, err := v.buildRequest(modelID, bodyMap, a)
		if err != nil {
			lastErr = err
			break
		}
		tr := v.transport()
		client, proxyAddr := tr.Client(a.tier(), streaming)
		resp, err := client.Do(up)
		if err != nil {
			tr.Mark(proxyAddr, 0, err)
			lastErr = err
			retryCount++
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			tr.Mark(proxyAddr, resp.StatusCode, nil)
			if streaming {
				return &callResult{stream: resp.Body, status: resp.StatusCode, nodeAddr: proxyAddr}, nil
			}
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if isAnthropicFormat(b) {
				b = convertAnthropicToOpenAI(b, modelID)
			}
			return &callResult{body: b, status: resp.StatusCode, headers: resp.Header, nodeAddr: proxyAddr}, nil
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
		if v.isRetryable(resp.StatusCode) {
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
		if streaming {
			// 非可重试状态：把错误体包装成流返回（上游错误透传，不重试）。
			return &callResult{stream: io.NopCloser(bytes.NewReader(lastBody)), status: lastStatus, nodeAddr: lastProxyAddr}, nil
		}
		break
	}
	if streaming {
		if lastStatus != 0 {
			return &callResult{stream: io.NopCloser(bytes.NewReader(lastBody)), status: lastStatus, nodeAddr: lastProxyAddr}, nil
		}
		return nil, fmt.Errorf("all models failed")
	}
	return &callResult{body: lastBody, status: lastStatus, headers: lastHeader, nodeAddr: lastProxyAddr}, lastErr
}

// Chat 实现 contract.Vendor（非流式）。
// 非 2xx / 传输失败时同时返回（含错误体的 Reply, error），供上层做厂商级 failover。
func (v *Vendor) Chat(ctx context.Context, msg *contract.Message) (*contract.Reply, error) {
	res, err := v.call(ctx, msg, false)
	if res == nil {
		return nil, err
	}
	return &contract.Reply{Body: res.body, Status: res.status, Headers: res.headers, NodeAddr: res.nodeAddr}, err
}

// ChatStream 实现 contract.Vendor（流式 SSE）。
// 错误体包装为可读流返回（error=nil，与历史行为一致）；仅完全无响应时返回 error。
func (v *Vendor) ChatStream(ctx context.Context, msg *contract.Message) (*contract.Stream, error) {
	res, err := v.call(ctx, msg, true)
	if res == nil {
		return nil, err
	}
	return &contract.Stream{ReadCloser: res.stream, Status: res.status, NodeAddr: res.nodeAddr}, nil
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
