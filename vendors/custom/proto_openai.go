// OpenAI 兼容协议出站适配：近乎透传（contract 统一形态即 OpenAI Chat），
// 仅改写 model 字段与认证头；响应/SSE 原样回传。
package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

type openaiProto struct{}

func (openaiProto) headers(key string, stream bool) map[string]string {
	h := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + key,
	}
	if stream {
		h["Accept"] = "text/event-stream"
	}
	return h
}

func (openaiProto) listModels(ctx context.Context, v *Vendor, key string) ([]string, error) {
	resp, _, err := v.do(ctx, http.MethodGet, v.cfg.BaseURL+"/models",
		map[string]string{"Authorization": "Bearer " + key}, nil, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("list models: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return nil, &keyStatusError{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("custom %s: bad models response: %w", v.cfg.ID, err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func (p openaiProto) chat(ctx context.Context, v *Vendor, model, key string, rawBody []byte) (*contract.Reply, error) {
	resp, addr, err := v.do(ctx, http.MethodPost, v.cfg.BaseURL+"/chat/completions",
		p.headers(key, false), rawBody, false)
	if err != nil {
		return nil, err
	}
	body := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		v.markErr(fmt.Sprintf("chat: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
	} else {
		v.markOK()
	}
	return &contract.Reply{Body: body, Status: resp.StatusCode, NodeAddr: addr, Headers: resp.Header}, nil
}

func (p openaiProto) chatStream(ctx context.Context, v *Vendor, model, key string, rawBody []byte) (*contract.Stream, error) {
	resp, addr, err := v.do(ctx, http.MethodPost, v.cfg.BaseURL+"/chat/completions",
		p.headers(key, true), rawBody, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := readBody(resp)
		v.markErr(fmt.Sprintf("chat stream: HTTP %d: %s", resp.StatusCode, truncateErr(body)))
		return &contract.Stream{ReadCloser: nopCloser{bytes.NewReader(body)}, Status: resp.StatusCode, NodeAddr: addr}, nil
	}
	v.markOK()
	return &contract.Stream{ReadCloser: resp.Body, Status: resp.StatusCode, NodeAddr: addr}, nil
}

// truncateErr 错误体摘要（日志/Health 用，防大段 HTML 刷屏）。
func truncateErr(b []byte) string {
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
