// Package contract 定义厂商与 core 之间的"数据线束"契约。
//
// 每个厂商（vendors/ 下的子文件夹）必须实现 Vendor 接口；
// 账号池型厂商（如 windsurf）在此基础上可选实现 PoolVendor 接口。
// core 只认本包的契约，厂商之间互不感知。
package contract

import (
	"context"
	"io"
	"net/http"
)

// Capabilities 描述一个模型的能力元数据（可选）。
// 供路由器做"门当户对"匹配（如不支持 tools 的模型就不该接 tools 请求），
// 也供上层 UI 展示模型能力。
type Capabilities struct {
	SupportsTools    bool // 是否支持 function calling / tools
	SupportsThinking bool // 是否支持思考模式
	SupportsVision   bool // 是否支持图片输入
	ContextWindow    int  // 上下文窗口大小（token）
	MaxTokens        int  // 单次最大输出 token（0=未知）
}

// Model 是厂商模型目录中的一条记录。
type Model struct {
	ID       string       // 厂商内部模型名（如 opencode 的 deepseek-v4-flash-free）
	Provider string       // 厂商 ID（如 "opencode" / "windsurf"）
	Free     bool         // 是否免费额度模型
	Caps     Capabilities // 能力元数据（可选，未知则零值）
}

// Msg 是归一化请求中的一条消息。
type Msg struct {
	Role             string
	Content          any // string 或 []Part（多模态）；与现有 Message.Content 语义一致
	ReasoningContent *string
	ToolCalls        []ToolCall
	ToolCallID       string
	Name             string
}

// ToolCall 是一次工具调用的结果/请求。
type ToolCall struct {
	ID       string
	Type     string
	Function FunctionCall
}

// FunctionCall 是工具调用中的 function 部分。
type FunctionCall struct {
	Name      string
	Arguments string
}

// Tool 定义可被模型调用的工具。
type Tool struct {
	Type     string
	Function ToolFunction
}

// ToolFunction 是工具的函数定义。
type ToolFunction struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Message 是 core 发给厂商的统一归一化请求（OpenAI Chat 形态）。
// 入站协议（OpenAI/Anthropic/Responses）在 core/protocol 统一转换为该形态；
// 厂商实现负责"该形态 → 厂商原生请求"的适配。
type Message struct {
	Model    string         // 厂商内部模型名（已由 core 解析/别名后）
	Messages []Msg          // 对话消息
	Stream   bool           // 是否流式 SSE
	Options  map[string]any // 透传选项：temperature / max_tokens / top_p / tools / thinking / extra_body 等
}

// Reply 是厂商返回给 core 的统一结果。
// Body 为 OpenAI Chat 形态（流式为逐 chunk），core 负责再转回各入站协议。
type Reply struct {
	Body    []byte      // 响应体
	Status  int         // HTTP 状态码
	Headers http.Header // 响应头（透传安全头）
}

// ErrRules 描述厂商可重试/可切换/进坏账的状态码语义。
// 现状（opencode）的 401/402/429/503 是写死的，这里按厂商差异化。
type ErrRules struct {
	Retryable  []int // 该状态码整体可重试（如 429/502/503/504）
	Switchable []int // 该状态码可切换下一个候选（另一个厂商/另一个账号）
	BadPool    []int // 连续命中进入坏池的状态码（如 401/402/429/503）
}

// VendorHealth 描述厂商当前健康状态（供 failover 与 UI）。
type VendorHealth struct {
	Available   bool   // 是否可用
	LastError   string // 最近一次错误描述
	LastSuccess string // 最近一次成功时间（RFC3339）
}

// Vendor 是每个厂商子文件夹必须实现的"数据线束"接口。
type Vendor interface {
	// ID 返回厂商唯一标识（如 "opencode"、"windsurf"），须与配置 providers[].id 一致。
	ID() string
	// Name 返回厂商展示名（如 "OpenCode"、"Devin/Windsurf"）。
	Name() string

	// ListModels 拉取厂商动态模型目录；core 做聚合与缓存。
	// 厂商内部应先返回静态兜底，再尝试动态刷新。
	ListModels(ctx context.Context) ([]Model, error)

	// IsFree 判定模型是否免费额度模型（厂商自有规则）。
	IsFree(modelID string) bool

	// Chat 发起非流式聊天，返回 OpenAI 形态响应体。
	Chat(ctx context.Context, msg *Message) (*Reply, error)

	// ChatStream 发起流式 chat，返回 SSE 响应体（core 负责读流/续写）。
	ChatStream(ctx context.Context, msg *Message) (io.ReadCloser, error)

	// Auth 根据入站请求构造厂商请求的认证头（如 "Bearer public"、"Bearer sk-..."）。
	Auth(r *http.Request) string

	// ErrSemantics 返回厂商的错误状态码语义（core 据此决定重试/切换/坏账）。
	ErrSemantics() ErrRules

	// Health 返回厂商健康状态。
	Health() VendorHealth
}

// PoolVendor 是账号池型厂商的可选扩展接口。
// core 用类型断言发现并调用"账号运维"能力：
// 请求前 EnsureReady 保证有可用账号（必要时自动注册），
// 选取账号、额度阈值预注册、24h 冷却等都由厂商内部实现。用户全程无感。
type PoolVendor interface {
	Vendor

	// EnsureReady 保证至少有一个可用账号；无可用账号时自动注册新号（可能阻塞，可后台）。
	EnsureReady(ctx context.Context) error

	// PoolStatus 返回账号池状态（可用数/冷却数/是否枯竭/最低额度）。
	PoolStatus() PoolStatus

	// Acquire 借出一个健康账号（受冷却与健康约束）。
	Acquire() (AcctID, error)

	// Release 归还账号（进入冷却状态）。
	Release(id AcctID)
}

// AcctID 是池型厂商内部的账号标识（如邮箱）。
type AcctID string

// PoolStatus 描述账号池状态（供 core 分发与 UI 展示）。
type PoolStatus struct {
	Available int     // 可用账号数
	Cooling   int     // 冷却中账号数
	Drought   bool    // 全池枯竭（无可选账号）
	QuotaMin  float64 // 全池最低剩余额度（%）
}