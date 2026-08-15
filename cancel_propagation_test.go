// CONC-1（H1）适配层测试：callOpenCodeAPI / callOpenCodeAPIStream 接收请求 ctx，
// 客户端断开（ctx 取消）后快速返回，下游 transport 的 RoundTrip 观察到取消。
package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cancelRecorderRT 记录每次 RoundTrip 收到的请求 ctx 是否已取消，并按取消失败。
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

// hangRT2 阻塞 RoundTrip 直到请求 ctx 取消（模拟客户端在请求挂起期间断开）。
type hangRT2 struct {
	cancelled int32
	started   chan struct{}
	returned  chan struct{}
	startOnce sync.Once
	endOnce   sync.Once
}

func newHangRT2() *hangRT2 {
	return &hangRT2{started: make(chan struct{}), returned: make(chan struct{})}
}

func (rt *hangRT2) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&rt.cancelled, 1)
	rt.startOnce.Do(func() { close(rt.started) })
	<-req.Context().Done()
	rt.endOnce.Do(func() { close(rt.returned) })
	return nil, req.Context().Err()
}

// installCancelRecorderClient 把全局 httpClient 换成指定 transport
// （mainCodeVendor 经 rootTransport 桥接它，activeSocks5="" 直连该 client）。
func installCancelRecorderClient(t *testing.T, tr http.RoundTripper) *cancelRecorderRT {
	t.Helper()
	rt := &cancelRecorderRT{}

	oldHTTPClient := httpClient
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldActiveSocks5 := activeSocks5
	oldSocks5Client := socks5Client
	oldSocks5ClientAddr := socks5ClientAddr

	if tr != nil {
		httpClient = &http.Client{Transport: tr}
	} else {
		httpClient = &http.Client{Transport: rt}
	}

	modelMu.Lock()
	modelsCache = []ModelInfo{{ID: "m-shared"}}
	goModelsCache = nil
	modelMu.Unlock()
	socks5Mu.Lock()
	activeSocks5 = ""
	socks5Client = nil
	socks5ClientAddr = ""
	socks5Mu.Unlock()

	mainCodeVendor().SetSession("test-version", "ses_cancel", "proj_cancel")

	t.Cleanup(func() {
		httpClient = oldHTTPClient
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelMu.Unlock()
		socks5Mu.Lock()
		activeSocks5 = oldActiveSocks5
		socks5Client = oldSocks5Client
		socks5ClientAddr = oldSocks5ClientAddr
		socks5Mu.Unlock()
	})
	return rt
}

// TestCallOpenCodeAPICanceledCtx 已取消的 ctx 调非流式适配层：快速返回错误，
// 下游 RoundTrip 观察到取消（H1 穿透）。
func TestCallOpenCodeAPICanceledCtx(t *testing.T) {
	rt := installCancelRecorderClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, status, _, _, err := callOpenCodeAPI(ctx, []byte(`{"model":"m-shared","messages":[]}`), "m-shared", UpstreamAuth{Mode: AuthRoutePublic})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0（取消后无上游响应）", status)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %v, want fast return on cancelled ctx", elapsed)
	}
	if atomic.LoadInt32(&rt.cancelled) == 0 {
		t.Fatalf("downstream RoundTrip never observed cancellation (hits=%d)", atomic.LoadInt32(&rt.hits))
	}
}

// TestCallOpenCodeAPIStreamCanceledCtx 已取消的 ctx 调流式适配层：快速返回错误，
// 下游 RoundTrip 观察到取消（H1 穿透）。
func TestCallOpenCodeAPIStreamCanceledCtx(t *testing.T) {
	rt := installCancelRecorderClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	rc, status, _, _, err := callOpenCodeAPIStream(ctx, []byte(`{"model":"m-shared","messages":[],"stream":true}`), "m-shared", UpstreamAuth{Mode: AuthRoutePublic})
	elapsed := time.Since(start)
	if rc != nil {
		rc.Close()
	}
	if err == nil {
		t.Fatal("err = nil, want cancel error")
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0（取消后无上游响应）", status)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %v, want fast return on cancelled ctx", elapsed)
	}
	if atomic.LoadInt32(&rt.cancelled) == 0 {
		t.Fatalf("downstream RoundTrip never observed cancellation (hits=%d)", atomic.LoadInt32(&rt.hits))
	}
}

// TestCallOpenCodeAPICancelMidFlight 请求挂起期间取消：适配层快速返回，
// 挂起 RoundTrip 收到取消并退出（无泄漏）。
func TestCallOpenCodeAPICancelMidFlight(t *testing.T) {
	hang := newHangRT2()
	installCancelRecorderClient(t, hang)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, _, err := callOpenCodeAPI(ctx, []byte(`{"model":"m-shared","messages":[]}`), "m-shared", UpstreamAuth{Mode: AuthRoutePublic})
		done <- err
	}()
	// 等请求进入挂起 RoundTrip 后取消。
	select {
	case <-hang.started:
	case <-time.After(2 * time.Second):
		t.Fatal("request never entered hanging RoundTrip")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not return after ctx cancel (leak)")
	}
	select {
	case <-hang.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("hanging RoundTrip goroutine still running (leak)")
	}
}
