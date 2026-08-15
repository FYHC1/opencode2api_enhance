package main

// CONC-2（H3）连接复用：clientForProxy 按代理地址缓存 http.Client，
// RR 单发与竞速候选共用同一缓存。

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// clearSocksClientCache 清空客户端缓存（隔离测试间缓存状态）。
func clearSocksClientCache() {
	socks5CacheMu.Lock()
	socks5ClientCache = map[proxyCacheKey]*http.Client{}
	socks5CacheMu.Unlock()
}

// 同 addr 两次取同一 client 指针（连接池复用）。
func TestClientForProxySameAddrSameClient(t *testing.T) {
	clearSocksClientCache()
	defer clearSocksClientCache()

	p := mkProxy(28601)
	c1 := clientForProxy(p)
	c2 := clientForProxy(p)
	if c1 != c2 {
		t.Fatal("同 addr 两次取 client 应返回同一指针（连接池复用）")
	}
	if c1.Timeout != 300*time.Second {
		t.Fatalf("cached client Timeout = %v, want 300s", c1.Timeout)
	}
}

// 代理配置变更（模拟 applyConfig 整体失效）后重建 client。
func TestClientForProxyRebuildAfterInvalidate(t *testing.T) {
	clearSocksClientCache()
	defer clearSocksClientCache()

	p := mkProxy(28602)
	c1 := clientForProxy(p)
	// 模拟 applyConfig 失效：清空整个缓存。
	clearSocksClientCache()
	c2 := clientForProxy(p)
	if c1 == c2 {
		t.Fatal("缓存失效后同 addr 应重建新 client")
	}
	if c2 != clientForProxy(p) {
		t.Fatal("重建后的 client 应再次进入缓存")
	}
}

// 同 addr 不同凭据不串：缓存键含 Username/Password。
func TestClientForProxyKeyIncludesCredentials(t *testing.T) {
	clearSocksClientCache()
	defer clearSocksClientCache()

	p := Socks5Proxy{Addr: "127.0.0.1:28603", Username: "u1", Password: "pw1"}
	q := Socks5Proxy{Addr: "127.0.0.1:28603", Username: "u2", Password: "pw2"}
	c1 := clientForProxy(p)
	c2 := clientForProxy(q)
	if c1 == c2 {
		t.Fatal("同 addr 不同凭据不应共用同一 client")
	}
	if c3 := clientForProxy(Socks5Proxy{Addr: "127.0.0.1:28603", Username: "u1", Password: "pw1"}); c3 != c1 {
		t.Fatal("同 addr 同凭据应命中缓存")
	}
}

// 流式浅拷贝去 Timeout 不污染缓存中的 client（含真实流式路径）。
func TestStreamingShallowCopyNotPolluteCache(t *testing.T) {
	clearSocksClientCache()
	defer clearSocksClientCache()

	p := mkProxy(28604)
	socks5Mu.Lock()
	oldActive, oldProxies := activeSocks5, socks5Proxies
	activeSocks5 = p.Addr
	socks5Proxies = []Socks5Proxy{p}
	socks5Mu.Unlock()
	t.Cleanup(func() {
		socks5Mu.Lock()
		activeSocks5, socks5Proxies = oldActive, oldProxies
		socks5Mu.Unlock()
	})

	cached := clientForProxy(p)
	sc := *cached
	sc.Timeout = 0
	if cached.Timeout != 300*time.Second {
		t.Fatal("浅拷贝修改 Timeout 污染了缓存中的 client")
	}

	// 真实流式路径：返回 Timeout=0 的浅拷贝，缓存 client 不受影响。
	sc2, addr := getStreamingHTTPClientForTierWithProxy(TierFree)
	if addr != p.Addr {
		t.Fatalf("streaming addr = %q, want %q", addr, p.Addr)
	}
	if sc2.Timeout != 0 {
		t.Fatalf("streaming client Timeout = %v, want 0", sc2.Timeout)
	}
	if got := clientForProxy(p); got.Timeout != 300*time.Second {
		t.Fatal("流式路径污染了缓存：缓存 client 的 Timeout 被改")
	}
}

// RR 单发路径与 clientForProxy 共用同一缓存（不再每请求新建 Transport）。
func TestRoundRobinSharesClientCache(t *testing.T) {
	clearSocksClientCache()
	defer clearSocksClientCache()

	oldRoute := routeMode.Load().(string)
	routeMode.Store("round_robin")
	defer routeMode.Store(oldRoute)

	p1 := mkProxy(28605)
	p2 := mkProxy(28606)
	socks5Mu.Lock()
	oldActive, oldProxies, oldRR := activeSocks5, socks5Proxies, socks5RRIndex
	activeSocks5 = socks5RR
	socks5Proxies = []Socks5Proxy{p1, p2}
	socks5Mu.Unlock()
	t.Cleanup(func() {
		socks5Mu.Lock()
		activeSocks5, socks5Proxies = oldActive, oldProxies
		socks5Mu.Unlock()
		atomic.StoreUint32(&socks5RRIndex, oldRR)
	})

	c1, a1 := getHTTPClientWithProxy()
	c2, a2 := getHTTPClientWithProxy()
	if a1 == a2 {
		t.Fatal("round_robin 两次请求应命中不同代理")
	}
	// RR 返回的 client 与按 addr 直取缓存的是同一指针。
	if c1 != clientForProxy(Socks5Proxy{Addr: a1}) {
		t.Fatal("RR 返回的 client 应来自 clientForProxy 的同一缓存")
	}
	if c2 != clientForProxy(Socks5Proxy{Addr: a2}) {
		t.Fatal("RR 返回的 client 应来自 clientForProxy 的同一缓存")
	}
}
