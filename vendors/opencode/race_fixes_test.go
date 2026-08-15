// G1/G6/G4 回归测试：
//   - G1：付费层（TierPaid）不进入竞速，免费层仍竞速且 tier 透传 CandidateClients
//   - G6：竞速落选候选的失败结果 Mark 上报池健康；赢家不被重复 Mark
//   - G4：transport 由 New 固化，并发首访无数据竞争（-race）
package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// ---------------------------------------------------------------- G1：付费层跳过竞速

// tierRacerTransport 记录 CandidateClients 是否被调用及收到的 tier（G1）。
type tierRacerTransport struct {
	rt        *fakeRT
	candCalls atomic.Int64
	tier      atomic.Int64
}

func (t *tierRacerTransport) CandidateClients(tier contract.Tier, _ bool, n int) ([]*http.Client, []string) {
	t.candCalls.Add(1)
	t.tier.Store(int64(tier))
	clients := make([]*http.Client, 0, n)
	addrs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		clients = append(clients, &http.Client{Transport: t.rt})
		addrs = append(addrs, fmt.Sprintf("n%d", i))
	}
	return clients, addrs
}

func (t *tierRacerTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return &http.Client{Transport: t.rt}, "n1"
}

func (t *tierRacerTransport) Mark(string, int, error) {}

func (t *tierRacerTransport) HealthyNodeCount() int { return 1000 }

func (t *tierRacerTransport) RaceStarted([]string) {}

func (t *tierRacerTransport) RaceFinished([]string) {}

// TestTierPaidSkipsRace G1：付费层请求不进入竞速（CandidateClients 不被调用），
// 与单发路径一致走 Client 直连；非流式/流式两条路径都要一致处理。
func TestTierPaidSkipsRace(t *testing.T) {
	ok := `{"id":"z","object":"chat.completion","choices":[]}`
	for _, tc := range []struct {
		name      string
		streaming bool
		mode      string // 付费路由：zen / go / auto（带 token）
	}{
		{"non-stream zen", false, "zen"},
		{"stream zen", true, "zen"},
		{"non-stream go", false, "go"},
		{"non-stream auto token", false, "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &tierRacerTransport{rt: &fakeRT{responses: []fakeResp{{status: 200, body: ok}}}}
			v := New(Config{Transport: tr, RaceCopies: 2})
			v.SetSession("1.15.3", "ses_g1", "proj_g1")

			raw := `{"model":"m-free","stream":` + map[bool]string{true: "true", false: "false"}[tc.streaming] + `,"messages":[{"role":"user","content":"hi"}]}`
			if tc.streaming {
				st, err := v.ChatStream(context.Background(), msgWith(raw, "m-free", tc.mode, "sk-xxx"))
				if err != nil {
					t.Fatalf("ChatStream: %v", err)
				}
				st.Close()
				if st.Status != 200 {
					t.Fatalf("status=%d, want 200", st.Status)
				}
			} else {
				reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", tc.mode, "sk-xxx"))
				if err != nil {
					t.Fatalf("Chat: %v", err)
				}
				if reply.Status != 200 {
					t.Fatalf("status=%d, want 200", reply.Status)
				}
				if reply.NodeAddr != "n1" {
					t.Fatalf("nodeAddr=%q, want n1（直连单发）", reply.NodeAddr)
				}
			}
			if tr.candCalls.Load() != 0 {
				t.Fatalf("CandidateClients 被调用 %d 次，付费层不应进入竞速", tr.candCalls.Load())
			}
		})
	}
}

// TestTierFreeStillRaces G1：免费层（public）仍竞速，且 tier 透传为 TierFree。
func TestTierFreeStillRaces(t *testing.T) {
	ok := `{"id":"z","object":"chat.completion","choices":[]}`
	tr := &tierRacerTransport{rt: &fakeRT{responses: []fakeResp{{status: 200, body: ok}, {status: 200, body: ok}}}}
	v := New(Config{Transport: tr, RaceCopies: 2})
	v.SetSession("1.15.3", "ses_g1", "proj_g1")

	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 {
		t.Fatalf("status=%d, want 200", reply.Status)
	}
	if tr.candCalls.Load() != 1 {
		t.Fatalf("CandidateClients 调用 %d 次，免费层应竞速", tr.candCalls.Load())
	}
	if tr.tier.Load() != int64(contract.TierFree) {
		t.Fatalf("CandidateClients(tier=%d), want TierFree", tr.tier.Load())
	}
}

