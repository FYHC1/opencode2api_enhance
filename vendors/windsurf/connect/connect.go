// Connect-RPC 客户端（Devin/Windsurf）：POST /exa.api_server_pb.ApiServerService/GetChatMessage。
// Connect 帧：1 字节 flags（bit0=gzip、bit1=end_stream）+ 4 字节 BE length + payload。
package connect

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ClientName    = "chisel"
	ClientVersion = "2026.8.18"
	FreeTierModel = "swe-1-6-slow"

	fieldContent = uint32(3)
	fieldFinish  = uint32(5)
	fieldUsage   = uint32(7)
	fieldReason  = uint32(9)
)

var (
	hosts = []string{"server.codeium.com", "server.self-serve.windsurf.com"}
	path  = "/exa.api_server_pb.ApiServerService/GetChatMessage"
)

// ChatMessage 是一次对话消息。
type ChatMessage struct {
	Role    string
	Content string
}

// ChatResult 是非流式聊天的结果。
type ChatResult struct {
	Content          string
	Reasoning        string
	FinishReason     string
	PromptTokens     uint64
	CompletionTokens uint64
	Model            string
	SessionID        string
	Host             string
}

// ResolveSelector 客户端模型名 → 上游 selector（免费档默认 swe-1-6-slow）。
func ResolveSelector(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "swe-1-6-slow", "swe-1.6-slow":
		return "swe-1-6-slow"
	case "swe-1-6", "swe-1.6":
		return "swe-1-6"
	case "swe-1-6-fast", "swe-1.6-fast":
		return "swe-1-6-fast"
	case "swe-1-5", "swe-1.5":
		return "MODEL_SWE_1_5_SLOW"
	case "swe-1-7", "swe-1.7":
		return "swe-1-7"
	case "", "windsurf-proxy", "default", "auto":
		return FreeTierModel
	default:
		return strings.TrimSpace(model)
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// fingerprint 366 字节 → 732 hex（时间戳亦惑）。
func fingerprint() string {
	buf := make([]byte, 366)
	_, _ = rand.Read(buf)
	now := uint64(time.Now().UnixNano())
	for i := 0; i < 8; i++ {
		buf[i%366] ^= byte(now >> (8 * uint(i)))
	}
	return hex.EncodeToString(buf)
}

func buildClientMetadata(token string) []byte {
	var out []byte
	for _, kv := range []struct {
		f uint32
		v string
	}{
		{1, ClientName}, {2, ClientVersion}, {3, token}, {4, "en"},
		{5, "windows"}, {7, ClientVersion}, {12, ClientName},
	} {
		out = append(out, EncodeString(kv.f, kv.v)...)
	}
	out = append(out, EncodeString(31, fingerprint())...)
	return out
}

func buildCompletionConfig(maxTokens *int64, temperature *float64) []byte {
	temp := 1.0
	if temperature != nil {
		temp = *temperature
	}
	if temp < 0.001 {
		temp = 0.001
	}
	mt := int64(4096)
	if maxTokens != nil {
		mt = *maxTokens
	}
	var out []byte
	out = append(out, EncodeVarintField(1, 1)...)
	out = append(out, EncodeVarintField(2, 128_000)...)
	out = append(out, EncodeVarintField(3, uint64(mt))...)
	out = append(out, EncodeDoubleField(5, temp)...)
	out = append(out, EncodeVarintField(7, 40)...)
	out = append(out, EncodeDoubleField(8, 0.95)...)
	return out
}

func buildChatMessage(role, text string) []byte {
	source := uint64(1)
	if role == "assistant" {
		source = 2
	}
	var out []byte
	out = append(out, EncodeString(1, newID())...)
	out = append(out, EncodeVarintField(2, source)...)
	out = append(out, EncodeString(3, text)...)
	return out
}

func buildModelConfig() []byte {
	var m []byte
	m = append(m, EncodeString(1, newID())...)
	m = append(m, EncodeVarintField(2, 1)...)
	m = append(m, EncodeVarintField(3, 4)...)
	return m
}

// BuildRequest 构造 GetChatMessage 的 Connect 协议主体（含消息/配置/会话）。
func BuildRequest(token string, messages []ChatMessage, model, sessionID string, maxTokens *int64, temperature *float64) ([]byte, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("missing session token")
	}
	if sessionID == "" {
		sessionID = newID()
	}
	var system []string
	var chatMsgs [][]byte
	for _, m := range messages {
		switch strings.ToLower(m.Role) {
		case "system":
			system = append(system, m.Content)
		case "assistant":
			chatMsgs = append(chatMsgs, buildChatMessage("assistant", m.Content))
		case "tool":
			chatMsgs = append(chatMsgs, buildChatMessage("user", "[tool result]: "+m.Content))
		default:
			chatMsgs = append(chatMsgs, buildChatMessage("user", m.Content))
		}
	}
	if len(chatMsgs) == 0 {
		return nil, errors.New("no user/assistant messages")
	}

	var parts []byte
	parts = append(parts, EncodeMessageField(1, buildClientMetadata(token))...)
	parts = append(parts, EncodeString(2, strings.Join(system, "\n"))...)
	for _, cm := range chatMsgs {
		parts = append(parts, EncodeMessageField(3, cm)...)
	}
	parts = append(parts, EncodeVarintField(7, 5)...)
	parts = append(parts, EncodeMessageField(8, buildCompletionConfig(maxTokens, temperature))...)
	parts = append(parts, EncodeMessageField(15, buildModelConfig())...)
	parts = append(parts, EncodeString(16, sessionID)...)
	parts = append(parts, EncodeVarintField(20, 1)...)
	parts = append(parts, EncodeString(21, ResolveSelector(model))...)
	return parts, nil
}

