package opencode

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
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

func newRaceVendor(tr contract.Transport, copies int) *Vendor {
	v := New(Config{Transport: tr, RaceCopies: copies})
	v.SetSession("1.15.3", "ses_t", "proj_t")
	return v
}

// ---- S1 竞速整体预算：挂起候选快速失败 / 无悬挂 goroutine ----

// hangRT 挂起候选：阻塞 RoundTrip 不返回，直到请求 ctx 取消（真实 transport 的取消语义）。
type hangRT struct {
	cancelled int32
	returned  chan struct{}
	once      sync.Once
}

func newHangRT() *hangRT { return &hangRT{returned: make(chan struct{})} }

func (rt *hangRT) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	atomic.AddInt32(&rt.cancelled, 1)
	rt.once.Do(func() { close(rt.returned) })
	return nil, req.Context().Err()
}

// hangBody 流式挂起体：Read 阻塞直到 Close（模拟首字节永不达）。
type hangBody struct {
	closed chan struct{}
	once   sync.Once
}

func newHangBody() *hangBody { return &hangBody{closed: make(chan struct{})} }

func (b *hangBody) Read(p []byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}

func (b *hangBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// hangStreamRT 立即回放 200 + hangBody（响应头已到，首字节永不达）。
type hangStreamRT struct {
	body *hangBody
}

func (rt *hangStreamRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       rt.body,
		Request:    req,
	}, nil
}

// trickleBody 慢吐流：每 chunk 间隔 interval；响应 ctx 取消时立即失败（挂起候选可回收）。
type trickleBody struct {
	mu       sync.Mutex
	interval time.Duration
	chunks   int
	sent     int
	closed   bool
	ctx      context.Context
}

func (b *trickleBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed || b.sent >= b.chunks {
		b.mu.Unlock()
		return 0, io.EOF
	}
	b.sent++
	b.mu.Unlock()
	select {
	case <-time.After(b.interval):
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func (b *trickleBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

// trickleRT 立即回放 200 + 慢吐流。
type trickleRT struct {
	interval time.Duration
	chunks   int
}

func (rt *trickleRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       &trickleBody{interval: rt.interval, chunks: rt.chunks, ctx: req.Context()},
		Request:    req,
	}, nil
}

// budgetRaceTransport：竞速候选全部挂起；单发路径（Client）回放成功。
type budgetRaceTransport struct {
	hangClients []*http.Client
	hangAddrs   []string
	okClient    *http.Client
	okAddr      string
}

func (t *budgetRaceTransport) CandidateClients(_ contract.Tier, _ bool, n int) ([]*http.Client, []string) {
	return t.hangClients, t.hangAddrs
}

func (t *budgetRaceTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return t.okClient, t.okAddr
}

func (t *budgetRaceTransport) Mark(string, int, error) {}

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

// ---- S1：竞速整体预算 ----

// TestRaceDoBudgetExpiryFastFail 挂起候选（阻塞 RoundTrip）：预算到期返回错误、
// 全部候选被 cancel、无悬挂 goroutine（配合 go test -race）。
func TestRaceDoBudgetExpiryFastFail(t *testing.T) {
	h1 := newHangRT()
	h2 := newHangRT()
	tr := &racerTransport{
		clients: []*http.Client{{Transport: h1}, {Transport: h2}},
		addrs:   []string{"h1", "h2"},
	}
	v := newRaceVendor(tr, 2)
	v.cfg.RaceBudgetMS = 200

	req, err := http.NewRequest("POST", "https://opencode.ai/zen/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, addr, err := v.raceDo(context.Background(), tr, req, false, 2)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "race budget") {
		t.Fatalf("err=%v, want race budget exceeded", err)
	}
	if resp != nil || addr != "" {
		t.Fatalf("resp=%v addr=%q, want nil on budget expiry", resp, addr)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("fast-fail took %v, want ~200ms", elapsed)
	}
	// cancel 被调用：两个候选的 RoundTrip 都观察到 ctx 取消（异步返回，轮询等待）。
	deadline := time.Now().Add(2 * time.Second)
	for (atomic.LoadInt32(&h1.cancelled) != 1 || atomic.LoadInt32(&h2.cancelled) != 1) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&h1.cancelled) != 1 || atomic.LoadInt32(&h2.cancelled) != 1 {
		t.Fatalf("cancelled h1=%d h2=%d, want 1/1", atomic.LoadInt32(&h1.cancelled), atomic.LoadInt32(&h2.cancelled))
	}
	// 无悬挂 goroutine：两个挂起 RoundTrip 均已返回。
	for _, h := range []*hangRT{h1, h2} {
		select {
		case <-h.returned:
		case <-time.After(2 * time.Second):
			t.Fatal("hanging RoundTrip goroutine still running (leak)")
		}
	}
}

// TestChatRaceBudgetErrorFallsBackToSingle 竞速预算到期 → 上层 retryCount 循环
// 进入单发路径（tr.Client）成功返回（续写兜底链）。
func TestChatRaceBudgetErrorFallsBackToSingle(t *testing.T) {
	h := newHangRT()
	okRT := &delayRT{delay: 0, body: `{"id":"single","object":"chat.completion","choices":[]}`}
	v := newRaceVendor(&budgetRaceTransport{
		hangClients: []*http.Client{{Transport: h}, {Transport: h}},
		hangAddrs:   []string{"h1", "h2"},
		okClient:    &http.Client{Transport: okRT},
		okAddr:      "ok-addr",
	}, 2)
	v.cfg.RaceBudgetMS = 150

	start := time.Now()
	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat: %v (budget error should fall back to single-flight)", err)
	}
	if reply.NodeAddr != "ok-addr" {
		t.Fatalf("nodeAddr=%s, want ok-addr (single-flight retry)", reply.NodeAddr)
	}
	if !strings.Contains(string(reply.Body), `"single"`) {
		t.Fatalf("body=%s, want single body", reply.Body)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("elapsed=%v, want >= budget (race attempted then fell back)", elapsed)
	}
	// 竞速候选被取消且 goroutine 已退出。
	if atomic.LoadInt32(&h.cancelled) == 0 {
		t.Fatal("hanging race candidate was not cancelled")
	}
	select {
	case <-h.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("race candidate goroutine still running (leak)")
	}
}

