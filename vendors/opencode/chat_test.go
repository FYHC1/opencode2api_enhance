package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// fakeRT 记录请求并回放预设响应（不触网）。
type fakeRT struct {
	mu        sync.Mutex
	responses []fakeResp
	urls      []string
	auths     []string
	payloads  []map[string]any
}

type fakeResp struct {
	status int
	body   string
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	payload := map[string]any{}
	if req.Body != nil {
		_ = json.NewDecoder(req.Body).Decode(&payload)
	}
	f.urls = append(f.urls, req.URL.String())
	f.auths = append(f.auths, req.Header.Get("Authorization"))
	f.payloads = append(f.payloads, payload)

	status := http.StatusInternalServerError
	body := `{"error":"no responses left"}`
	if len(f.responses) > 0 {
		next := f.responses[0]
		f.responses = f.responses[1:]
		status = next.status
		body = next.body
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// fakeContractTransport 实现 contract.Transport。
type fakeContractTransport struct {
	rt        *fakeRT
	proxyAddr string
	streaming bool
	marks     []string
	mu        sync.Mutex
}

func (t *fakeContractTransport) Client(_ contract.Tier, streaming bool) (*http.Client, string) {
	t.mu.Lock()
	t.streaming = streaming
	t.mu.Unlock()
	return &http.Client{Transport: t.rt}, t.proxyAddr
}

func (t *fakeContractTransport) Mark(proxyAddr string, status int, _ error) {
	t.mu.Lock()
	t.marks = append(t.marks, fmt.Sprintf("%s:%d", proxyAddr, status))
	t.mu.Unlock()
}

// newTestVendor 构造带假传输的厂商，并预置会话（跳过版本探测）。
func newTestVendor(rt *fakeRT, addr string) *Vendor {
	v := New(Config{Transport: &fakeContractTransport{rt: rt, proxyAddr: addr}})
	v.SetSession("1.15.3", "ses_test", "proj_test")
	return v
}

func msgWith(raw, model, mode, token string) *contract.Message {
	return &contract.Message{
		Model: model,
		Options: map[string]any{
			KeyRawBody:   []byte(raw),
			KeyAuthMode:  mode,
			KeyAuthToken: token,
		},
	}
}

func TestChatPublicUsesZenURLAndBody(t *testing.T) {
	rt := &fakeRT{responses: []fakeResp{{status: 200, body: `{"id":"z","object":"chat.completion","choices":[]}`}}}
	v := newTestVendor(rt, "n1")

	raw := `{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`
	reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 {
		t.Fatalf("status = %d", reply.Status)
	}
	if reply.NodeAddr != "n1" {
		t.Fatalf("node addr = %q", reply.NodeAddr)
	}
	if len(rt.urls) != 1 || !strings.HasSuffix(rt.urls[0], "/zen/v1/chat/completions") {
		t.Fatalf("url = %v, want zen endpoint", rt.urls)
	}
	if len(rt.auths) != 1 || rt.auths[0] != "Bearer public" {
		t.Fatalf("auth = %v, want Bearer public", rt.auths)
	}
}

func TestChatGoModeUsesGoEndpoint(t *testing.T) {
	rt := &fakeRT{responses: []fakeResp{{status: 200, body: `{"id":"g","object":"chat.completion","choices":[]}`}}}
	v := newTestVendor(rt, "n2")
	v.SetCatalog([]contract.Model{
		{ID: "kimi-go", Provider: "opencode", Meta: map[string]string{"surface": "go"}},
	})

	raw := `{"model":"kimi-go","messages":[]}`
	reply, err := v.Chat(context.Background(), msgWith(raw, "kimi-go", "go", "sk-gokey"))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 {
		t.Fatalf("status = %d", reply.Status)
	}
	if len(rt.urls) != 1 || !strings.HasSuffix(rt.urls[0], "/zen/go/v1/chat/completions") {
		t.Fatalf("url = %v, want go endpoint", rt.urls)
	}
	if len(rt.auths) != 1 || rt.auths[0] != "Bearer sk-gokey" {
		t.Fatalf("auth = %v, want Bearer sk-gokey", rt.auths)
	}
}

func TestChatAuthModesRouteEndpoints(t *testing.T) {
	rt := &fakeRT{responses: []fakeResp{{status: 200, body: `{"id":"r","object":"chat.completion","choices":[]}`}}}
	v := newTestVendor(rt, "n5")
	v.SetCatalog([]contract.Model{
		{ID: "m-shared", Provider: "opencode", Meta: map[string]string{"surface": "zen"}},
		{ID: "m-shared", Provider: "opencode", Meta: map[string]string{"surface": "go"}},
		{ID: "m-goonly", Provider: "opencode", Meta: map[string]string{"surface": "go"}},
	})

	tests := []struct {
		name    string
		model   string
		mode    string
		wantURL string
	}{
		{"auto shared => zen", "m-shared", "auto", "/zen/v1/chat/completions"},
		{"auto go-only => go", "m-goonly", "auto", "/zen/go/v1/chat/completions"},
		{"go prefix shared => go", "m-shared", "go", "/zen/go/v1/chat/completions"},
		{"zen prefix forced => zen", "m-shared", "zen", "/zen/v1/chat/completions"},
		{"public => zen", "m-shared", "public", "/zen/v1/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt.mu.Lock()
			rt.responses = []fakeResp{{status: 200, body: `{"id":"r","object":"chat.completion","choices":[]}`}}
			rt.mu.Unlock()

			raw := `{"model":"` + tt.model + `","messages":[]}`
			reply, err := v.Chat(context.Background(), msgWith(raw, tt.model, tt.mode, "sk-key"))
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if reply.Status != 200 {
				t.Fatalf("status = %d", reply.Status)
			}
			rt.mu.Lock()
			url := rt.urls[len(rt.urls)-1]
			rt.mu.Unlock()
			if !strings.HasSuffix(url, tt.wantURL) {
				t.Fatalf("url = %s, want suffix %s", url, tt.wantURL)
			}
		})
	}
}

func TestChatRetriesOn429ThenSucceeds(t *testing.T) {
	rt := &fakeRT{responses: []fakeResp{
		{status: 429, body: `{"error":"throttled"}`},
		{status: 200, body: `{"id":"ok","object":"chat.completion","choices":[]}`},
	}}
	v := newTestVendor(rt, "n3")
	tr := v.transport().(*fakeContractTransport)

	raw := `{"model":"m-free","messages":[]}`
	reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 {
		t.Fatalf("status = %d, want 200 after retry", reply.Status)
	}
	if len(rt.urls) != 2 {
		t.Fatalf("request count = %d, want 2 (retry)", len(rt.urls))
	}
	found := false
	for _, m := range tr.marks {
		if strings.HasSuffix(m, ":429") {
			found = true
		}
	}
	if !found {
		t.Fatalf("marks = %v, want a 429 mark", tr.marks)
	}
}

func TestChatConvertsAnthropicEnvelope(t *testing.T) {
	anthropicBody := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`
	rt := &fakeRT{responses: []fakeResp{{status: 200, body: anthropicBody}}}
	v := newTestVendor(rt, "n4")

	raw := `{"model":"m-free","messages":[]}`
	reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply.Body, &out); err != nil {
		t.Fatalf("reply body is not JSON: %v", err)
	}
	if out["object"] != "chat.completion" {
		t.Fatalf("object = %v, want chat.completion (Anthropic envelope converted)", out["object"])
	}
	if out["usage"].(map[string]any)["prompt_tokens"] == nil {
		t.Fatal("usage.prompt_tokens missing after conversion")
	}
}
