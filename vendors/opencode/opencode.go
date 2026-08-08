// Package opencode 实现 OpenCode 厂商的"数据线束"（contract.Vendor）。
//
// 迁移自 package main 中的 opencode 硬编码逻辑（session / 模型目录 / 上游调用语义）。
// 本包不含代理池与 HTTP 客户端——通过 contract.Transport 由 core（网关）注入，
// 代理池/健康维护在 core/gateway 侧；厂商只负责"构造请求 + 解释响应 + 上游语义"。
package opencode

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// 上游端点（OpenCode 专属）。
const (
	zenModelsURL = "https://opencode.ai/zen/v1/models"
	goModelsURL  = "https://opencode.ai/zen/go/v1/models"
	versionURL   = "https://registry.npmjs.org/opencode-ai/latest"
	versionDef   = "1.15.3"

	// surfaceZen / surfaceGo 是 contract.Model.Meta 中 "surface" 键的取值，
	// 用于保留 zen 目录与 go 目录的区分（路由/目录过滤需要）。
	surfaceZen = "zen"
	surfaceGo  = "go"
)

// Options 键：core→厂商传递归一化请求时使用（见 contract.Message.Options）。
const (
	// KeyRawBody 存放已由 core/protocol 归一化的 OpenAI Chat 请求体（[]byte），Chat 阶段使用。
	KeyRawBody = "_oc_raw_body"
	// KeyAuthMode 存放认证路由模式："public"|"auto"|"zen"|"go"。
	KeyAuthMode = "_oc_auth_mode"
	// KeyAuthToken 存放透传密钥（不含前缀；public 为空）。
	KeyAuthToken = "_oc_auth_token"
)

// Config 是厂商装配配置（由 core 提供）。
type Config struct {
	ID   string // 厂商标识（默认 "opencode"）
	Name string // 展示名（默认 "OpenCode"）
	// Transport 由 core（网关）注入的 HTTP 传输（含代理池）。nil 时用 contract.DirectTransport。
	Transport contract.Transport
	// AdminPassword 本地门禁密钥：客户端用它修复应视为 public（免费）而非透传付费 key。
	AdminPassword string
}

// Vendor 实现 contract.Vendor，代表 OpenCode 上游。
type Vendor struct {
	cfg Config
	tr  contract.Transport

	// 会话状态（原全局 ocSession* 收敛为实例字段）
	ocSessionID string
	ocProjectID string
	ocClientVer string
	ocOnce      sync.Once

	// 模型目录缓存
	modelMu  sync.RWMutex
	cacheAll []contract.Model // ListModels 合并结果
}

// New 构造 OpenCode 厂商。
func New(cfg Config) *Vendor {
	if cfg.ID == "" {
		cfg.ID = "opencode"
	}
	if cfg.Name == "" {
		cfg.Name = "OpenCode"
	}
	return &Vendor{cfg: cfg}
}

// ---------------------------------------------------------------------------
// contract.Vendor 基础接口
// ---------------------------------------------------------------------------

// ID 实现 contract.Vendor。
func (v *Vendor) ID() string { return v.cfg.ID }

// Name 实现 contract.Vendor。
func (v *Vendor) Name() string { return v.cfg.Name }

// transport 返回注入的传输层，未注入时退化为直连。
func (v *Vendor) transport() contract.Transport {
	if v.tr == nil {
		if v.cfg.Transport != nil {
			v.tr = v.cfg.Transport
		} else {
			v.tr = contract.DirectTransport{}
		}
	}
	return v.tr
}

// sessionID 保证会话已初始化并返回当前 session id。
func (v *Vendor) sessionID() string {
	v.ocOnce.Do(func() {
		v.ocClientVer = v.fetchOCVersion()
		v.ocSessionID = "ses_" + randomString(24)
		v.ocProjectID = randomHex(40)
		slog.Info("opencode session initialized", "version", v.ocClientVer, "session_id", v.ocSessionID)
	})
	return v.ocSessionID
}

// refreshOCSession 强制刷新会话（供管理端/401 恢复调用）。
func (v *Vendor) refreshOCSession() {
	v.ocClientVer = v.fetchOCVersion()
	v.ocSessionID = "ses_" + randomString(24)
	v.ocProjectID = randomHex(40)
	slog.Info("opencode session refreshed", "version", v.ocClientVer, "session_id", v.ocSessionID)
	v.ocOnce = sync.Once{}
}

