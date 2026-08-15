// CONC-1（H1）测试：请求取消穿透——已取消 / 进行中取消的 ctx 都能快速中止
// 下游调用，transport 的 RoundTrip 观察到请求 ctx 取消（-race 干净）。
package opencode

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// cancelContractTransport 把 cancelRecorderRT / hangCancelRT 桥接为 contract.Transport。
type cancelContractTransport struct {
	rt http.RoundTripper
}

func (t *cancelContractTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return &http.Client{Transport: t.rt}, "n1"
}

func (t *cancelContractTransport) Mark(string, int, error) {}

// newCancelVendor 构造带取消感知 fake transport 的厂商（单发路径）。
func newCancelVendor(rt http.RoundTripper) *Vendor {
	v := New(Config{Transport: &cancelContractTransport{rt: rt}})
	v.SetSession("1.15.3", "ses_cancel", "proj_cancel")
	return v
}

// cancelRecorderRT 记录每次 RoundTrip 收到的请求 ctx 是否已取消，并按其快速失败。
type cancelRecorderRT struct {
	hits      int32
	cancelled int32
}

func (rt *cancelRecorderRT) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&rt.hits, 1)
	if req.Context().Err() != nil {
		atomic.AddInt32(&rt.cancelled, 1)
	}
	return nil, req.Context().Err()
}

// hangCancelRT 阻塞 RoundTrip 直到请求 ctx 取消（模拟挂起上游 + 客户端断开）。
type hangCancelRT struct {
	cancelled int32
	returned  chan struct{}
	once      sync.Once
}

func newHangCancelRT() *hangCancelRT { return &hangCancelRT{returned: make(chan struct{})} }

func (rt *hangCancelRT) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	atomic.AddInt32(&rt.cancelled, 1)
	rt.once.Do(func() { close(rt.returned) })
	return nil, req.Context().Err()
}

// TestChatCanceledCtxFastFails 已取消的 ctx：Chat 快速返回，下游 transport 收到取消。
func TestChatCanceledCtxFastFails(t *testing.T) {
	rt := &cancelRecorderRT{}
	v := newCancelVendor(rt)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	reply, err := v.Chat(ctx, msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Chat err = %v, want context.Canceled", err)
	}
	if reply != nil && reply.Status != 0 {
		t.Fatalf("reply status = %d, want 0（取消后无上游响应）", reply.Status)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Chat took %v, want fast return on cancelled ctx", elapsed)
	}
	if atomic.LoadInt32(&rt.cancelled) == 0 {
		t.Fatalf("downstream transport never observed cancellation (hits=%d)", atomic.LoadInt32(&rt.hits))
	}
}

// TestChatStreamCanceledCtxFastFails 已取消的 ctx：ChatStream 快速返回，下游 transport 收到取消。
func TestChatStreamCanceledCtxFastFails(t *testing.T) {
	rt := &cancelRecorderRT{}
	v := newCancelVendor(rt)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	st, err := v.ChatStream(ctx, msgWith(`{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	elapsed := time.Since(start)
	if st != nil {
		st.Close()
	}
	if err == nil {
		t.Fatal("ChatStream err = nil, want error（已取消 ctx 不得成功）")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ChatStream took %v, want fast return on cancelled ctx", elapsed)
	}
	if atomic.LoadInt32(&rt.cancelled) == 0 {
		t.Fatalf("downstream transport never observed cancellation (hits=%d)", atomic.LoadInt32(&rt.hits))
	}
}

// TestChatCancelMidFlightAbortsRequest 进行中取消：挂起 RoundTrip 收到取消并返回，
// Chat 快速失败，无悬挂 goroutine（-race 检测泄漏）。
func TestChatCancelMidFlightAbortsRequest(t *testing.T) {
	rt := newHangCancelRT()
	v := newCancelVendor(rt)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = v.Chat(ctx, msgWith(`{"model":"m-free","messages":[]}`, "m-free", "public", ""))
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // 让请求进入挂起 RoundTrip
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Chat did not return after ctx cancel (leak)")
	}
	if atomic.LoadInt32(&rt.cancelled) == 0 {
		t.Fatal("hanging transport never observed cancellation")
	}
	select {
	case <-rt.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("RoundTrip goroutine still running (leak)")
	}
}

// TestChatCanceledCtxCancelsRaceCandidates 竞速路径：已取消的 ctx 传入 raceDo，
// 各候选 RoundTrip 都观察到取消并快速全败返回（raceDo 内部不动，ctx 到达即可取消）。
func TestChatCanceledCtxCancelsRaceCandidates(t *testing.T) {
	h1 := newHangRT()
	h2 := newHangRT()
	tr := &racerTransport{
		clients: []*http.Client{{Transport: h1}, {Transport: h2}},
		addrs:   []string{"c1", "c2"},
	}
	v := newRaceVendor(tr, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	reply, err := v.Chat(ctx, msgWith(`{"model":"m-free","messages":[]}`, "m-free", "public", ""))
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if reply != nil && reply.Status != 0 {
		t.Fatalf("reply status = %d, want 0", reply.Status)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("race took %v, want fast fail on cancelled ctx", elapsed)
	}
	// 两个候选都观察到取消且 goroutine 退出（无泄漏）。
	for _, h := range []*hangRT{h1, h2} {
		if atomic.LoadInt32(&h.cancelled) == 0 {
			t.Fatal("race candidate never observed cancellation")
		}
		select {
		case <-h.returned:
		case <-time.After(2 * time.Second):
			t.Fatal("race candidate goroutine still running (leak)")
		}
	}
}
