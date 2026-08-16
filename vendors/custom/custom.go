// Package custom 自定义模型源：用户自带 key 的第三方供应商适配器。
//
// 形态：一个实例 = 一个用户自定义源（config providers[] 里一条 type:"custom" 条目，
// id 由用户命名），装配参数经 ProviderSpec.Params 注入：
//
//	base_url  上游根地址（如 https://open.bigmodel.cn/api/paas/v4、
//	          https://api.anthropic.com/v1、https://generativelanguage.googleapis.com/v1beta）
//	api_key   上游密钥（由网关持有，客户端无需携带）
//	protocol  出站协议："openai"（默认，OpenAI 兼容）| "anthropic" | "gemini"
//	via_proxy 出站是否走代理池（默认 false 直连；true 时复用节点池出口）
//
// 模型命名：目录恒带 "{id}/" 前缀（如 "myglm/glm-4.7"），与其它厂商同名模型
// 天然隔离、路由与展示稳定；调用上游前剥掉前缀。请求经统一网关发出（Transport 注入），
// 统计/调用日志/失败切换由 core 既有链路自动覆盖。
package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// Params 键（ProviderSpec.Params；配置 providers[].params 同名透传）。
const (
	ParamBaseURL  = "base_url"
	ParamAPIKey   = "api_key"
	ParamProtocol = "protocol"
	ParamViaProxy = "via_proxy"
)

// 协议取值。
const (
	ProtoOpenAI    = "openai"
	ProtoAnthropic = "anthropic"
	ProtoGemini    = "gemini"
)

// keyRawBody 与 core 适配层（upstream.go chatViaVendor）注入的原始 OpenAI 请求体
// Extra 键同值。原始体保留 tools/options 等完整字段，优于归一化 Messages 重建。
const keyRawBody = "_oc_raw_body"

// Config 构造参数。
type Config struct {
	ID        string // 实例标识（用户自定义，模型前缀即它）
	Name      string // 展示名
	BaseURL   string // 上游根地址（尾斜杠容忍）
	APIKey    string
	Protocol  string // openai | anthropic | gemini
	ViaProxy  bool   // 出站走代理池（TierFree）；默认直连（TierPaid）
	Transport contract.Transport
}

// Vendor 自定义模型源厂商。
type Vendor struct {
	cfg         Config
	proto       chatProto
	mu          sync.Mutex
	models      []contract.Model // 最近一次成功目录（失败时兜底返回）
	lastErr     string
	lastSuccess time.Time
}

// New 构造自定义模型源厂商。
func New(cfg Config) (*Vendor, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("custom: id is required")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("custom %s: base_url is required", cfg.ID)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Protocol == "" {
		cfg.Protocol = ProtoOpenAI
	}
	var p chatProto
	switch cfg.Protocol {
	case ProtoOpenAI:
		p = &openaiProto{}
	case ProtoAnthropic:
		p = &anthropicProto{}
	case ProtoGemini:
		p = &geminiProto{}
	default:
		return nil, fmt.Errorf("custom %s: unknown protocol %q (want openai|anthropic|gemini)", cfg.ID, cfg.Protocol)
	}
	if cfg.Transport == nil {
		cfg.Transport = contract.DirectTransport{}
	}
	return &Vendor{cfg: cfg, proto: p}, nil
}

// ---------------------------------------------------------------------------
// contract.Vendor
// ---------------------------------------------------------------------------

func (v *Vendor) ID() string   { return v.cfg.ID }
func (v *Vendor) Name() string { return v.cfg.Name }

func (v *Vendor) tier() contract.Tier {
	if v.cfg.ViaProxy {
		return contract.TierFree
	}
	return contract.TierPaid
}

// prefix 模型目录前缀（"{id}/"）。
func (v *Vendor) prefix() string { return v.cfg.ID + "/" }

// upstreamModel 把对外模型名（带前缀）还原为上游真实模型名。
// 无前缀时原样返回（经 routing.model_provider_map 强制映射的裸名场景）。
func (v *Vendor) upstreamModel(model string) string {
	return strings.TrimPrefix(model, v.prefix())
}

// prefixedModel 给上游模型名加本源前缀。
func (v *Vendor) prefixedModel(upstream string) string { return v.prefix() + upstream }

