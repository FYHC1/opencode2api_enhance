package windsurf

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// linesReader 逐行回放（每行自带 '\n'），读完返回 io.EOF。
type linesReader struct {
	lines []string
	i     int
}

func (r *linesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.lines) {
		return 0, io.EOF
	}
	line := r.lines[r.i] + "\n"
	r.i++
	return copy(p, line), nil
}

func (r *linesReader) Close() error { return nil }

// streamSeg 是一次 DoChatStream 调用的预期产出。
type streamSeg struct {
	lines []string // SSE 行（不含换行）；按序逐行返回
	err   error    // 非 nil：该次调用直接返回错误（连接失败）
}

// scriptChatter 记录请求并按 token 顺序回放脚本。
type scriptChatter struct {
	mu    sync.Mutex
	byTok map[string][]*streamSeg
	calls map[string]int
	reqs  []*contract.Message
}

func (s *scriptChatter) DoChatStream(_ context.Context, token string, msg *contract.Message) (*contract.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, msg)
	i := s.calls[token]
	s.calls[token] = i + 1
	segs := s.byTok[token]
	if segs == nil || i >= len(segs) {
		return nil, errors.New("unscripted call for token " + token)
	}
	seg := segs[i]
	if seg.err != nil {
		return nil, seg.err
	}
	return &contract.Stream{Status: http.StatusOK, ReadCloser: &linesReader{lines: seg.lines}}, nil
}

// DoChat 满足 Chatter 接口（本组测试只走流式路径）。
func (s *scriptChatter) DoChat(_ context.Context, _ string, _ *contract.Message) (*contract.Reply, error) {
	return nil, errors.New("scriptChatter: streaming only")
}

// sseData 构造 OpenAI SSE data 行。
func sseData(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b)
}

// deltaData 构造内容增量 chunk。
func deltaData(content string) string {
	return sseData(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{"content": content},
			"index": 0,
		}},
	})
}

// errData 构造上游错误事件行。
func errData(msg string) string {
	return sseData(map[string]any{
		"error": map[string]any{"type": "capacity", "message": msg},
	})
}

// newTestVendor 构造带两个健康账号的检测厂商。
func newTestVendor() *Vendor {
	v := New(Config{MinAvailable: 2, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	v.pool.add(&Account{Email: "acc1@t", WindsurfSessionToken: "tok-1", QuotaDaily: 100, QuotaWeekly: 100})
	v.pool.add(&Account{Email: "acc2@t", WindsurfSessionToken: "tok-2", QuotaDaily: 100, QuotaWeekly: 100})
	return v
}

// chatMsg 便捷构造归一化请求。
func chatMsg(messages ...contract.Msg) *contract.Message {
	if len(messages) == 0 {
		messages = []contract.Msg{{Role: "user", Content: "hi"}}
	}
	return &contract.Message{Model: "swe-1-6-slow", Messages: messages}
}

// readStream 读取流直到 EOF，返回拼出的文本。
func readStream(t *testing.T, st *contract.Stream) string {
	t.Helper()
	defer st.Close()
	all, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(all)
}

// begin testing section
