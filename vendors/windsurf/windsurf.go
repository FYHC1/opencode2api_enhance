// Package windsurf 实现 Devin/Windsurf 账号池型厂商（contract.Vendor + contract.PoolVendor）。
//
// 形态：账号池自动注册、健康度挑选、24h 冷却复用、额度阈值预注册——用户无感。
// 上游聊天（server.codeium.com 的 Connect-RPC）通过 Chatter 接口注入；protocol 移植在
// chatter.go（当前仅接口 + 说明，P3-B 填充），以此包先打通"池运维 + 契约 + 换号"。
package windsurf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// freeModel 免费实测模型（源自 windsurf-account-manager）。
const freeModel = "swe-1-6-slow"

var (
	ErrNoAccount    = errors.New("windsurf: 无可用账号")
	ErrUnavailable  = errors.New("windsurf: 未装配注册/聊天实现")
	ErrNotWiredChat = errors.New("windsurf: Chatter 未接线（P3-B）")
)

// Config 是池型厂商装配配置。
type Config struct {
	ID   string // 厂商标识（默认 "windsurf"）
	Name string // 展示名（默认 "Devin/Windsurf"）

	// HTTPClient 上游 HTTP 复用（nil → http.DefaultClient）。
	HTTPClient *http.Client

	// MinAvailable EnsureReady 保持的最小可用账号数（默认 1）。
	MinAvailable int
	// QuotaThreshold 预注册阈值（全池最低剩余额度%≤此值触发后台注册；默认 20）。
	QuotaThreshold float64
	// Cooldown 账号冷却时长（默认 24h）。
	Cooldown time.Duration
	// StoreFile 账号库 JSON 路径（空 = 仅内存）。
	StoreFile string

	// Mailbox 临时邮箱提供者（nil 则无法自动注册）。
	Mailbox MailboxProvider
	// Registrar 注册链路（nil 则无法自动注册）。
	Registrar Registrar
	// Chatter 上游聊天协议实现（Connect-RPC；nil 时 Chat 报未接线）。
	Chatter Chatter
}

// Vendor 实现 contract.Vendor 与 contract.PoolVendor。
type Vendor struct {
	cfg  Config
	pool *Pool

	mu          sync.Mutex
	registering bool // 防并发重复注册
	lastErr     string
	lastSuccess time.Time
}