// wrapEnvelope 构造未压缩 Connect 帧。
func wrapEnvelope(proto []byte) []byte {
	frame := make([]byte, 5+len(proto))
	frame[0] = 0
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(proto)))
	copy(frame[5:], proto)
	return frame
}

func maybeGunzip(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// frameParseNew 是 Connect 帧解析器（增量缓冲）。
type frameBuffer struct {
	buf []byte
}

// push 追加 chunk；drain 剥出完整帧。
func (fb *frameBuffer) drain() ([]connectFrame, error) {
	var out []connectFrame
	for {
		if len(fb.buf) < 5 {
			break
		}
		flags := fb.buf[0]
		size := int(binary.BigEndian.Uint32(fb.buf[1:5]))
		if size > 16*1024*1024 {
			return nil, errors.New("frame too large")
		}
		if len(fb.buf) < 5+size {
			break
		}
		payload := append([]byte(nil), fb.buf[5:5+size]...)
		fb.buf = fb.buf[5+size:]
		if flags&0x01 != 0 {
			p, err := maybeGunzip(payload)
			if err != nil {
				return nil, err
			}
			payload = p
		}
		out = append(out, connectFrame{endStream: flags&0x02 != 0, payload: payload})
	}
	return out, nil
}

type connectFrame struct {
	endStream bool
	payload   []byte
}

// decodeFrameDelta 解析数据帧 → content/reasoning/finish/usage。
func decodeFrameDelta(payload []byte) (content, reasoning string, finish *int64, usage *[2]uint64) {
	m, err := parseMessage(payload)
	if err != nil {
		return "", "", nil, nil
	}
	content = m.FirstString(fieldContent)
	reasoning = m.FirstString(fieldReason)
	if v, ok := m.FirstVarint(fieldFinish); ok {
		f := int64(v)
		finish = &f
	}
	for _, meta := range m.Bytes[fieldUsage] {
		mm, err := parseMessage(meta)
		if err != nil {
			continue
		}
		p, pok := mm.FirstVarint(2)
		c, cok := mm.FirstVarint(3)
		if pok || cok {
			usage = &[2]uint64{p, c}
			break
		}
	}
	return
}

func finishReasonStr(code *int64) string {
	if code == nil {
		return "stop"
	}
	switch *code {
	case 1:
		return "length"
	case 3:
		return "tool_calls"
	default:
		return "stop"
	}
}

// Client 是 Connect-RPC 的 HTTP 客户端。
type Client struct {
	Hosts []string
	HTTP  *http.Client
}

// NewClient 构建客户端（hc nil → http.DefaultClient）。
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{Hosts: append([]string(nil), hosts...), HTTP: hc}
}

func (c *Client) post(ctx context.Context, host, token string, framed []byte) (*http.Response, error) {
	url := "https://" + host + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(framed))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Accept-Encoding", "gzip")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "connect-es/2.0.0")
	req.Header.Set("Authorization", "Basic "+token+"-"+token)
	return c.HTTP.Do(req)
}

// DoChat 非流式：迭代 hosts，成功即返回聚合结果。
func (c *Client) DoChat(ctx context.Context, token string, messages []ChatMessage, model string, maxTokens *int64, temperature *float64) (*ChatResult, error) {
	sessionID := newID()
	proto, err := BuildRequest(token, messages, model, sessionID, maxTokens, temperature)
	if err != nil {
		return nil, err
	}
	framed := wrapEnvelope(proto)

	var lastErr string
	for _, host := range c.Hosts {
		resp, err := c.post(ctx, host, token, framed)
		if err != nil {
			lastErr = host + ": " + err.Error()
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet := truncate(string(body), 400)
			lastErr = fmt.Sprintf("%s HTTP %d: %s", host, resp.StatusCode, snippet)
			continue
		}
		res, err := parseNonStream(body)
		if err != nil {
			lastErr = fmt.Sprintf("%s: %v", host, err)
			continue
		}
		res.Host = host
		res.Model = ResolveSelector(model)
		res.SessionID = sessionID
		return res, nil
	}
	if lastErr == "" {
		lastErr = "all hosts failed"
	}
	return nil, errors.New("DEVIN_CONNECT: " + lastErr)
}