// TestRaceDoStreamFirstByteTimeout 流式候选首字节等待超时（headers 已到、首字节永不达）：
// 预算内返回错误，挂起流被关闭（Read 解除阻塞），无悬挂 goroutine。
func TestRaceDoStreamFirstByteTimeout(t *testing.T) {
	b1 := newHangBody()
	b2 := newHangBody()
	tr := &racerTransport{
		clients: []*http.Client{{Transport: &hangStreamRT{body: b1}}, {Transport: &hangStreamRT{body: b2}}},
		addrs:   []string{"s1", "s2"},
	}
	v := newRaceVendor(tr, 2)
	v.cfg.RaceBudgetMS = 200

	req, _ := http.NewRequest("POST", "https://opencode.ai/zen/v1/chat/completions", strings.NewReader(`{}`))
	start := time.Now()
	resp, addr, err := v.raceDo(context.Background(), tr, req, true, 2)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("want timeout error, got resp=%v addr=%s", resp, addr)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("stream timeout took %v, want ~200ms", elapsed)
	}
	// 挂起流被关闭 → 首字节读 goroutine 退出（无悬挂）。
	for _, b := range []*hangBody{b1, b2} {
		select {
		case <-b.closed:
		case <-time.After(2 * time.Second):
			t.Fatal("hanging stream body not closed (leak)")
		}
	}
}

// TestChatStreamLongStreamNotTruncated 锁流后长流不被截断：赢家首字节在预算内，
// 随后慢吐总时长超过预算，仍完整读完（赢家 ctx 不被取消 + 客户端无总超时）。
func TestChatStreamLongStreamNotTruncated(t *testing.T) {
	winner := &trickleRT{interval: 80 * time.Millisecond, chunks: 5}
	loserBody := newHangBody()
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: winner}, {Transport: &hangStreamRT{body: loserBody}}},
		addrs:   []string{"w-addr", "l-addr"},
	}, 2)
	v.cfg.RaceBudgetMS = 200

	stream, err := v.ChatStream(context.Background(), msgWith(`{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()
	if stream.NodeAddr != "w-addr" {
		t.Fatalf("nodeAddr=%s, want w-addr", stream.NodeAddr)
	}
	start := time.Now()
	buf, rerr := io.ReadAll(stream.ReadCloser)
	elapsed := time.Since(start)
	if rerr != nil {
		t.Fatalf("read stream: %v", rerr)
	}
	if len(buf) != 5 {
		t.Fatalf("stream len=%d, want 5 (not truncated)", len(buf))
	}
	// 总读取时长超过预算：赢家流不被竞速预算截断（计时读完）。
	if elapsed < 200*time.Millisecond {
		t.Fatalf("stream read took %v, want > budget (long stream not truncated)", elapsed)
	}
	// 落选候选的挂起流被关闭回收。
	select {
	case <-loserBody.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("loser stream body not closed")
	}
}

// TestRaceBudgetZeroDefaults 预算 0 回退 10s 默认；负数同样回退。
func TestRaceBudgetZeroDefaults(t *testing.T) {
	v := New(Config{})
	if got := v.raceBudget(); got != 10*time.Second {
		t.Fatalf("default budget=%v, want 10s", got)
	}
	v2 := New(Config{RaceBudgetMS: 500})
	if got := v2.raceBudget(); got != 500*time.Millisecond {
		t.Fatalf("configured budget=%v, want 500ms", got)
	}
	v3 := New(Config{RaceBudgetMS: -1})
	if got := v3.raceBudget(); got != 10*time.Second {
		t.Fatalf("negative budget=%v, want 10s fallback", got)
	}
}

// TestChatRaceFastWinsWithinBudget 快候选在预算内返回（不回归）：预算很小、慢候选
// 超过预算，快候选仍正常胜出（预算只兜底，不误杀快赢）。
func TestChatRaceFastWinsWithinBudget(t *testing.T) {
	slow := &delayRT{delay: 600 * time.Millisecond, body: `{"id":"slow","object":"chat.completion","choices":[]}`}
	fast := &delayRT{delay: 0, body: `{"id":"fast","object":"chat.completion","choices":[]}`}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: slow}, {Transport: fast}},
		addrs:   []string{"slow-addr", "fast-addr"},
	}, 2)
	v.cfg.RaceBudgetMS = 200 // 慢候选超过预算，快候选必须仍胜出

	start := time.Now()
	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.NodeAddr != "fast-addr" {
		t.Fatalf("nodeAddr=%s, want fast-addr", reply.NodeAddr)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("race took %v, want ~fast (slow cancelled)", elapsed)
	}
	// 慢候选被取消（其 ctx 在赢家锁流后取消），goroutine 退出（无悬挂）。
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&slow.cancelled) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&slow.cancelled) == 0 {
		t.Fatal("slow candidate not cancelled after fast win (leak)")
	}
}
