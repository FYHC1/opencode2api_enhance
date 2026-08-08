// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

type ClaudeRequest struct {
	Model         string          `json:"model"`
	Messages      []ClaudeMessage `json:"messages"`
	System        any             `json:"system,omitempty"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Tools         []ClaudeTool    `json:"tools,omitempty"`
	ToolChoice    any             `json:"tool_choice,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Metadata      any             `json:"metadata,omitempty"`
	Thinking      any             `json:"thinking,omitempty"`
}

type ClaudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ClaudeContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
}

type ClaudeTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type ClaudeResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Content    []ClaudeContent `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      ClaudeUsage     `json:"usage,omitempty"`
}

type ClaudeUsage map[string]any

// ======================== Responses API 类型 ========================