func (v *Vendor) fetchOCVersion() string {
	req, err := http.NewRequest("GET", versionURL, nil)
	if err != nil {
		return versionDef
	}
	req.Header.Set("Accept", "application/json")
	client, _ := v.transport().Client(contract.TierFree, false)
	resp, err := client.Do(req)
	if err != nil {
		return versionDef
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var info struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &info) == nil && info.Version != "" {
		return info.Version
	}
	return versionDef
}

// ListModels 实现 contract.Vendor：拉取 zen + go 两个目录，返回合并列表。
func (v *Vendor) ListModels(ctx context.Context) ([]contract.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, _ := v.transport().Client(contract.TierFree, false)
	var out []contract.Model
	for _, ep := range []struct{ url, surface string }{
		{zenModelsURL, surfaceZen},
		{goModelsURL, surfaceGo},
	} {
		req, err := http.NewRequestWithContext(ctx, "GET", ep.url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer public")
		req.Header.Set("x-opencode-session", v.sessionID())
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		var cat struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &cat) != nil {
			continue
		}
		for _, m := range cat.Data {
			out = append(out, contract.Model{
				ID:       m.ID,
				Provider: v.cfg.ID,
				Free:     v.isFree(m.ID),
				Meta:     map[string]string{"surface": ep.surface},
			})
		}
	}
	return out, nil
}

// Cache 返回最近一次 ListModels 的结果（core 聚合用；未拉取则自动拉一次）。
func (v *Vendor) Cache(ctx context.Context) []contract.Model {
	v.modelMu.RLock()
	cached := v.cacheAll
	v.modelMu.RUnlock()
	if len(cached) > 0 {
		return cached
	}
	all, err := v.ListModels(ctx)
	if err != nil {
		return cached
	}
	v.modelMu.Lock()
	v.cacheAll = all
	v.modelMu.Unlock()
	return all
}

// IsFree 实现 contract.Vendor：沿用既有规则（-free 后缀 / big-pickle）。
func (v *Vendor) IsFree(modelID string) bool {
	return v.isFree(modelID)
}

func (v *Vendor) isFree(modelID string) bool {
	return strings.Contains(strings.ToLower(modelID), "-free") || strings.EqualFold(modelID, "big-pickle")
}

// ErrSemantics 实现 contract.Vendor：opencode 的可重试/可切换/坏账状态码。
func (v *Vendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{
		Retryable:  []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
		Switchable: []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable},
		BadPool:    []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusServiceUnavailable},
	}
}

// Health 实现 contract.Vendor（前置阶段：常绿；P3 接入真实探测）。
func (v *Vendor) Health() contract.VendorHealth {
	return contract.VendorHealth{Available: true}
}

// Auth 实现 contract.Vendor：根据入站请求构造厂商对上游的认证头。
// 门禁（本层密钥校验）由 core/gateway 负责；这里解析的是客户端想要的"上游模式"：
//   - 无头 / Bearer public / 占位 key → Bearer public
//   - 其它 → Bearer <token>
//
// 更细的 go:/zen: 前缀路由在后续 Chat 切流阶段转移到内部 auth 判定（保持现状行为）。
func (v *Vendor) Auth(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return "Bearer public"
	}
	token := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
	if token == "" || token == "public" {
		return "Bearer public"
	}
	return "Bearer " + token
}

// ---------------------------------------------------------------------------
// 会话初始化钩子（测试/管理端用）
// ---------------------------------------------------------------------------

// SetSessionForTest 预置会话并消费 once，跳过版本探测（测试用）。
func (v *Vendor) SetSessionForTest(ver, sid, pid string) {
	v.ocClientVer = ver
	v.ocSessionID = sid
	v.ocProjectID = pid
	v.ocOnce.Do(func() {})
}

// SetCatalog 注入模型目录缓存（core/aggregator 刷新后回填；也供测试）。
func (v *Vendor) SetCatalog(models []contract.Model) {
	v.modelMu.Lock()
	v.cacheAll = append([]contract.Model(nil), models...)
	v.modelMu.Unlock()
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// randomString 生成 n 位小写字母+数字随机串。
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = letters[b[i]%byte(len(letters))]
	}
	return string(b)
}

// randomHex 生成 n 位十六进制随机串。
func randomHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = hex[b[i]%byte(len(hex))]
	}
	return string(b)
}
