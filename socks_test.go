package main

// S3 质量自愈：坏池分类型恢复（链路类自愈 / 账号类永久禁用）。
// 链路类（如 503 服务不可用）badUntil 到期放行 1 次探测，成功清状态 / 失败重新坏池；
// 账号类（401/402/429）badUntil 恒零永久禁用，任何路径都不可自动试探。

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// resetS3Health 复位坏池/熔断/质量全局状态（socks_test 内部用，避免测试间污染）。
func resetS3Health() {
	poolPerfMode.Store(true)
	badPoolResetSec.Store(300)
	poolBreakerThreshold.Store(3)
	poolHalfOpenIntervalSec.Store(60)
	poolQualityPath = ""
	poolQualityCache = nil
	poolQualityStamp = time.Time{}
	poolQualityLoaded = time.Time{}
	poolFeedback = map[string][]poolFbSample{}
	poolBreakers = map[string]*poolBreaker{}
	proxyInFlightMu.Lock()
	proxyInFlight = map[string]*atomic.Int64{}
	proxyInFlightMu.Unlock()
	socks5HealthMu.Lock()
	socks5Health = map[string]socks5HealthState{}
	socks5HealthMu.Unlock()
}

// expireLinkBadPool 把链路类坏池到期时间拨到过去（模拟 bad_pool_reset_sec 已过）。
func expireLinkBadPool(addr string) {
	socks5HealthMu.Lock()
	st := socks5Health[addr]
	st.badUntil = time.Now().Add(-time.Second)
	st.badProbeUsed = false
	socks5Health[addr] = st
	socks5HealthMu.Unlock()
}

// badPoolStateOf 读取健康表状态（持锁拷贝）。
func badPoolStateOf(addr string) socks5HealthState {
	socks5HealthMu.Lock()
	defer socks5HealthMu.Unlock()
	return socks5Health[addr]
}

// 分类函数：401/402/429 账号类；5xx（如 503/502/500）链路类。
func TestBadPoolAccountClass(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		if !badPoolAccountClass(code) {
			t.Fatalf("code %d must be account-class", code)
		}
	}
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusInternalServerError, http.StatusGatewayTimeout} {
		if badPoolAccountClass(code) {
			t.Fatalf("code %d must be link-class", code)
		}
	}
}

// 链路类（503）连续 3 次进坏池：badUntil 有值（可自动恢复），badProbeUsed 复位。
func TestLinkClassBadPoolHasExpiry(t *testing.T) {
	resetS3Health()
	bad := mkProxy(28301)
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	}
	st := badPoolStateOf(bad.Addr)
	if st.badReason == "" || !strings.Contains(st.badReason, "503") {
		t.Fatalf("503 x3 should enter bad pool, got %q", st.badReason)
	}
	if st.badUntil.IsZero() {
		t.Fatal("link-class bad pool must set badUntil (auto-recoverable)")
	}
	if d := time.Until(st.badUntil); d < 299*time.Second || d > 301*time.Second {
		t.Fatalf("badUntil = %v from now, want ~300s", d)
	}
	if st.badProbeUsed {
		t.Fatal("entering bad pool must reset probe quota (badProbeUsed=false)")
	}
}

// 账号类（401/402/429）连续 3 次进坏池：badUntil 恒零（永久禁用）。
func TestAccountClassBadPoolPermanent(t *testing.T) {
	resetS3Health()
	for _, code := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		bad := mkProxy(uint16(28310 + code%100))
		for i := 0; i < 3; i++ {
			markSocks5Result(bad.Addr, code, nil)
		}
		st := badPoolStateOf(bad.Addr)
		if st.badReason == "" {
			t.Fatalf("code %d x3 should enter bad pool", code)
		}
		if !st.badUntil.IsZero() {
			t.Fatalf("account-class (code %d) badUntil must stay zero, got %v", code, st.badUntil)
		}
		if st.badProbeUsed {
			t.Fatalf("account-class (code %d) must never consume probe quota", code)
		}
	}
}

