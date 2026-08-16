// 请求治理回归测试（移植自 PR #9，按 main 现状适配）：
// 重试预算封顶、目录刷新节流（防 /v1/models 冷启动惊群）。
package main

import (
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// ---------- 重试预算封顶 ----------

func TestMaxRouteRetriesCapped(t *testing.T) {
	// 即使代理池很大，重试预算也必须封顶（防止上游故障时重试风暴）。
	socks5Mu.Lock()
	old := socks5Proxies
	socks5Proxies = make([]Socks5Proxy, 30)
	for i := range socks5Proxies {
		socks5Proxies[i] = Socks5Proxy{Addr: "127.0.0.1:1"}
	}
	socks5Mu.Unlock()
	defer func() {
		socks5Mu.Lock()
		socks5Proxies = old
		socks5Mu.Unlock()
	}()

	if got := maxRouteRetries(); got != maxUpstreamRetries {
		t.Fatalf("maxRouteRetries = %d, want cap %d", got, maxUpstreamRetries)
	}
}

// ---------- 目录刷新节流（防 /v1/models 冷启动惊群） ----------

func TestRefreshModelCatalogIfDueThrottle(t *testing.T) {
	snapshotCatalogGen(t)
	oldLast := catalogLastRefresh
	t.Cleanup(func() {
		catalogRefreshMu.Lock()
		catalogLastRefresh = oldLast
		catalogRefreshMu.Unlock()
	})

	fake := &catalogFakeVendor{id: "fake", models: []contract.Model{{ID: "f1", Provider: "fake"}}}
	agg := aggregator.New()
	agg.Register(fake)
	globalAgg = agg

	// 强制版（启动/厂商重建路径）：无条件刷新，代际+1。
	refreshModelCatalog()
	if got := catalogGen.Load(); got != 1 {
		t.Fatalf("强制刷新后代际 = %d, want 1", got)
	}

	// 刚刷新过：IfDue 版应跳过（代际不变）——上游故障时不被每请求重打。
	refreshModelCatalogIfDue()
	if got := catalogGen.Load(); got != 1 {
		t.Fatalf("10s 内 IfDue 应跳过，代际 = %d, want 仍为 1", got)
	}

	// 超过最小间隔：IfDue 版应执行（代际+1）。
	catalogRefreshMu.Lock()
	catalogLastRefresh = time.Now().Add(-catalogRefreshMinGap - time.Second)
	catalogRefreshMu.Unlock()
	refreshModelCatalogIfDue()
	if got := catalogGen.Load(); got != 2 {
		t.Fatalf("超过最小间隔后 IfDue 应执行，代际 = %d, want 2", got)
	}
}
