// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

type AppConfig struct {
	ModelAlias           map[string]string `json:"model_alias"`
	ReasoningEffortMap   map[string]string `json:"reasoning_effort_map"`
	ForceDisableThinking bool              `json:"force_disable_thinking"`
	Socks5Proxies        []Socks5Proxy     `json:"socks5_proxies,omitempty"`
	ActiveSocks5         string            `json:"active_socks5,omitempty"`
	// RouteMode 网关/代理池路由模式：failover（默认，成功不动游标，失败才切换）| round_robin
	RouteMode string `json:"route_mode,omitempty"`

	// 流内超时切换配置（毫秒；区间随机，防上游识别为定时扫描）
	TTFTMinMS    int `json:"timeout_ttft_min_ms,omitempty"`
	TTFTMaxMS    int `json:"timeout_ttft_max_ms,omitempty"`
	SilenceMinMS int `json:"timeout_silence_min_ms,omitempty"`
	SilenceMaxMS int `json:"timeout_silence_max_ms,omitempty"`
	ProbeMin     int `json:"failover_probe_min,omitempty"`
	ProbeMax     int `json:"failover_probe_max,omitempty"`
	// 调用日志保留上限（条）
	CallLogMax int `json:"call_log_max,omitempty"`

	// 坏状态码组：状态码 → 原因文案，遇到即切节点并计数（可配置，默认见 badStatusCodes）
	BadStatusCodes map[string]string `json:"bad_status_codes,omitempty"`
	// 坏池阈值：连续坏状态码次数达到后节点进坏池（默认 3）
	BadThreshold int `json:"bad_threshold,omitempty"`
	// ShowNodePrefix 是否在对话流首段展示「🤖 节点 · 模型」前缀（默认关闭）
	ShowNodePrefix *bool `json:"show_node_prefix,omitempty"`

	// Providers 厂商注册表（配置驱动；缺省 = 单 opencode）
	Providers []ProviderCfg `json:"providers,omitempty"`
	// Routing 模型→厂商路由
	Routing RoutingCfg `json:"routing,omitempty"`
}

// ProviderCfg 描述一个模型厂商（vendors/ 下的实现）。
type ProviderCfg struct {
	ID   string `json:"id"`   // 厂商标识，与厂商实现 ID() 一致（如 "opencode"）
	Type string `json:"type"` // 厂商类型（"opencode" | "windsurf" | ...），用于选择实现
	Name string `json:"name,omitempty"`
	// Enabled 开关；nil 视为 true。
	Enabled *bool `json:"enabled,omitempty"`
}

// RoutingCfg 是模型→厂商分发配置。
type RoutingCfg struct {
	// ModelProvider 模型名 → 厂商 ID 的强制映射（优先于厂商目录匹配）。
	ModelProvider map[string]string `json:"model_provider_map,omitempty"`
	// DefaultProvider 兜底厂商（缺省 "opencode"）。
	DefaultProvider string `json:"default_provider,omitempty"`
}

// ======================== Claude Messages API 类型 ========================
