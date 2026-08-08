// 上游调用适配层（P2-B3 切流后）。
//
// callOpenCodeAPI / callOpenCodeAPIStream 签名保持不变（handler / 测试 / 网关续写
// 均不感知），内部桥接到全局 OpenCode 厂商（vendors/opencode，实现 contract.Vendor）。
// 传输层经 rootTransport 复用既有 SOCKS5 池/健康/冷却逻辑。
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/vendors/opencode"
)

const (
	maxUpstreamRetries = 3
	max401Retries      = 3
)

var (
	ocAdapterOnce   sync.Once
	ocAdapterTarget *opencode.Vendor
)

// mainCodeVendor 返回全局 OpenCode 厂商（惰性装配，测试与生产共用）。
func mainCodeVendor() *opencode.Vendor {
	ocAdapterOnce.Do(func() {
		ocAdapterTarget = opencode.New(opencode.Config{
			ID:            "opencode",
			Name:          "OpenCode",
			Transport:     rootTransport{},
			AdminPassword: adminPassword,
		})
	})
	return ocAdapterTarget
}

// modeName 把本包认证路由模式映射为 vendor 侧字符串（public/auto/zen/go）。
func modeName(mode AuthRouteMode) string {
	switch mode {
	case AuthRoutePublic:
		return "public"
	case AuthRouteAuto:
		return "auto"
	case AuthRouteZen:
		return "zen"
	case AuthRouteGo:
		return "go"
	default:
		return "auto"
	}
}

// syncVendorState 把 main 侧的会话与会话缓存推给 vendor，保证：
//   - 测试注入的 ocSession* 与 modelsCache/goModelsCache 直接生效（fake httpClient 桥接不变）；
//   - 生产经 refreshModelCatalog 刷新的目录同样在 vendor 内可查（go 端点路由判定）。
func syncVendorState(v *opencode.Vendor) {
	initOCSession()
	v.SetSession(ocClientVer, ocSessionID, ocProjectID)

	modelMu.RLock()
	zen, goM := modelsCache, goModelsCache
	modelMu.RUnlock()
	all := make([]contract.Model, 0, len(zen)+len(goM))
	for _, m := range zen {
		all = append(all, contract.Model{ID: m.ID, Provider: "opencode", Free: isFreeModel(m.ID), Meta: map[string]string{"surface": "zen"}})
	}
	for _, m := range goM {
		all = append(all, contract.Model{ID: m.ID, Provider: "opencode", Free: isFreeModel(m.ID), Meta: map[string]string{"surface": "go"}})
	}
	v.SetCatalog(all)
}

// chatCandidates 返回可服务 modelID 的厂商（failover 顺序）。
// 未装配路由器（测试环境）时退化为单 opencode。
func chatCandidates(modelID string) []contract.Vendor {
	if chatRouterVar != nil {
		if cs := chatRouterVar.Candidates(modelID); len(cs) > 0 {
			return cs
		}
	}
	return []contract.Vendor{mainCodeVendor()}
}

// seedVendorCatalog 把 globalAgg 中属于该厂商的模型推给它（SetCatalog 可选实现）。
func seedVendorCatalog(v contract.Vendor) {
	if globalAgg == nil {
		return
	}
	seeder, ok := v.(interface{ SetCatalog([]contract.Model) })
	if !ok {
		return
	}
	var mine []contract.Model
	for _, m := range globalAgg.Catalog() {
		if m.Provider == v.ID() {
			mine = append(mine, m)
		}
	}
	seeder.SetCatalog(mine)
}

// chatViaVendor 经单个厂商发起非流式上游调用。
func chatViaVendor(v contract.Vendor, upstreamBody []byte, modelID string, auth UpstreamAuth) (*contract.Reply, error) {
	if oc, ok := v.(*opencode.Vendor); ok && oc == mainCodeVendor() {
		syncVendorState(oc)
	} else {
		seedVendorCatalog(v)
	}
	msg := &contract.Message{
		Model: modelID,
		// 归一化消息：非 opencode 厂商（如 windsurf 账号池）不读 KeyRawBody，
		// 走 Messages 字段；opencode 仍优先读 Options[KeyRawBody]，此字段对其无副作用。
		Messages: rawBodyToContractMessages(upstreamBody),
		Options: map[string]any{
			opencode.KeyRawBody:    upstreamBody,
			opencode.KeyAuthMode:   modeName(auth.Mode),
			opencode.KeyAuthToken:  auth.Token,
			opencode.KeyMaxRetries: maxRouteRetries(),
		},
	}
	return v.Chat(context.Background(), msg)
}

