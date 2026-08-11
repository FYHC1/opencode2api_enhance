// Package protocol 定义协议层类型与纯转换（P1.2b 函数下沉）。
// 本文件：Anthropic Claude 请求侧协议边界（自根目录 anthropic_protocol.go 下沉）。
package protocol

// ConvertClaudeRequest 是请求侧协议边界。它返回一个全新的 Chat Completions
// 请求，并且永不修改调用方所拥有的值。
func ConvertClaudeRequest(req ClaudeRequest) OpenAIRequest {
	out := OpenAIRequest{
		Model: req.Model, Messages: ClaudeToOpenAIMessages(req.Messages, req.System),
		Stream: req.Stream, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		TopP: req.TopP, Tools: ClaudeToOpenAITools(req.Tools),
		ToolChoice: ConvertClaudeToolChoice(req.ToolChoice),
	}
	if req.TopK != nil {
		if out.ExtraBody == nil {
			out.ExtraBody = map[string]any{}
		}
		out.ExtraBody["top_k"] = *req.TopK
	}
	if len(req.StopSequences) > 0 {
		if out.ExtraBody == nil {
			out.ExtraBody = map[string]any{}
		}
		out.ExtraBody["stop"] = append([]string(nil), req.StopSequences...)
	}
	if metadata, ok := req.Metadata.(map[string]any); ok {
		if user, ok := metadata["user_id"].(string); ok && user != "" {
			if out.ExtraBody == nil {
				out.ExtraBody = map[string]any{}
			}
			out.ExtraBody["user"] = user
		}
	}
	return out
}

// ConvertClaudeToolChoice 把 Anthropic tool_choice 映射为 Chat Completions 形态。
func ConvertClaudeToolChoice(choice any) any {
	m, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	switch m["type"] {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		if name, ok := m["name"].(string); ok && name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
	}
	return choice
}
