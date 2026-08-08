package connect

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeRT 记录请求体并回放预设 Connect 帧（不触网）。
type fakeRT struct {
	reqBody  []byte
	respBody []byte
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	f.reqBody = b
	return &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(f.respBody)),
		Request:    req,
	}, nil
}

func TestDoChatParsesFrame(t *testing.T) {
	// 构造响应：两个数据帧 content 分别 "hello"、" world" + endStream "{}"
	d1 := EncodeString(fieldContent, "hello")
	d2 := EncodeString(fieldContent, " world")
	var body []byte
	body = append(append(body, wrapEnvelope(d1)...), wrapEnvelope(d2)...)
	body = append(body, wrapEnvelope([]byte("{}"))...)

	rt := &fakeRT{respBody: body}
	c := NewClient(&http.Client{Transport: rt})

	res, err := c.DoChat(context.Background(), "tok-sess", []ChatMessage{{Role: "user", Content: "hi"}}, "swe-1-6-slow", nil, nil)
	if err != nil {
		t.Fatalf("DoChat: %v", err)
	}
	if res.Content != "hello world" {
		t.Fatalf("content = %q, want 'hello world'", res.Content)
	}
	if res.FinishReason != "stop" {
		t.Fatalf("finish = %q", res.FinishReason)
	}
	// 请求帧未压缩且带头部认证
	if len(rt.reqBody) < 5 || rt.reqBody[0] != 0 {
		t.Fatalf("request frame flags = %02x (want uncompressed 0)", rt.reqBody[0])
	}
	authOK := false
	// 重放请求以检查认证头
	if !strings.Contains(string(rt.reqBody), "tok-sess") {
		t.Fatalf("request body should embed token")
	}
	_ = authOK
}

func TestStreamSSE(t *testing.T) {
	// 帧：content "a"，content "b" + finish=0 → end frame
	var body []byte
	body = append(body, wrapEnvelope(EncodeString(fieldContent, "a"))...)
	fin := EncodeVarintField(fieldFinish, 0)
	body = append(body, wrapEnvelope(append(EncodeString(fieldContent, "b"), fin...))...)
	body = append(body, frameWithFlags(0x02, []byte("{}"))...) // end_stream 帧（flags bit1）

	rt := &fakeRT{respBody: body}
	c := NewClient(&http.Client{Transport: rt})
	rc, err := c.StreamSSE(context.Background(), "tok-x", []ChatMessage{{Role: "user", Content: "hi"}}, "swe-1-6", nil, nil)
	if err != nil {
		t.Fatalf("StreamSSE: %v", err)
	}
	defer rc.Close()
	all, _ := io.ReadAll(rc)
	sse := string(all)
	if !strings.Contains(sse, "\"a\"") && !strings.Contains(sse, "\"b\"") {
		t.Fatalf("SSE missing content: %s", sse)
	}
	if !strings.Contains(sse, "\"finish_reason\"") {
		t.Fatalf("SSE missing finish_reason: %s", sse)
	}
	if !strings.Contains(sse, "DONE") {
		t.Fatalf("SSE missing [DONE]: %s", sse)
	}
}

// frameWithFlags 构造带指定 flags 的 Connect 帧。
func frameWithFlags(flags byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flags
	out[1] = byte(len(payload) >> 24)
	out[2] = byte(len(payload) >> 16)
	out[3] = byte(len(payload) >> 8)
	out[4] = byte(len(payload))
	copy(out[5:], payload)
	return out
}