// TestRaceDoPassesTierThrough G1：raceDo 把 tier 原样透传给 CandidateClients（不再硬编码 TierFree）。
func TestRaceDoPassesTierThrough(t *testing.T) {
	tr := &tierRacerTransport{rt: &fakeRT{responses: []fakeResp{{status: 200, body: `{}`}, {status: 200, body: `{}`}}}}
	v := New(Config{Transport: tr, RaceCopies: 2})
	v.SetSession("1.15.3", "ses_g1", "proj_g1")

	req, err := http.NewRequest("POST", "https://opencode.ai/zen/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, _, err := v.raceDo(context.Background(), tr, req, false, contract.TierPaid, 2, tr.Mark)
	if err != nil {
		t.Fatalf("raceDo: %v", err)
	}
	resp.Body.Close()
	if tr.candCalls.Load() != 1 || tr.tier.Load() != int64(contract.TierPaid) {
		t.Fatalf("candCalls=%d tier=%d, want 1/TierPaid（tier 透传）", tr.candCalls.Load(), tr.tier.Load())
	}
}

// ---------------------------------------------------------------- G6：落选候选失败上报

type markEntry struct {
	addr   string
	status int
	err    error
}

// g6Racer 实现 contract.Racer：记录每次 Mark（addr/status/err）。
type g6Racer struct {
	clients []*http.Client
	addrs   []string

	mu    sync.Mutex
	marks []markEntry
}

func (t *g6Racer) CandidateClients(_ contract.Tier, _ bool, n int) ([]*http.Client, []string) {
	if n > len(t.clients) {
		n = len(t.clients)
	}
	return t.clients[:n], t.addrs[:n]
}

func (t *g6Racer) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return t.clients[0], t.addrs[0]
}

func (t *g6Racer) Mark(addr string, status int, err error) {
	t.mu.Lock()
	t.marks = append(t.marks, markEntry{addr: addr, status: status, err: err})
	t.mu.Unlock()
}

func (t *g6Racer) markSnapshot() []markEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]markEntry(nil), t.marks...)
}

func (t *g6Racer) HealthyNodeCount() int { return 1000 }

func (t *g6Racer) RaceStarted([]string) {}

func (t *g6Racer) RaceFinished([]string) {}

func countMarks(marks []markEntry, addr string, status int) int {
	n := 0
	for _, m := range marks {
		if m.addr == addr && m.status == status && m.err == nil {
			n++
		}
	}
	return n
}

// TestRaceLoserFailureMarked G6：竞速赢家锁流后，落选候选的失败被 Mark 上报；
// 赢家只被调用方 call 标记一次（不重复）。
func TestRaceLoserFailureMarked(t *testing.T) {
	loser := &failRT{status: http.StatusServiceUnavailable}
	winner := &delayRT{delay: 20 * time.Millisecond, body: `{"id":"win","object":"chat.completion","choices":[]}`}
	tr := &g6Racer{
		clients: []*http.Client{{Transport: loser}, {Transport: winner}},
		addrs:   []string{"loser-addr", "win-addr"},
	}
	v := newRaceVendor(tr, 2)

	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 || reply.NodeAddr != "win-addr" {
		t.Fatalf("status=%d nodeAddr=%s, want 200/win-addr", reply.Status, reply.NodeAddr)
	}
	// 落选候选的 503 由竞速路径上报（主循环或 raceDrain 收尾其一）。
	deadline := time.Now().Add(2 * time.Second)
	for countMarks(tr.markSnapshot(), "loser-addr", http.StatusServiceUnavailable) != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	marks := tr.markSnapshot()
	if got := countMarks(marks, "loser-addr", http.StatusServiceUnavailable); got != 1 {
		t.Fatalf("落选候选 marked=%d, want 1（失败必须可见）; marks=%v", got, marks)
	}
	if got := countMarks(marks, "win-addr", http.StatusOK); got != 1 {
		t.Fatalf("赢家 marked=%d, want 1（不重复标记）; marks=%v", got, marks)
	}
	if len(marks) != 2 {
		t.Fatalf("marks=%v, want 仅落选失败+赢家各 1 次", marks)
	}
}

// TestRaceAllFailCandidatesAllMarked G6：全败路径各候选逐个上报（不能只报 firstFail）。
// 选 400（不可重试）避免单发重试污染 mark 断言。
func TestRaceAllFailCandidatesAllMarked(t *testing.T) {
	f1 := &failRT{status: http.StatusBadRequest}
	f2 := &failRT{status: http.StatusBadRequest}
	tr := &g6Racer{
		clients: []*http.Client{{Transport: f1}, {Transport: f2}},
		addrs:   []string{"a1", "a2"},
	}
	v := newRaceVendor(tr, 2)

	reply, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if reply == nil || reply.Status != http.StatusBadRequest {
		t.Fatalf("reply=%+v err=%v, want status 400", reply, err)
	}
	marks := tr.markSnapshot()
	if got := countMarks(marks, "a1", http.StatusBadRequest); got != 1 {
		t.Fatalf("候选 a1 marked=%d, want 1; marks=%v", got, marks)
	}
	if got := countMarks(marks, "a2", http.StatusBadRequest); got != 1 {
		t.Fatalf("候选 a2 marked=%d, want 1; marks=%v", got, marks)
	}
	if len(marks) != 2 {
		t.Fatalf("marks=%v, want 全败两候选各 1 次", marks)
	}
}

// cancelRT 立即回放 context.Canceled（模拟客户端断开 / 上游连接被取消）。
type cancelRT struct{}

func (rt *cancelRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, context.Canceled
}