// ListModels 拉取上游目录并加前缀。失败时回退最近一次成功缓存。
func (v *Vendor) ListModels(ctx context.Context) ([]contract.Model, error) {
	ids, err := v.proto.listModels(ctx, v)
	if err != nil {
		v.mu.Lock()
		cached := append([]contract.Model(nil), v.models...)
		v.mu.Unlock()
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	out := make([]contract.Model, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		out = append(out, contract.Model{
			ID:       v.prefixedModel(id),
			Provider: v.cfg.ID,
			// key 由网关持有，客户端不带 key 也可调用 → 对外即"免费可用"目录。
			Free: true,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("custom %s: empty model list", v.cfg.ID)
	}
	v.mu.Lock()
	v.models = out
	v.mu.Unlock()
	return out, nil
}

// IsFree 自定义源模型恒可用（key 在网关侧），返回 true。
func (v *Vendor) IsFree(string) bool { return true }

// ErrSemantics 通用语义：瞬时错误可重试/可切换厂商；401/403（key 失效）可切换
// （同名模型或存在其它候选时接手），不进坏池（与代理池健康无关）。
func (v *Vendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{
		Retryable:  []int{http.StatusRequestTimeout, http.StatusTooManyRequests, 500, 502, 503, 504},
		Switchable: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooManyRequests, 500, 502, 503, 504},
	}
}

// Auth 自定义源用配置 key，认证头在协议层构造；入站 key 不透传。
func (v *Vendor) Auth(*http.Request) string { return "Bearer " + v.cfg.APIKey }

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

func (v *Vendor) markErr(err string) {
	v.mu.Lock()
	v.lastErr = err
	v.mu.Unlock()
}

func (v *Vendor) markOK() {
	v.mu.Lock()
	v.lastErr = ""
	v.lastSuccess = time.Now()
	v.mu.Unlock()
}

// Chat 非流式：原始 OpenAI 请求体 → 上游协议适配 → OpenAI 形态响应。
func (v *Vendor) Chat(ctx context.Context, msg *contract.Message) (*contract.Reply, error) {
	body, err := v.buildBody(msg, false)
	if err != nil {
		return nil, err
	}
	return v.proto.chat(ctx, v, v.upstreamModel(msg.Model), body)
}

// ChatStream 流式：返回 OpenAI Chat 形态 SSE（协议层负责原生流转换）。
func (v *Vendor) ChatStream(ctx context.Context, msg *contract.Message) (*contract.Stream, error) {
	body, err := v.buildBody(msg, true)
	if err != nil {
		return nil, err
	}
	return v.proto.chatStream(ctx, v, v.upstreamModel(msg.Model), body)
}

// buildBody 取 Extra 里的原始 OpenAI 请求体，改写 model/stream 后交协议层。
// Extra 缺失（独立调用/测试）时从归一化 Messages 重建最小请求体。
func (v *Vendor) buildBody(msg *contract.Message, stream bool) ([]byte, error) {
	var m map[string]any
	if raw, _ := msg.Extra[keyRawBody].([]byte); len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("custom %s: bad raw body: %w", v.cfg.ID, err)
		}
	}
	if m == nil {
		m = map[string]any{}
		if len(msg.Messages) > 0 {
			msgs := make([]any, 0, len(msg.Messages))
			for _, mm := range msg.Messages {
				msgs = append(msgs, map[string]any{"role": mm.Role, "content": mm.Content})
			}
			m["messages"] = msgs
		}
	}
	m["model"] = v.upstreamModel(msg.Model)
	m["stream"] = stream
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("custom %s: marshal body: %w", v.cfg.ID, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 协议层共享 HTTP
// ---------------------------------------------------------------------------

// chatProto 单个出站协议的适配器：统一 OpenAI 形态 ⇄ 厂商原生。
type chatProto interface {
	// listModels 拉取上游模型目录（原始 ID，不带本源前缀）。
	listModels(ctx context.Context, v *Vendor) ([]string, error)
	// chat 非流式调用；rawBody 为 OpenAI Chat 请求体，返回 OpenAI Chat 响应体。
	chat(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Reply, error)
	// chatStream 流式调用；返回 OpenAI Chat 形态 SSE。
	chatStream(ctx context.Context, v *Vendor, model string, rawBody []byte) (*contract.Stream, error)
}

// do 经统一网关 Transport 发出请求（直连或代理池由配置决定），回传出口节点地址。
// streaming=true 时用无总超时客户端（长推理流不被切断）。
func (v *Vendor) do(ctx context.Context, method, url string, headers map[string]string, body []byte, streaming bool) (*http.Response, string, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, "", err
	}
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	client, addr := v.cfg.Transport.Client(v.tier(), streaming)
	resp, err := client.Do(req)
	if err != nil {
		if addr != "" {
			v.cfg.Transport.Mark(addr, 0, err)
		}
		v.markErr(err.Error())
		return nil, addr, err
	}
	if addr != "" {
		v.cfg.Transport.Mark(addr, resp.StatusCode, nil)
	}
	return resp, addr, nil
}

// readBody 读完并关闭响应体。
func readBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	return b
}

// nopCloser 把已读出的字节包成 ReadCloser（错误体透传用）。
type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

var _ io.ReadCloser = nopCloser{}
