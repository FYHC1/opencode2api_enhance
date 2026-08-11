// Package protocol 定义协议层类型与纯转换（P1.2b）。
// 本文件：Anthropic→Chat Completions 的纯转换辅助（P1.2b 函数下沉）。
package protocol

// NormalizeFinishReason maps Anthropic stop reasons onto the closed set used
// by Chat Completions.
func NormalizeFinishReason(reason string) string {
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

// AnthropicUsageToChat 把 Anthropic usage 字段映射为 Chat Completions 形态。
func AnthropicUsageToChat(usage map[string]any) map[string]any {
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
	if p, pok := NumberAsFloat(out["prompt_tokens"]); pok {
		if c, cok := NumberAsFloat(out["completion_tokens"]); cok {
			out["total_tokens"] = p + c
		}
	}
	delete(out, "input_tokens")
	delete(out, "output_tokens")
	return out
}

// NumberAsFloat 把数值类型归一为 float64。
func NumberAsFloat(v any) (float64, bool) {
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
