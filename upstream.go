// 上游调用适配层（P2-B3 切流后）。
//
// callOpenCodeAPI / callOpenCodeAPIStream 签名保持不变（handler / 测试 / 网关续写
// 均不感知），内部桥接到全局 OpenCode 厂商（vendors/opencode，实现 contract.Vendor）。
// 传输层经 rootTransport 复用既有 SOCKS5 池/健康/冷却逻辑。
package main

import (
	"context"
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

// callOpenCodeAPI 非流式上游调用（适配层，签名与历史一致）。
func callOpenCodeAPI(upstreamBody []byte, modelID string, auth UpstreamAuth) ([]byte, int, http.Header, string, error) {
	v := mainCodeVendor()
	syncVendorState(v)
	msg := &contract.Message{
		Model: modelID,
		Options: map[string]any{
			opencode.KeyRawBody:    upstreamBody,
			opencode.KeyAuthMode:   modeName(auth.Mode),
			opencode.KeyAuthToken:  auth.Token,
			opencode.KeyMaxRetries: maxRouteRetries(),
		},
	}
	reply, err := v.Chat(context.Background(), msg)
	if reply == nil {
		return nil, 0, nil, "", err
	}
	return reply.Body, reply.Status, reply.Headers, reply.NodeAddr, err
}

// callOpenCodeAPIStream 流式上游调用（适配层，签名与历史一致）。
func callOpenCodeAPIStream(upstreamBody []byte, modelID string, auth UpstreamAuth) (io.ReadCloser, int, http.Header, string, error) {
	v := mainCodeVendor()
	syncVendorState(v)
	msg := &contract.Message{
		Model: modelID,
		Options: map[string]any{
			opencode.KeyRawBody:    upstreamBody,
			opencode.KeyAuthMode:   modeName(auth.Mode),
			opencode.KeyAuthToken:  auth.Token,
			opencode.KeyMaxRetries: maxRouteRetries(),
		},
	}
	stream, err := v.ChatStream(context.Background(), msg)
	if stream == nil {
		return nil, 0, nil, "", err
	}
	return stream.ReadCloser, stream.Status, nil, stream.NodeAddr, err
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