// 链路类坏池未到期：不放行（不消费配额），选其它健康节点。
func TestLinkClassBadPoolNotExpiredSkipped(t *testing.T) {
	resetS3Health()
	bad := mkProxy(28305)
	other := mkProxy(28306)
	proxies := []Socks5Proxy{bad, other}
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	}
	if p := pickWeightedProxy(proxies, 0); p.Addr != other.Addr {
		t.Fatalf("unexpired link bad pool picked %s, want %s", p.Addr, other.Addr)
	}
	if badPoolStateOf(bad.Addr).badProbeUsed {
		t.Fatal("unexpired bad pool must not consume probe quota")
	}
}

// 链路类坏池到期：放行 1 次探测 → 成功（2xx）清 badReason 状态。
func TestLinkClassBadPoolExpiryProbeSuccess(t *testing.T) {
	resetS3Health()
	bad := mkProxy(28301)
	other := mkProxy(28302)
	proxies := []Socks5Proxy{bad, other}
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	}
	expireLinkBadPool(bad.Addr)

	// P2 路径：到期放行 1 次探测（最多一个节点）。
	if p := pickWeightedProxy(proxies, 0); p.Addr != bad.Addr {
		t.Fatalf("pickWeightedProxy=%s, want bad-pool probe %s", p.Addr, bad.Addr)
	}
	// 配额已消费：第二次选择不再放行（回落到其它节点）。
	if p := pickWeightedProxy(proxies, 0); p.Addr != other.Addr {
		t.Fatalf("second pick=%s, want %s (probe quota consumed)", p.Addr, other.Addr)
	}

	// 探测成功（2xx）→ 清坏池（既有 TestSuccessClearsBadPool 语义保持）。
	markSocks5Result(bad.Addr, http.StatusOK, nil)
	socks5HealthMu.Lock()
	_, ok := socks5Health[bad.Addr]
	socks5HealthMu.Unlock()
	if ok {
		t.Fatal("2xx must clear bad pool entry entirely")
	}
}

// 链路类坏池到期：放行 1 次探测 → 失败重新坏池（重置计时）。
// 覆盖两类失败：坏状态码（503）与链路错误（requestErr）。
func TestLinkClassBadPoolExpiryProbeFailReset(t *testing.T) {
	resetS3Health()
	bad := mkProxy(28303)
	other := mkProxy(28304)
	proxies := []Socks5Proxy{bad, other}
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	}

	// 场景 1：探测失败为坏状态码（503）→ 重新坏池重置计时。
	expireLinkBadPool(bad.Addr)
	if p := pickWeightedProxy(proxies, 0); p.Addr != bad.Addr {
		t.Fatalf("probe pick=%s, want %s", p.Addr, bad.Addr)
	}
	markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	st := badPoolStateOf(bad.Addr)
	if st.badReason == "" {
		t.Fatal("bad pool must persist after probe failure (503)")
	}
	if d := time.Until(st.badUntil); d < 299*time.Second {
		t.Fatalf("badUntil after 503 probe failure = %v from now, want >= ~300s", d)
	}
	if st.badProbeUsed {
		t.Fatal("re-bad must reset probe quota")
	}

	// 场景 2：探测失败为链路错误（连接失败/超时）→ 同样重新坏池重置计时。
	expireLinkBadPool(bad.Addr)
	if p := pickWeightedProxy(proxies, 1); p.Addr != bad.Addr {
		t.Fatalf("probe pick2=%s, want %s", p.Addr, bad.Addr)
	}
	markSocks5Result(bad.Addr, 0, io.EOF)
	st = badPoolStateOf(bad.Addr)
	if st.badReason == "" || st.badUntil.IsZero() {
		t.Fatal("link-error probe failure must keep bad pool and reset badUntil")
	}
	if d := time.Until(st.badUntil); d < 299*time.Second {
		t.Fatalf("badUntil after link-error probe failure = %v from now, want >= ~300s", d)
	}
}

