package windsurf

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
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

// Chatter 是上游聊天传输（Devin/Windsurf Connect-RPC）。P3-B 移植实现。
type Chatter interface {
	// DoChat 非流式聊天。acct 是账号池中的账号标识（邮箱）。
	DoChat(ctx context.Context, acct string, msg *contract.Message) (*contract.Reply, error)
	// DoChatStream 流式聊天（SSE 转换后返回 Stream，NodeAddr 可空）。
	DoChatStream(ctx context.Context, acct string, msg *contract.Message) (*contract.Stream, error)
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
