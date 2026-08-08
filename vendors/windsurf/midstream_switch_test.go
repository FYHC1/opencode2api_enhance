// mid-stream account switch tests (P3-B7). Fakes live in midstream_test.go.
package windsurf

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// mustStream 打开流；失败即 fatal。
func mustStream(t *testing.T, st *contract.Stream, err error) *contract.Stream {
	t.Helper()
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	return st
}

// TestSwitchOnMidStreamError：流内 capacity 错误 → 换号续写；
// 错误事件不外泄；续接请求带已吐内容上下文；旧号冷却、新号可用。
func TestSwitchOnMidStreamError(t *testing.T) {
	v := newTestVendor()
	chat := &scriptChatter{
		byTok: map[string][]*streamSeg{
			"tok-1": {{lines: []string{deltaData("a"), errData("capacity exceeded")}}},
			"tok-2": {{lines: []string{deltaData(" world"), "data: [DONE]"}}},
		},
		calls: map[string]int{},
	}
	v.cfg.Chatter = chat

	st, err := v.ChatStream(context.Background(), chatMsg())
	out := readStream(t, mustStream(t, st, err))
	if !strings.Contains(out, `"a"`) || !strings.Contains(out, `" world"`) {
		t.Fatalf("output missing content: %s", out)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("error event must not be forwarded: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing [DONE]: %s", out)
	}
	if len(chat.reqs) != 2 {
		t.Fatalf("want 2 upstream requests, got %d", len(chat.reqs))
	}
	// 续接请求：原消息 + assistant(已吐内容) + user(请继续)
	msgs := chat.reqs[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("resume messages = %+v, want 3", msgs)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "a" {
		t.Fatalf("resume assistant = %+v", msgs[1])
	}
	if msgs[2].Role != "user" || !strings.Contains(msgs[2].Content.(string), "请继续") {
		t.Fatalf("resume user = %+v", msgs[2])
	}
	// 池状态：tok-1 冷却、tok-2 可用
	ps := v.PoolStatus()
	if ps.Available != 1 || ps.Cooling != 1 {
		t.Fatalf("pool status = %+v, want 1 avail / 1 cooling", ps)
	}
}

// TestSwitchOnEOFWithoutDone：流提前中断（EOF 且无 [DONE]）也应换号续写。
func TestSwitchOnEOFWithoutDone(t *testing.T) {
	v := newTestVendor()
	chat := &scriptChatter{
		byTok: map[string][]*streamSeg{
			"tok-1": {{lines: []string{deltaData("x")}}}, // 无 [DONE] 即 EOF
			"tok-2": {{lines: []string{deltaData("y"), "data: [DONE]"}}},
		},
		calls: map[string]int{},
	}
	v.cfg.Chatter = chat

	st, err := v.ChatStream(context.Background(), chatMsg())
	out := readStream(t, mustStream(t, st, err))
	if !strings.Contains(out, `"x"`) || !strings.Contains(out, `"y"`) {
		t.Fatalf("output missing content: %s", out)
	}
	if len(chat.reqs) != 2 {
		t.Fatalf("want 2 upstream requests, got %d", len(chat.reqs))
	}
}

// TestNoAccountsSurfacesFinalError：无可用换号账号时，错误以 SSE 事件上抛。
func TestNoAccountsSurfacesFinalError(t *testing.T) {
	v := New(Config{MinAvailable: 1, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	v.pool.add(&Account{Email: "only@t", WindsurfSessionToken: "tok-only", QuotaDaily: 100, QuotaWeekly: 100})
	chat := &scriptChatter{
		byTok: map[string][]*streamSeg{
			"tok-only": {{lines: []string{deltaData("p"), errData("quota exhausted")}}},
		},
		calls: map[string]int{},
	}
	v.cfg.Chatter = chat

	st, err := v.ChatStream(context.Background(), chatMsg())
	out := readStream(t, mustStream(t, st, err))
	if !strings.Contains(out, "midstream_error") {
		t.Fatalf("final error must be surfaced: %s", out)
	}
	if strings.Contains(out, "data: [DONE]") {
		t.Fatalf("error stream must not contain [DONE]: %s", out)
	}
	// 该账号已被标记耗尽（进入冷却）
	ps := v.PoolStatus()
	if ps.Available != 0 || ps.Cooling != 1 {
		t.Fatalf("pool status = %+v", ps)
	}
}

// TestNormalCompletionNoSwitch：正常 [DONE] 结束不换号，账号保持可用。
func TestNormalCompletionNoSwitch(t *testing.T) {
	v := newTestVendor()
	chat := &scriptChatter{
		byTok: map[string][]*streamSeg{
			"tok-1": {{lines: []string{deltaData("hi"), "data: [DONE]"}}},
		},
		calls: map[string]int{},
	}
	v.cfg.Chatter = chat

	st, err := v.ChatStream(context.Background(), chatMsg())
	out := readStream(t, mustStream(t, st, err))
	if !strings.Contains(out, "hi") || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("output = %s", out)
	}
	if len(chat.reqs) != 1 {
		t.Fatalf("want 1 upstream request, got %d", len(chat.reqs))
	}
	ps := v.PoolStatus()
	if ps.Available != 2 || ps.Cooling != 0 {
		t.Fatalf("pool should stay healthy: %+v", ps)
	}
}
