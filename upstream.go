// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

func buildOCRequest(modelID string, bodyMap map[string]any, auth UpstreamAuth) (*http.Request, error) {
	return buildOCRequestWithEndpoint(modelID, bodyMap, auth, auth.shouldUseGoEndpoint(modelID))
}

func buildOCRequestWithEndpoint(modelID string, bodyMap map[string]any, auth UpstreamAuth, useGoEndpoint bool) (*http.Request, error) {
	bodyMap["model"] = modelID
	delete(bodyMap, "reasoning_effort")
	tryBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	var upstreamURL string
	if useGoEndpoint {
		upstreamURL = "https://opencode.ai/zen/go/v1/chat/completions"
	} else {
		upstreamURL = "https://opencode.ai/zen/v1/chat/completions"
	}
	req, err := http.NewRequest("POST", upstreamURL, bytes.NewReader(tryBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth.authorizationHeader())
	req.Header.Set("User-Agent", fmt.Sprintf("opencode/%s", ocClientVer))
	req.Header.Set("x-opencode-client", "cli")
	req.Header.Set("x-opencode-project", ocProjectID)
	req.Header.Set("x-opencode-session", ocSessionID)
	req.Header.Set("x-opencode-request", "req_"+randomString(24))
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func shouldRetryUpstreamStatus(status int) bool {
	// 仅重试可恢复的临时性错误
	switch status {
	case http.StatusUnauthorized, // 401 认证过期或 token 未同步
		http.StatusTooManyRequests,    // 429 限流
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		return true
	}
	// 其他 5xx 也重试，但 4xx 中只有 401 和 429 重试
	return status >= 500 && status < 600
}

const (
	maxUpstreamRetries = 3
	max401Retries      = 3
)

func callOpenCodeAPI(upstreamBody []byte, modelID string, auth UpstreamAuth) ([]byte, int, http.Header, string, error) {
	initOCSession()

	var bodyMap map[string]any
	if err := json.Unmarshal(upstreamBody, &bodyMap); err != nil {
		return nil, 500, nil, "", fmt.Errorf("invalid request body")
	}
	useGoEndpoint := auth.shouldUseGoEndpoint(modelID)

	var lastErr error
	var retryCount int
	var retry401Count int
	var lastBody []byte
	var lastStatus int
	var lastHeader http.Header
	var lastProxyAddr string
	maxRetries := maxRouteRetries()
	for retryCount <= maxRetries {
		up, err := buildOCRequestWithEndpoint(modelID, bodyMap, auth, useGoEndpoint)
		if err != nil {
			lastErr = err
			break
		}
		client, proxyAddr := getHTTPClientForTierWithProxy(auth.tier())
		resp, err := client.Do(up)
		if err != nil {
			markSocks5Result(proxyAddr, 0, err)
			lastErr = err
			retryCount++
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			markSocks5Result(proxyAddr, resp.StatusCode, nil)
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, 0, nil, "", readErr
			}
			if isAnthropicFormat(b) {
				b = convertAnthropicToOpenAI(b, modelID)
			}
			return b, resp.StatusCode, resp.Header, proxyAddr, nil
		}
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		markSocks5Result(proxyAddr, resp.StatusCode, nil)
		slog.Error("upstream error", "model", modelID, "status", resp.StatusCode, "body", string(errBody))
		lastBody = errBody
		lastStatus = resp.StatusCode
		lastHeader = resp.Header
		lastProxyAddr = proxyAddr
		lastErr = fmt.Errorf("upstream error")
		if shouldRetryUpstreamStatus(resp.StatusCode) {
			client.CloseIdleConnections()
			if resp.StatusCode == http.StatusUnauthorized {
				retry401Count++
				if retry401Count >= max401Retries {
					break
				}
			} else {
				retryCount++
				if retryCount >= maxRetries {
					break
				}
			}
			continue
		}
		break
	}
	return lastBody, lastStatus, lastHeader, lastProxyAddr, lastErr
}

func callOpenCodeAPIStream(upstreamBody []byte, modelID string, auth UpstreamAuth) (io.ReadCloser, int, http.Header, string, error) {
	initOCSession()

	var bodyMap map[string]any
	if err := json.Unmarshal(upstreamBody, &bodyMap); err != nil {
		return nil, 500, nil, "", fmt.Errorf("invalid request body")
	}
	useGoEndpoint := auth.shouldUseGoEndpoint(modelID)

	var lastBody []byte
	var lastStatus int
	var lastHeader http.Header
	var lastProxyAddr string
	var retryCount int
	var retry401Count int
	maxRetries := maxRouteRetries()
	for retryCount <= maxRetries {
		up, err := buildOCRequestWithEndpoint(modelID, bodyMap, auth, useGoEndpoint)
		if err != nil {
			break
		}
		// SSE 流式请求用去总超时客户端，避免健康长推理流被 5 分钟人为切断
		client, proxyAddr := getStreamingHTTPClientForTierWithProxy(auth.tier())
		resp, err := client.Do(up)
		if err != nil {
			markSocks5Result(proxyAddr, 0, err)
			retryCount++
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			markSocks5Result(proxyAddr, resp.StatusCode, nil)
			return resp.Body, resp.StatusCode, resp.Header, proxyAddr, nil
		}
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		markSocks5Result(proxyAddr, resp.StatusCode, nil)
		slog.Error("upstream error", "model", modelID, "status", resp.StatusCode, "body", string(errBody))
		lastBody = errBody
		lastStatus = resp.StatusCode
		lastHeader = resp.Header
		lastProxyAddr = proxyAddr
		if shouldRetryUpstreamStatus(resp.StatusCode) {
			client.CloseIdleConnections()
			if resp.StatusCode == http.StatusUnauthorized {
				retry401Count++
				if retry401Count >= max401Retries {
					break
				}
			} else {
				retryCount++
				if retryCount >= maxRetries {
					break
				}
			}
			continue
		}
		// 不可重试的错误体供下游透传
		return io.NopCloser(bytes.NewReader(lastBody)), lastStatus, lastHeader, lastProxyAddr, nil
	}
	if lastStatus != 0 {
		return io.NopCloser(bytes.NewReader(lastBody)), lastStatus, lastHeader, lastProxyAddr, nil
	}
	return nil, 500, nil, "", fmt.Errorf("all models failed")
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

// ======================== Chat Completions Handler ========================