// New 构造 windsurf 厂商。
func New(cfg Config) *Vendor {
	if cfg.ID == "" {
		cfg.ID = "windsurf"
	}
	if cfg.Name == "" {
		cfg.Name = "Devin/Windsurf"
	}
	if cfg.MinAvailable <= 0 {
		cfg.MinAvailable = 1
	}
	if cfg.QuotaThreshold <= 0 || cfg.QuotaThreshold > 100 {
		cfg.QuotaThreshold = 20
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 24 * time.Hour
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	v := &Vendor{cfg: cfg}
	v.pool = newPool(cfg.Cooldown, cfg.StoreFile)
	if cfg.StoreFile != "" {
		if err := v.pool.loadFile(cfg.StoreFile); err != nil {
			slog.Warn("windsurf: load store", "error", err)
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// contract.Vendor 基础接口
// ---------------------------------------------------------------------------

func (v *Vendor) ID() string   { return v.cfg.ID }
func (v *Vendor) Name() string { return v.cfg.Name }

// ListModels 实现 contract.Vendor：账号池厂商免费模型为固定实测列表。
func (v *Vendor) ListModels(_ context.Context) ([]contract.Model, error) {
	return []contract.Model{{
		ID:       freeModel,
		Provider: v.cfg.ID,
		Free:     true,
		Caps:     contract.Capabilities{SupportsTools: true},
	}}, nil
}

// IsFree 实现 contract.Vendor。
func (v *Vendor) IsFree(modelID string) bool { return modelID == freeModel }

// ErrSemantics 实现 contract.Vendor：capacity/429/401 等按可切账号处理。
func (v *Vendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{
		Retryable:  []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
		Switchable: []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable},
		BadPool:    []int{http.StatusUnauthorized, http.StatusPaymentRequired},
	}
}

// Auth 实现 contract.Vendor：池型厂商按账号内部认证，入站不携带上游 key。
func (v *Vendor) Auth(_ *http.Request) string { return "" }

// Health 实现 contract.Vendor。
func (v *Vendor) Health() contract.VendorHealth {
	v.mu.Lock()
	defer v.mu.Unlock()
	h := contract.VendorHealth{Available: true}
	if !v.lastSuccess.IsZero() {
		h.LastSuccess = v.lastSuccess.Format(time.RFC3339)
	}
	if v.lastErr != "" {
		h.LastError = v.lastErr
	}
	return h
}

// ---------------------------------------------------------------------------
// contact.PoolVendor 能力（账号运维）
// ---------------------------------------------------------------------------

// EnsureReady 保证至少 MinAvailable 个可用账号；不足则自动注册（同步补齐）。
func (v *Vendor) EnsureReady(ctx context.Context) error {
	now := time.Now()
	avail := len(v.pool.available(now))
	if avail >= v.cfg.MinAvailable {
		return nil
	}
	return v.registerNew(ctx, v.cfg.MinAvailable-avail)
}

// registerNew 注册 need 个新账号（串行，防并发重复注册；无 Registrar 时报错）。
func (v *Vendor) registerNew(ctx context.Context, need int) error {
	if v.cfg.Registrar == nil || v.cfg.Mailbox == nil {
		return ErrUnavailable
	}
	if need <= 0 {
		return nil
	}
	v.mu.Lock()
	if v.registering {
		v.mu.Unlock()
		return errors.New("windsurf: 正在注册中")
	}
	v.registering = true
	v.mu.Unlock()
	defer func() {
		v.mu.Lock()
		v.registering = false
		v.mu.Unlock()
	}()

	for i := 0; i < need; i++ {
		email, err := v.cfg.Registrar.Register(ctx, v.cfg.Mailbox)
		if err != nil {
			v.mu.Lock()
			v.lastErr = err.Error()
			v.mu.Unlock()
			return err
		}
		v.pool.add(&Account{
			Email:      email,
			QuotaDaily: 100, QuotaWeekly: 100, // 未知额度，乐观按 100 计
			CreatedAt: time.Now(),
		})
		slog.Info("windsurf: account registered", "email_masked", maskEmail(email))
	}
	return nil
}

// preRegisterIfLow 额度≤阈值时后台预注册 1 个新号（防抖：同一时刻只一次）。
func (v *Vendor) preRegisterIfLow() {
	if v.cfg.Registrar == nil || v.cfg.Mailbox == nil {
		return
	}
	if v.pool.quotaMin() > v.cfg.QuotaThreshold {
		return
	}
	ready := make(chan struct{}, 1)
	select {
	case ready <- struct{}{}:
	default:
		return // 已有一次进行中
	}
	go func() {
		defer func() { <-ready }()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := v.registerNew(ctx, 1); err != nil {
			slog.Warn("windsurf: pre-register failed", "error", err)
		}
	}()
}

// PoolStatus 实现 contract.PoolVendor。
func (v *Vendor) PoolStatus() contract.PoolStatus {
	return v.pool.status(time.Now())
}

// Acquire 实现 contract.PoolVendor：借出一个健康账号。
func (v *Vendor) Acquire() (contract.AcctID, error) {
	a, err := v.pool.acquire(time.Now())
	if err != nil {
		return "", err
	}
	return contract.AcctID(a.Email), nil
}

// Release 实现 contract.PoolVendor：归还账号（可在 issue 中标记耗尽）。
func (v *Vendor) Release(id contract.AcctID) {
	v.pool.release(string(id), time.Now(), false)
}

// markExhausted 额度耗尽/故障换号路径内部使用。
func (v *Vendor) markExhausted(id contract.AcctID) {
	v.pool.release(string(id), time.Now(), true)
	v.preRegisterIfLow()
}

// ---------------------------------------------------------------------------
// 聊天（借号 → 上游 → 还号/换号）
// ---------------------------------------------------------------------------

// Chat 实现 contract.Vendor（非流式）。
// 流程：EnsureReady → 借号 → 上游请求 → 成功记账 / 可切换错误换下一账号（至多 2 次）。
func (v *Vendor) Chat(ctx context.Context, msg *contract.Message) (*contract.Reply, error) {
	v.pool.mu.Lock()
	chatter := v.cfg.Chatter
	v.pool.mu.Unlock()
	if chatter == nil {
		return nil, ErrNotWiredChat
	}

	acct, err := v.Acquire()
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		token := v.pool.tokenOf(string(acct))
		if token == "" {
			v.pool.release(string(acct), time.Now(), true)
			return nil, errors.New("windsurf: account 无会话令牌")
		}
		reply, err := chatter.DoChat(ctx, token, msg)
		if err == nil && reply != nil && reply.Status >= 200 && reply.Status < 300 {
			v.pool.touch(string(acct), time.Now())
			v.mu.Lock()
			v.lastSuccess = time.Now()
			v.lastErr = ""
			v.mu.Unlock()
			v.preUsageRefresh(string(acct))
			return reply, nil
		}
		// 失败：可切换（429/quota/capacity/传输错）→ 换号重试一次；否则记录冷却返回。
		v.pool.release(string(acct), time.Now(), true)
		status := 0
		if reply != nil {
			status = reply.Status
		}
		if attempt == 0 && shouldSwitch(status, err) {
			acct, err = v.Acquire()
			if err != nil {
				return reply, err
			}
			continue
		}
		v.mu.Lock()
		if err != nil {
			v.lastErr = err.Error()
		}
		v.mu.Unlock()
		if reply != nil {
			if err == nil {
				err = fmt.Errorf("windsurf: upstream error (status %d)", status)
			}
			return reply, err
		}
		return nil, err
	}
	return nil, ErrNoAccount
}

// ChatStream 实现 contract.Vendor（流式）。Connect-RPC 流式接缝同 Chat。
func (v *Vendor) ChatStream(ctx context.Context, msg *contract.Message) (*contract.Stream, error) {
	v.pool.mu.Lock()
	chatter := v.cfg.Chatter
	v.pool.mu.Unlock()
	if chatter == nil {
		return nil, ErrNotWiredChat
	}
	acct, err := v.Acquire()
	if err != nil {
		return nil, err
	}
	token := v.pool.tokenOf(string(acct))
	if token == "" {
		v.pool.release(string(acct), time.Now(), true)
		return nil, errors.New("windsurf: account 无会话令牌")
	}
	stream, err := chatter.DoChatStream(ctx, token, msg)
	if err != nil || stream == nil {
		v.pool.release(string(acct), time.Now(), true)
		if stream != nil {
			return stream, err
		}
		return nil, err
	}
	v.pool.touch(string(acct), time.Now())
	v.preRegisterIfLow()
	return stream, nil
}

// SetPoolUsage 供上游用量刷新回写（P3-B 经 GetUserStatus 调用）。
func (v *Vendor) SetPoolUsage(email string, daily, weekly float64) {
	v.pool.updateUsage(email, daily, weekly)
}

func (v *Vendor) preUsageRefresh(email string) {
	// P3-B：异步 GetUserStatus 回写额度；当前为空实现（池行为不受影响）。
}

// shouldSwitch 判定账号级失败是否值得换号（传输错误 / 可切换状态码）。
func shouldSwitch(status int, err error) bool {
	if err != nil {
		return true
	}
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return status >= 500 && status < 600
}
