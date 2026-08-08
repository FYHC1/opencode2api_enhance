// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

func chatCompletionsHandler(w http.ResponseWriter, r *http.Request) {
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
	slog.Debug("chat completion request body", "count", cnt, "body", string(body))

	var req OpenAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Model = resolveModel(req.Model)
	if req.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	// 全流程调用日志：记录每个请求的决策链（网关模式下）
	startTime := time.Now()
	callRec := CallRecord{
		ReqID:     getReqID(r.Context()),
		TS:        time.Now().Format(time.RFC3339),
		Path:      r.URL.Path,
		Model:     req.Model,
		Stream:    req.Stream,
		RouteMode: routeMode,
		Status:    "ok",
	}
	if callRec.ReqID == "" {
		callRec.ReqID = "req_" + randomString(12)
	}

	// 多模态路由：检测到图片时转发到配置的上游

	req.Messages = fixToolCallGaps(req.Messages)
	keepReasoning := wantsReasoning(&req)
	req.Messages = ensureReasoningContent(req.Messages, keepReasoning)
	if req.Stream {
		if req.ExtraBody == nil {
			req.ExtraBody = map[string]any{}
		}
		req.ExtraBody["stream_options"] = map[string]any{"include_usage": true}
	}
	upstreamBody := buildUpstreamBody(&req)

	if req.Stream {
		upResp, status, _, proxyAddr, err := callOpenCodeAPIStream(upstreamBody, req.Model, auth)
		callRec.Nodes = append(callRec.Nodes, proxyAddr)
		if err != nil || status < 200 || status >= 300 {
			callRec.Status = "fail"
			callRec.ErrMsg = fmt.Sprintf("upstream status %d: %v", status, err)
			callRec.Events = append(callRec.Events, CallEvent{Type: "upstream_error", Node: proxyAddr, Detail: callRec.ErrMsg, At: time.Now()})
			recordCall(callRec)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if upResp != nil {
				errBody, _ := io.ReadAll(upResp)
				if len(errBody) > 0 {
					w.Write(errBody)
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "upstream error", "type": "upstream_error"}})
			return
		}
		callRec.Events = append(callRec.Events, CallEvent{Type: "connect_ok", Node: proxyAddr, Detail: "connected", At: time.Now()})
		defer upResp.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		// 流内超时 + 断点续写切换（阶段1验证过的核心逻辑）
		res := streamWithResume(w, r, upstreamBody, req.Model, auth, upResp, proxyAddr, keepReasoning, &callRec)
		callRec.DurationMS = time.Since(startTime).Milliseconds()
		if res.PromptTok > 0 || res.Completion > 0 {
			callRec.PromptTok = res.PromptTok
			callRec.CompletionTok = res.Completion
			recordTokenUsage(req.Model, res.PromptTok, res.Completion, res.PromptTok+res.Completion, proxyAddr)
		}
		if !res.OK {
			callRec.Status = "fail"
			if res.ErrMsg != "" {
				callRec.ErrMsg = res.ErrMsg
			}
			// 若未吐过 [DONE]，补错误事件
			w.Write([]byte("data: {\"error\":\"stream interrupted: " + res.ErrMsg + "\"}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		} else {
			callRec.Status = "ok"
		}
		recordCall(callRec)
		return
	}

	respBody, status, _, proxyAddr, err := callOpenCodeAPI(upstreamBody, req.Model, auth)
	callRec.Nodes = append(callRec.Nodes, proxyAddr)
	if err != nil || status < 200 || status >= 300 {
		callRec.Status = "fail"
		callRec.ErrMsg = fmt.Sprintf("upstream status %d: %v", status, err)
		callRec.DurationMS = time.Since(startTime).Milliseconds()
		callRec.Events = append(callRec.Events, CallEvent{Type: "upstream_error", Node: proxyAddr, Detail: callRec.ErrMsg, At: time.Now()})
		recordCall(callRec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if len(respBody) > 0 {
			w.Write(respBody)
		} else {
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "upstream error", "type": "upstream_error"}})
		}
		return
	}
	outBody := respBody
	convertedResp, err := convertResponse(respBody, keepReasoning)
	if err == nil {
		outBody = convertedResp
	}
	// Record token usage
	var usageResp map[string]any
	if json.Unmarshal(respBody, &usageResp) == nil {
		if u, ok := usageResp["usage"].(map[string]any); ok {
			pt, _ := u["prompt_tokens"].(float64)
			ct, _ := u["completion_tokens"].(float64)
			tt, _ := u["total_tokens"].(float64)
			if tt > 0 {
				recordTokenUsage(req.Model, int64(pt), int64(ct), int64(tt), proxyAddr)
			}
			callRec.PromptTok = int64(pt)
			callRec.CompletionTok = int64(ct)
		}
	}
	callRec.DurationMS = time.Since(startTime).Milliseconds()
	callRec.Events = append(callRec.Events, CallEvent{Type: "complete", Node: proxyAddr, Detail: "done", At: time.Now()})
	recordCall(callRec)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(outBody)
}

