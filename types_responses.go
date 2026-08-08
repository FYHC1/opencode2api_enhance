// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

type ResponsesAPIRequest struct {
	Model              string          `json:"model"`
	Input              any             `json:"input"`
	Messages           []Message       `json:"messages,omitempty"`
	Instructions       string          `json:"instructions,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	MaxTokens          *int            `json:"max_output_tokens,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	FrequencyPenalty   *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty    *float64        `json:"presence_penalty,omitempty"`
	Reasoning          ReasonEffort    `json:"reasoning,omitempty"`
	Include            []string        `json:"include,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	Tools              []ResponsesTool `json:"tools,omitempty"`
	ToolChoice         any             `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool           `json:"parallel_tool_calls,omitempty"`
	Stop               any             `json:"stop,omitempty"`
	User               string          `json:"user,omitempty"`
	StreamOptions      any             `json:"stream_options,omitempty"`
	Metadata           any             `json:"metadata,omitempty"`
}

type ResponsesTool struct {
	Type            string         `json:"type"`
	Name            string         `json:"name,omitempty"`
	Description     string         `json:"description,omitempty"`
	Parameters      map[string]any `json:"parameters,omitempty"`
	Function        *ToolFunction  `json:"function,omitempty"`
	ServerLabel     string         `json:"server_label,omitempty"`
	ServerURL       string         `json:"server_url,omitempty"`
	ConnectorID     string         `json:"connector_id,omitempty"`
	Authorization   string         `json:"authorization,omitempty"`
	AllowedTools    []string       `json:"allowed_tools,omitempty"`
	RequireApproval any            `json:"require_approval,omitempty"`
}

type ReasonEffort struct {
	Effort string `json:"effort,omitempty"`
}

type StoredResponseState struct {
	Model        string          `json:"model"`
	Instructions string          `json:"instructions,omitempty"`
	Tools        []ResponsesTool `json:"tools,omitempty"`
	ToolChoice   any             `json:"tool_choice,omitempty"`
	Output       []any           `json:"output,omitempty"`
}

// ======================== 配置管理 ========================