// TestRaceAllFailCanceledNotMarked G32：竞速全败且错误为 ctx.Canceled 时，
// call() 不得把 Canceled 连带真实 addr Mark 到节点（不污染冷却/熔断）。
// raceMarkOutcome 已排除 Canceled，此处补 all-fail 返回路径的缺口。
func TestRaceAllFailCanceledNotMarked(t *testing.T) {
	for _, tc := range []struct {
		name      string
		streaming bool
	}{
		{"non-stream", false},
		{"stream", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &g6Racer{
				clients: []*http.Client{{Transport: &cancelRT{}}, {Transport: &cancelRT{}}},
				addrs:   []string{"c1", "c2"},
			}
			v := newRaceVendor(tr, 2)
			raw := `{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`
			if tc.streaming {
				raw = `{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			}
			if tc.streaming {
				st, err := v.ChatStream(context.Background(), msgWith(raw, "m-free", "public", ""))
				if st != nil {
					st.Close()
				}
				if err == nil {
					t.Fatalf("ChatStream err=nil, want 全败失败")
				}
			} else {
				if _, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", "")); !errors.Is(err, context.Canceled) {
					t.Fatalf("Chat err=%v, want context.Canceled", err)
				}
			}
			// raceDrain 异步收尾，轮询等待其落定后再断言无 Mark。
			deadline := time.Now().Add(2 * time.Second)
			for len(tr.markSnapshot()) != 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if marks := tr.markSnapshot(); len(marks) != 0 {
				t.Fatalf("marks=%v, want 0（ctx.Canceled 不得标记到节点）", marks)
			}
		})
	}
}

// TestRaceStreamLoserFailureMarked G6：流式路径同样上报落选候选失败。
func TestRaceStreamLoserFailureMarked(t *testing.T) {
	loser := &failRT{status: http.StatusServiceUnavailable}
	winner := &delayRT{delay: 20 * time.Millisecond, body: "data: {\"id\":\"win\"}\n\n"}
	tr := &g6Racer{
		clients: []*http.Client{{Transport: loser}, {Transport: winner}},
		addrs:   []string{"loser-addr", "win-addr"},
	}
	v := newRaceVendor(tr, 2)

	st, err := v.ChatStream(context.Background(), msgWith(`{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	st.Close()
	if st.Status != 200 || st.NodeAddr != "win-addr" {
		t.Fatalf("status=%d nodeAddr=%s, want 200/win-addr", st.Status, st.NodeAddr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for countMarks(tr.markSnapshot(), "loser-addr", http.StatusServiceUnavailable) != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	marks := tr.markSnapshot()
	if got := countMarks(marks, "loser-addr", http.StatusServiceUnavailable); got != 1 {
		t.Fatalf("落选候选 marked=%d, want 1; marks=%v", got, marks)
	}
	if got := countMarks(marks, "win-addr", http.StatusOK); got != 1 {
		t.Fatalf("赢家 marked=%d, want 1（不重复）; marks=%v", got, marks)
	}
	if len(marks) != 2 {
		t.Fatalf("marks=%v, want 仅 2 条", marks)
	}
}

// ---------------------------------------------------------------- G4：transport 并发首访

// TestConcurrentFirstTransportAccess G4：并发首访 transport 无数据竞争（-race 检测）。
// 修复前 transport() 懒初始化对 v.tr 无锁写；修复后 New 固化、transport() 只读。
func TestConcurrentFirstTransportAccess(t *testing.T) {
	rt := &fakeRT{}
	const n = 16
	rt.responses = make([]fakeResp, n)
	for i := range rt.responses {
		rt.responses[i] = fakeResp{status: 200, body: `{"id":"z","object":"chat.completion","choices":[]}`}
	}
	tr := &fakeContractTransport{rt: rt, proxyAddr: "n1"}
	v := New(Config{Transport: tr, RaceCopies: 1}) // 单发路径，聚焦 transport() 并发
	v.SetSession("1.15.3", "ses_g4", "proj_g4")

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(streaming bool) {
			defer wg.Done()
			raw := `{"model":"m-free","stream":` + map[bool]string{true: "true", false: "false"}[streaming] + `,"messages":[]}`
			if streaming {
				st, err := v.ChatStream(context.Background(), msgWith(raw, "m-free", "public", ""))
				if err != nil {
					t.Errorf("ChatStream: %v", err)
					return
				}
				st.Close()
				if st.Status != 200 {
					t.Errorf("stream status=%d", st.Status)
				}
				return
			}
			reply, err := v.Chat(context.Background(), msgWith(raw, "m-free", "public", ""))
			if err != nil {
				t.Errorf("Chat: %v", err)
				return
			}
			if reply.Status != 200 {
				t.Errorf("status=%d", reply.Status)
			}
		}(i%2 == 0)
	}
	wg.Wait()
}

// TestNewFixesTransport G4：New 即固化 transport（注入的保持原值，nil 退回直连）。
func TestNewFixesTransport(t *testing.T) {
	if _, ok := New(Config{}).transport().(contract.DirectTransport); !ok {
		t.Fatal("nil Transport 应退回 DirectTransport")
	}
	tr := &fakeContractTransport{rt: &fakeRT{}, proxyAddr: "n1"}
	if got := New(Config{Transport: tr}).transport(); got != tr {
		t.Fatalf("注入的 Transport 未保持原值: %v", got)
	}
}