// parseNonStream 解析整包响应的数据帧。
func parseNonStream(body []byte) (*ChatResult, error) {
	fb := &frameBuffer{}
	fb.buf = append(fb.buf, body...)
	frames, err := fb.drain()
	if err != nil {
		return nil, err
	}
	res := &ChatResult{}
	var finish *int64
	var usage *[2]uint64
	sawEOS := false
	for _, fr := range frames {
		if fr.endStream {
			sawEOS = true
			txt := strings.TrimSpace(string(fr.payload))
			if txt != "" && txt != "{}" && strings.Contains(txt, "error") {
				return nil, errors.New("trailer error: " + truncate(txt, 300))
			}
			continue
		}
		content, reasoning, f, u := decodeFrameDelta(fr.payload)
		res.Content += content
		res.Reasoning += reasoning
		if f != nil {
			finish = f
		}
		if u != nil {
			usage = u
		}
	}
	if res.Content == "" && res.Reasoning == "" {
		return nil, fmt.Errorf("empty completion (eos=%v frames=%d)", sawEOS, len(frames))
	}
	if res.Content == "" {
		res.Content = res.Reasoning
	}
	res.FinishReason = finishReasonStr(finish)
	if usage != nil {
		res.PromptTokens = usage[0]
		res.CompletionTokens = usage[1]
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// StreamSSE 发起流式聊天，返回可读的 OpenAI 兼容 SSE 流（逐事件）。
func (c *Client) StreamSSE(ctx context.Context, token string, messages []ChatMessage, model string, maxTokens *int64, temperature *float64) (io.ReadCloser, error) {
	sessionID := newID()
	proto, err := BuildRequest(token, messages, model, sessionID, maxTokens, temperature)
	if err != nil {
		return nil, err
	}
	framed := wrapEnvelope(proto)

	var lastErr string
	for _, host := range c.Hosts {
		resp, err := c.post(ctx, host, token, framed)
		if err != nil {
			lastErr = host + ": " + err.Error()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Sprintf("%s HTTP %d: %s", host, resp.StatusCode, truncate(string(body), 400))
			continue
		}
		return &sseReader{src: resp.Body, sessionID: sessionID}, nil
	}
	if lastErr == "" {
		lastErr = "all hosts failed"
	}
	return nil, errors.New("DEVIN_CONNECT: " + lastErr)
}

// sseReader 把上游数据帧转成 OpenAI 兼容 SSE 行。
type sseReader struct {
	src       io.ReadCloser
	fb        frameBuffer
	done      bool
	streamErr bool // 收到 end_stream 错误 trailer：产 error 事件而非 [DONE]
	pending   []string
	sessionID string
}

func (r *sseReader) fill() error {
	chunk := make([]byte, 32*1024)
	n, err := r.src.Read(chunk)
	if n == 0 {
		return err
	}
	r.fb.buf = append(r.fb.buf, chunk[:n]...)
	frames, ferr := r.fb.drain()
	if ferr != nil {
		return ferr
	}
	for _, fr := range frames {
		if fr.endStream {
			r.done = true
			// Connect-RPC end_stream 帧可能携带 JSON trailer；若含 error 则产出
			// SSE 错误事件（供上层"流中无感换号"触发），否则正常收尾 [DONE]。
			txt := strings.TrimSpace(string(fr.payload))
			if txt != "" && txt != "{}" {
				var trailer map[string]any
				if json.Unmarshal([]byte(txt), &trailer) == nil {
					if e, ok := trailer["error"]; ok && e != nil {
						r.streamErr = true
						r.pending = append(r.pending, "data: "+toJSON(map[string]any{"error": e}))
					}
				}
			}
			continue
		}
		content, reasoning, fin, usage := decodeFrameDelta(fr.payload)
		payload := map[string]any{}
		delta := map[string]any{}
		if reasoning != "" {
			delta["reasoning_content"] = reasoning
		}
		if content != "" {
			delta["content"] = content
		}
		if len(delta) == 0 && fin == nil && usage == nil {
			continue
		}
		payload["choices"] = []any{map[string]any{"delta": delta, "index": 0}}
		r.pending = append(r.pending, "data: "+toJSON(payload))
		if fin != nil {
			chunkMap := map[string]any{
				"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finishReasonStr(fin), "index": 0}},
			}
			r.pending = append(r.pending, "data: "+toJSON(chunkMap))
		}
		if usage != nil {
			r.pending = append(r.pending, "data: "+toJSON(map[string]any{
				"usage": map[string]any{"prompt_tokens": usage[0], "completion_tokens": usage[1]},
			}))
		}
	}
	if r.done && !r.streamErr {
		r.pending = append(r.pending, "data: [DONE]")
	}
	if err != nil && err != io.EOF && !r.done {
		r.pending = append(r.pending, "data: "+toJSON(map[string]any{
			"error": map[string]any{"message": err.Error()},
		}))
	}
	return err
}

// Close 实现 io.Closer。
func (r *sseReader) Close() error {
	return r.src.Close()
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Read 实现 io.Reader：输出 SSE 行，行尾以 \n 分隔。
func (r *sseReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		if r.done {
			return 0, io.EOF
		}
		err := r.fill()
		if err != nil {
			if len(r.pending) > 0 {
				break
			}
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, err
		}
	}
	line := r.pending[0]
	r.pending = r.pending[1:]
	if len(line) > len(p)-1 {
		line = line[:len(p)-1]
	}
	n := copy(p, line)
	p[n] = '\n'
	return n + 1, nil
}
