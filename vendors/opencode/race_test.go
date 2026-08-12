package opencode

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// delayRT 延迟指定时长后回放 200；ctx 取消则提前返回错误。
type delayRT struct {
	delay     time.Duration
	body      string
	hits      int32
	cancelled int32
}

func (rt *delayRT) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&rt.hits, 1)
	select {
	case <-time.After(rt.delay):
	case <-req.Context().Done():
		atomic.AddInt32(&rt.cancelled, 1)
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Request:    req,
	}, nil
}

// failRT 立即回放指定状态码（非 2xx）。
type failRT struct {
	status int
}

func (rt *failRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: rt.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
		Request:    req,
	}, nil
}

// racerTransport 实现 contract.Racer：每候选一个独立 client。
type racerTransport struct {
	clients []*http.Client
	addrs   []string
}

func (rt *racerTransport) CandidateClients(_ contract.Tier, _ bool, n int) ([]*http.Client, []string) {
	if n > len(rt.clients) {
		n = len(rt.clients)
	}
	return rt.clients[:n], rt.addrs[:n]
}

func (rt *racerTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return rt.clients[0], rt.addrs[0]
}

func (rt *racerTransport) Mark(string, int, error) {}

func newRaceVendor(tr *racerTransport, copies int) *Vendor {
	v := New(Config{Transport: tr, RaceCopies: copies})
	v.SetSession("1.15.3", "ses_t", "proj_t")
	return v
}

// TestChatRaceFastWins 快候选胜出：响应与 nodeAddr 来自快端，慢端被取消（整体耗时≈快端）。
func TestChatRaceFastWins(t *testing.T) {
	slow := &delayRT{delay: 300 * time.Millisecond, body: `{"id":"slow","object":"chat.completion","choices":[]}`}
	fast := &delayRT{delay: 0, body: `{"id":"fast","object":"chat.completion","choices":[]}`}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: slow}, {Transport: fast}},
		addrs:   []string{"slow-addr", "fast-addr"},
	}, 2)

	start := time.Now()
	raw := `{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`
	reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", ""))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 || reply.NodeAddr != "fast-addr" {
		t.Fatalf("status=%d nodeAddr=%s, want 200/fast-addr", reply.Status, reply.NodeAddr)
	}
	if !strings.Contains(string(reply.Body), `"fast"`) {
		t.Fatalf("body=%s, want fast body", reply.Body)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("race took %v, want ~fast (slow cancelled)", elapsed)
	}
	// 快端胜出后立即 cancel：慢 goroutine 可能尚未轮到发起（未进 transport），
	// 因此不强求 slow 被尝试——只要胜者不是它即可（NodeAddr/body 已断言）。
	if atomic.LoadInt32(&slow.cancelled) == 0 && atomic.LoadInt32(&slow.hits) == 0 {
		t.Log("slow 未轮到发起即被 cancel（符合预期：竞速立即锁定）")
	}
	if atomic.LoadInt32(&fast.hits) != 1 {
		t.Fatalf("fast hits=%d, want 1", atomic.LoadInt32(&fast.hits))
	}
}

// TestChatRaceStreamFirstChunkLocks 流式：快流首 chunk 胜出，慢流被取消。
func TestChatRaceStreamFirstChunkLocks(t *testing.T) {
	slow := &delayRT{delay: 300 * time.Millisecond, body: "data: {\"id\":\"slow\"}\n\n"}
	fast := &delayRT{delay: 0, body: "data: {\"id\":\"fast\"}\n\n"}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: slow}, {Transport: fast}},
		addrs:   []string{"slow-addr", "fast-addr"},
	}, 2)

	stream, err := v.ChatStream(context.Background(), msgWith(`{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if stream.NodeAddr != "fast-addr" {
		t.Fatalf("nodeAddr=%s, want fast-addr", stream.NodeAddr)
	}
	buf, _ := io.ReadAll(stream.ReadCloser)
	if !strings.Contains(string(buf), "fast") {
		t.Fatalf("stream body=%s, want fast", buf)
	}
	// 快流首 chunk 胜出：慢 goroutine 可能未轮到发起，不强求被尝试。
	if atomic.LoadInt32(&slow.hits) == 0 {
		t.Log("slow 未轮到发起即被 cancel（符合预期）")
	}
}

// TestChatRaceAllFail 全候选失败：重试耗尽后返回最后状态码（503）。
func TestChatRaceAllFail(t *testing.T) {
	f1 := &failRT{status: http.StatusServiceUnavailable}
	f2 := &failRT{status: http.StatusServiceUnavailable}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: f1}, {Transport: f2}},
		addrs:   []string{"a1", "a2"},
	}, 2)

	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if reply == nil || reply.Status != http.StatusServiceUnavailable {
		t.Fatalf("reply=%+v err=%v, want status 503", reply, err)
	}
}

// TestChatRaceDisabledDegrades 竞速关闭（copies=1）：只命中首个候选，退化单发。
func TestChatRaceDisabledDegrades(t *testing.T) {
	first := &delayRT{delay: 0, body: `{"id":"first","object":"chat.completion","choices":[]}`}
	second := &delayRT{delay: 0, body: `{"id":"second","object":"chat.completion","choices":[]}`}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: first}, {Transport: second}},
		addrs:   []string{"first-addr", "second-addr"},
	}, 1) // copies=1 → 竞速关闭

	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.NodeAddr != "first-addr" {
		t.Fatalf("nodeAddr=%s, want first-addr (single)", reply.NodeAddr)
	}
	if atomic.LoadInt32(&second.hits) != 0 {
		t.Fatalf("second hits=%d, want 0 (race disabled)", atomic.LoadInt32(&second.hits))
	}
}