// 账号类坏池永不自动放行：单发（P2 / 基线）与竞速路径均跳过，badProbeUsed 保持 false
// （断言无试探）。
func TestAccountClassNeverAutoProbed(t *testing.T) {
	resetS3Health()
	bad := mkProxy(28401)
	other := mkProxy(28402)
	proxies := []Socks5Proxy{bad, other}
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusTooManyRequests, nil)
	}
	st := badPoolStateOf(bad.Addr)
	if st.badReason == "" || !st.badUntil.IsZero() {
		t.Fatal("precondition: 429 must be permanent bad pool")
	}

	// P2 路径：即使节点"已到期"（账号类恒零，模拟不了过期），永不放行。
	if p := pickWeightedProxy(proxies, 0); p.Addr == bad.Addr {
		t.Fatal("account-class bad pool must never be auto-released (weighted)")
	}
	// 基线路径。
	poolPerfMode.Store(false)
	if p := pickHealthyProxy(proxies, 0); p.Addr == bad.Addr {
		t.Fatal("account-class bad pool must never be auto-released (baseline)")
	}
	poolPerfMode.Store(true)
	// 竞速路径：候选不足也不放行账号类。
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{bad, other}
	socks5Mu.Unlock()
	if got := raceCandidates(2); len(got) != 0 {
		t.Fatalf("raceCandidates=%+v, want nil (account-class never released)", got)
	}
	if badPoolStateOf(bad.Addr).badProbeUsed {
		t.Fatal("account-class must never consume probe quota (no auto probing)")
	}

	// 401/402 同样断言。
	for _, code := range []int{http.StatusUnauthorized, http.StatusPaymentRequired} {
		a := mkProxy(uint16(28410 + code%10))
		proxies2 := []Socks5Proxy{a, other}
		for i := 0; i < 3; i++ {
			markSocks5Result(a.Addr, code, nil)
		}
		if p := pickWeightedProxy(proxies2, 0); p.Addr == a.Addr {
			t.Fatalf("code %d must never be auto-released", code)
		}
	}
}

// 熔断半开与链路类坏池探针并存：优先熔断半开，坏池配额不被浪费（不消费）。
func TestPickWeightedProxyHalfOpenBeatsBadPoolProbe(t *testing.T) {
	resetS3Health()
	halfNode := mkProxy(28420)
	badNode := mkProxy(28421)
	proxies := []Socks5Proxy{halfNode, badNode}
	for i := 0; i < 3; i++ {
		applyPoolResult(halfNode.Addr, 503, nil)
		markSocks5Result(badNode.Addr, http.StatusServiceUnavailable, nil)
	}
	poolBreakerMu.Lock()
	poolBreakers[halfNode.Addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()
	expireLinkBadPool(badNode.Addr)

	if p := pickWeightedProxy(proxies, 0); p.Addr != halfNode.Addr {
		t.Fatalf("picked %s, want half-open %s", p.Addr, halfNode.Addr)
	}
	if badPoolStateOf(badNode.Addr).badProbeUsed {
		t.Fatal("bad-pool probe quota must not be consumed when half-open wins")
	}
}

// 基线模式（poolPerfMode=false）：链路类坏池到期同样放行探测，成功清状态。
func TestBaselineBadPoolProbeRelease(t *testing.T) {
	resetS3Health()
	poolPerfMode.Store(false)
	bad := mkProxy(28407)
	other := mkProxy(28408)
	proxies := []Socks5Proxy{bad, other}
	for i := 0; i < 3; i++ {
		markSocks5Result(bad.Addr, http.StatusServiceUnavailable, nil)
	}
	expireLinkBadPool(bad.Addr)

	// 轮询游标落在坏池节点上 → 放行探测。
	if p := pickHealthyProxy(proxies, 0); p.Addr != bad.Addr {
		t.Fatalf("baseline probe pick=%s, want %s", p.Addr, bad.Addr)
	}
	// 成功 → 清坏池。
	markSocks5Result(bad.Addr, http.StatusOK, nil)
	socks5HealthMu.Lock()
	_, ok := socks5Health[bad.Addr]
	socks5HealthMu.Unlock()
	if ok {
		t.Fatal("2xx must clear bad pool entry entirely")
	}
}