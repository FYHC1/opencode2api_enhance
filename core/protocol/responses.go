// Package protocol 定义协议层类型与纯转换（P1.2b）。
// 本文件：Responses API 纯转换辅助（P1.2b 函数下沉）。
package protocol

import "encoding/json"

// ResponseOutcome 是流式/非流式 builder 共享的顶层状态，防止二者漂移。
type ResponseOutcome struct {
	Status            string
	Event             string
	IncompleteDetails any
}

// ResponsesOutcome 按 finish_reason 映射响应终态。
func ResponsesOutcome(finishReason string) ResponseOutcome {
	if finishReason == "length" {
		return ResponseOutcome{Status: "incomplete", Event: "response.incomplete", IncompleteDetails: map[string]any{"reason": "max_output_tokens"}}
	}
	return ResponseOutcome{Status: "completed", Event: "response.completed"}
}

// OutputIndexAllocator 按首次出现分配索引（不依赖其它项是否存在）。
type OutputIndexAllocator struct{ next int }

// Allocate 分配下一个索引。
func (a *OutputIndexAllocator) Allocate() int {
	index := a.next
	a.next++
	return index
}

// Len 已分配索引数。
func (a *OutputIndexAllocator) Len() int { return a.next }

// ApplyResponsesRequestEcho 把请求的透传字段回显到响应。
func ApplyResponsesRequestEcho(response map[string]any, req ResponsesAPIRequest) {
	if req.Metadata != nil {
		response["metadata"] = CloneJSONValue(req.Metadata)
	}
	if req.Reasoning.Effort != "" {
		response["reasoning"] = map[string]any{"effort": req.Reasoning.Effort}
	}
	if req.ParallelToolCalls != nil {
		response["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.Temperature != nil {
		response["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		response["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		response["max_output_tokens"] = *req.MaxTokens
	}
	if req.Store != nil {
		response["store"] = *req.Store
	}
}

// CloneJSONValue 深拷贝任意 JSON 可序列化值（泛型）。
func CloneJSONValue[T any](value T) T {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned T
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return value
	}
	return cloned
}
