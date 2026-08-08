package windsurf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/vendors/windsurf/connect"
)

// ---------------------------------------------------------------------------
// 可插拔接缝（P3-B 移植 Connect-RPC/tmaily 真实实现时填充）
// ---------------------------------------------------------------------------

// MailboxProvider 提供一次性临时邮箱（收置信件）。
type MailboxProvider interface {
	// Create 申请一个新邮箱地址（如 wsf<hex>@<domain>）。
	Create(ctx context.Context) (address string, err error)
	// WaitCode 轮询该邮箱返回的验证码（服务端提示词含 qhint 关键字则命中）。
	WaitCode(ctx context.Context, address, hint string, timeout time.Duration) (code string, err error)
}

// Registrar 完成一个账号的注册链路（临时邮箱 → 服务端注册 → session token 入库）。
type Registrar interface {
	// Register 注册一个账号并返回其唯一标识（邮箱），供账号池记录。
	Register(ctx context.Context, mb MailboxProvider) (email string, err error)
}

// Chatter 是上游聊天传输（Devin/Windsurf Connect-RPC）。P3-B 由 connect 包实现。
type Chatter interface {
	// DoChat 非流式聊天。token 是账号的 windsurf session token。
	DoChat(ctx context.Context, token string, msg *contract.Message) (*contract.Reply, error)
	// DoChatStream 流式聊天（返回 OpenAI 兼容 SSE 流）。
	DoChatStream(ctx context.Context, token string, msg *contract.Message) (*contract.Stream, error)
}

// ---------------------------------------------------------------------------
// ConnectChatter：用 connect 包实现的默认 Chatter
// ---------------------------------------------------------------------------

// NewConnectChatter 构造 Connect-RPC 聊天传输。
func NewConnectChatter(hc *http.Client) Chatter {
	return &connectChatter{client: connect.NewClient(hc)}
}

type connectChatter struct {
	client *connect.Client
}

func (c *connectChatter) DoChat(ctx context.Context, token string, msg *contract.Message) (*contract.Reply, error) {
	res, err := c.client.DoChat(ctx, token, toChatMessages(msg), msg.Model, nil, nil)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf(
		`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":%s}],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`,
		jsonQuote(res.Content), jsonQuote(res.FinishReason), res.PromptTokens, res.CompletionTokens,
	)
	return &contract.Reply{Body: []byte(body), Status: http.StatusOK}, nil
}

func (c *connectChatter) DoChatStream(ctx context.Context, token string, msg *contract.Message) (*contract.Stream, error) {
	rc, err := c.client.StreamSSE(ctx, token, toChatMessages(msg), msg.Model, nil, nil)
	if err != nil {
		return nil, err
	}
	return &contract.Stream{ReadCloser: rc, Status: http.StatusOK}, nil
}

// toChatMessages 把 contract.Message 归一化消息转成上游文本消息。
func toChatMessages(msg *contract.Message) []connect.ChatMessage {
	var out []connect.ChatMessage
	for _, m := range msg.Messages {
		out = append(out, connect.ChatMessage{Role: m.Role, Content: flattenContent(m.Content)})
	}
	if len(out) == 0 {
		out = append(out, connect.ChatMessage{Role: "user", Content: "hello"})
	}
	return out
}

// flattenContent 把 string / []Part 内容展平为文本（图片降级为占位文本）。
func flattenContent(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, part := range v {
			if pm, ok := part.(map[string]any); ok {
				if text, ok := pm["text"].(string); ok {
					sb.WriteString(text)
				} else if pm["type"] == "image_url" {
					sb.WriteString("[image]")
				}
			}
		}
		return sb.String()
	default:
		return ""
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------------------
// 暂缺实现时给出明确占位（不产生假行为）
// ---------------------------------------------------------------------------

// tmailyMailbox 是 TMaily 临时邮箱的骨架实现（真实 HTTP 客户端见 P3-B）。
// 当前占位：返回明确未实现错误，避免静默假注册。
type tmailyMailbox struct {
	c *http.Client
}

// newTMailyMailbox 构造 TMaily 邮箱提供者（P3-B 实现 HTTP 逻辑）。
func newTMailyMailbox(client *http.Client) MailboxProvider {
	return &tmailyMailbox{c: client}
}

func (t *tmailyMailbox) Create(_ context.Context) (string, error) {
	return "", errNotImplemented("TMaily create mailbox (P3-B)")
}

func (t *tmailyMailbox) WaitCode(_ context.Context, _ string, _ string, _ time.Duration) (string, error) {
	return "", errNotImplemented("TMaily wait code (P3-B)")
}

type errNotImplemented string

func (e errNotImplemented) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// maskEmail 打码邮箱做日志展示（避免敏感轮换）。
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	if len(local) > 3 {
		local = local[:3] + "..."
	}
	return local + "@" + email[at+1:]
}

// ensureReadAll 读取流并回卷（用于不支持流的非流式调用）。
func ensureReadAll(r io.ReadCloser) ([]byte, error) {
	defer r.Close()
	return io.ReadAll(r)
}

// httpDo 简单封装底层 HTTP 调用（供 P3-B 各实现复用）。
func httpDo(c *http.Client, req *http.Request) (*http.Response, error) {
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}