// ======================== Models Handler ========================

func listModelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	modelMu.RLock()
	loaded, models := modelsLoaded, modelsCache
	modelMu.RUnlock()
	if !loaded || len(models) == 0 {
		fetched, err := fetchModels()
		if err == nil && len(fetched) > 0 {
			modelMu.Lock()
			modelsCache = fetched
			modelsLoaded = true
			models = modelsCache
			modelMu.Unlock()
		}
	}
	if len(models) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "无法获取模型列表，请检查上游服务是否可用",
		})
		return
	}
	// 保存别名快照；目录权限仍按真实上游模型判断，最后再替换为客户端可见名称。
	configMu.RLock()
	aliases := make(map[string]string, len(modelAlias))
	for alias, upstream := range modelAlias {
		aliases[alias] = upstream
	}
	configMu.RUnlock()

	auth := extractUpstreamAuth(r)
	var combinedModels []ModelInfo
	switch {
	case auth.shouldUseGoCatalog():
		modelMu.RLock()
		combinedModels = make([]ModelInfo, 0, len(models)+len(goModelsCache))
		for _, model := range models {
			if isFreeModel(model.ID) {
				combinedModels = append(combinedModels, model)
			}
		}
		for _, goModel := range goModelsCache {
			if !containsModelWithID(combinedModels, goModel.ID) {
				combinedModels = append(combinedModels, goModel)
			}
		}
		modelMu.RUnlock()
	case auth.Mode == AuthRoutePublic:
		combinedModels = models
		filtered := make([]ModelInfo, 0, len(combinedModels))
		for _, m := range combinedModels {
			if isFreeModel(m.ID) {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) > 0 {
			combinedModels = filtered
		}
	default:
		combinedModels = models
	}
	allModels := replaceModelIDsWithAliases(combinedModels, aliases)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   allModels,
	})
}

func replaceModelIDsWithAliases(models []ModelInfo, aliases map[string]string) []ModelInfo {
	aliasesByUpstream := make(map[string][]string, len(aliases))
	for alias, upstream := range aliases {
		alias = strings.TrimSpace(alias)
		upstream = strings.TrimSpace(upstream)
		if alias == "" || upstream == "" {
			continue
		}
		aliasesByUpstream[upstream] = append(aliasesByUpstream[upstream], alias)
	}
	for upstream := range aliasesByUpstream {
		sort.Strings(aliasesByUpstream[upstream])
	}

	result := make([]ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		visibleIDs := aliasesByUpstream[model.ID]
		if len(visibleIDs) == 0 {
			// 自动兜底：未配置别名的 -free 模型，展示名去掉 -free 后缀
			// （内部请求仍用原名；显式别名优先）。
			if strings.HasSuffix(model.ID, "-free") {
				visibleIDs = []string{strings.TrimSuffix(model.ID, "-free")}
			} else {
				visibleIDs = []string{model.ID}
			}
		}
		for _, visibleID := range visibleIDs {
			if _, exists := seen[visibleID]; exists {
				continue
			}
			visibleModel := model
			visibleModel.ID = visibleID
			if visibleID != model.ID {
				visibleModel.OwnedBy = "alias"
			}
			result = append(result, visibleModel)
			seen[visibleID] = struct{}{}
		}
	}
	return result
}

// ======================== Claude Messages API ========================

func extractClaudeSystemText(system any) string {
	if system == nil {
		return ""
	}
	switch v := system.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if block, ok := item.(map[string]any); ok {
				if block["type"] == "text" {
					if text, ok := block["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func cleanJsonSchema(schema any) any {
	m, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	clean := make(map[string]any, len(m))
	for k, v := range m {
		// Annotation-only keys are omitted for upstream compatibility. Constraint
		// keys such as additionalProperties and format are preserved.
		if k == "$schema" || k == "title" || k == "examples" {
			continue
		}
		switch child := v.(type) {
		case map[string]any:
			clean[k] = cleanJsonSchema(child)
		case []any:
			copyArray := make([]any, len(child))
			for i, elem := range child {
				copyArray[i] = cleanJsonSchema(elem)
			}
			clean[k] = copyArray
		default:
			clean[k] = v
		}
	}
	return clean
}