// chatViaVendorStream 构造单个厂商的流式上游调用。
func chatViaVendorStream(v contract.Vendor, upstreamBody []byte, modelID string, auth UpstreamAuth) (*contract.Stream, error) {
	if oc, ok := v.(*opencode.Vendor); ok && oc == mainCodeVendor() {
		syncVendorState(oc)
	} else {
		seedVendorCatalog(v)
	}
	msg := &contract.Message{
		Model: modelID,
		// 归一化厂商（windsurf 池型）读 Messages 字段；opencode 仍走 Options[KeyRawBody]。
		Messages: rawBodyToContractMessages(upstreamBody),
		Options: map[string]any{
			opencode.KeyRawBody:    upstreamBody,
			opencode.KeyAuthMode:   modeName(auth.Mode),
			opencode.KeyAuthToken:  auth.Token,
			opencode.KeyMaxRetries: maxRouteRetries(),
		},
	}
	return v.ChatStream(context.Background(), msg)
}

// rawBodyToContractMessages 从 OpenAI Chat 形态请求体提取归一化消息。
// content 保持 string / 数组两种形态（与 contract.Msg.Content 语义一致），
// 供非 opencode 厂商（windsurf 等）经 contract.Message.Messages 读取。
func rawBodyToContractMessages(body []byte) []contract.Msg {
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	out := make([]contract.Msg, 0, len(req.Messages))
	for _, raw := range req.Messages {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		role, _ := m["role"].(string)
		if role == "" {
			continue
		}
		msg := contract.Msg{Role: role}
		switch c := m["content"].(type) {
		case string:
			msg.Content = c
		case []any:
			msg.Content = c
		}
		out = append(out, msg)
	}
	return out
}

// shouldSwitchVendor 判定某厂商的失败是否应切换下一个候选厂商。
// 有上游状态码 → 按厂商 Switchable 语义 + 5xx 判定；无状态码（传输错误）→ 切换。
func shouldSwitchVendor(v contract.Vendor, status int, _ error) bool {
	if status == 0 {
		return true
	}
	rules := v.ErrSemantics()
	for _, s := range rules.Switchable {
		if status == s {
			return true
		}
	}
	return status >= 500 && status < 600
}

// callOpenCodeAPI 非流式上游调用（适配层；路由 + 厂商级 failover）。
func callOpenCodeAPI(upstreamBody []byte, modelID string, auth UpstreamAuth) ([]byte, int, http.Header, string, error) {
	cands := chatCandidates(modelID)

	var lastReply *contract.Reply
	var lastErr error
	for i, v := range cands {
		reply, err := chatViaVendor(v, upstreamBody, modelID, auth)
		if err == nil && reply != nil && reply.Status >= 200 && reply.Status < 300 {
			return reply.Body, reply.Status, reply.Headers, reply.NodeAddr, nil
		}
		lastReply, lastErr = reply, err
		if i == len(cands)-1 {
			break
		}
		status := 0
		if reply != nil {
			status = reply.Status
		}
		if !shouldSwitchVendor(v, status, err) {
			break
		}
	}
	if lastReply == nil {
		return nil, 0, nil, "", lastErr
	}
	return lastReply.Body, lastReply.Status, lastReply.Headers, lastReply.NodeAddr, lastErr
}

// callOpenCodeAPIStream 流式上游调用（适配层；路由 + 厂商级 failover；签名与历史一致）。
func callOpenCodeAPIStream(upstreamBody []byte, modelID string, auth UpstreamAuth) (io.ReadCloser, int, http.Header, string, error) {
	cands := chatCandidates(modelID)

	var lastStream *contract.Stream
	var lastErr error
	for i, v := range cands {
		stream, err := chatViaVendorStream(v, upstreamBody, modelID, auth)
		if err == nil && stream != nil && stream.Status >= 200 && stream.Status < 300 {
			return stream.ReadCloser, stream.Status, nil, stream.NodeAddr, nil
		}
		lastStream, lastErr = stream, err
		if i == len(cands)-1 {
			break
		}
		status := 0
		if stream != nil {
			status = stream.Status
		}
		if !shouldSwitchVendor(v, status, err) {
			break
		}
	}
	if lastStream == nil {
		return nil, 0, nil, "", lastErr
	}
	return lastStream.ReadCloser, lastStream.Status, nil, lastStream.NodeAddr, lastErr
}

// ======================== 安全响应头过滤 ========================

var safeResponseHeaders = map[string]bool{
	"Content-Type":          true,
	"X-RateLimit-Limit":     true,
	"X-RateLimit-Remaining": true,
	"X-RateLimit-Reset":     true,
}

func filterResponseHeaders(h http.Header) http.Header {
	filtered := make(http.Header)
	for k, v := range h {
		if safeResponseHeaders[k] {
			filtered[k] = v
		}
	}
	return filtered
}
