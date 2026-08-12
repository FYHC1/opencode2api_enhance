package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// resetPoolPerfState 复位 P2 全局状态（避免测试间污染）。
func resetPoolPerfState() {
	poolPerfMode = true
	poolBreakerThreshold = 3
	poolHalfOpenIntervalSec = 60
	poolQualityPath = ""
	poolQualityCache = nil
	poolQualityStamp = time.Time{}
	poolQualityLoaded = time.Time{}
	poolFeedback = map[string][]poolFbSample{}
	poolBreakers = map[string]*poolBreaker{}
	socks5Health = map[string]socks5HealthState{}
	socks5HealthMu.Lock()
	socks5Health = map[string]socks5HealthState{}
	socks5HealthMu.Unlock()
}

// writePoolQualityFile 写一份质量文件（P1 持久化格式）。
func writePoolQualityFile(t *testing.T, entries map[string]struct {
	Port  uint16
	Score int
	Level string
}) string {
	t.Helper()
	data := `[`
	first := true
	for _, e := range entries {
		if !first {
			data += ","
		}
		first = false
		data += `{"singbox_port":` + strconv.Itoa(int(e.Port)) + `,"score":` + strconv.Itoa(e.Score) + `,"level":"` + e.Level + `"}`
	}
	data += `]`
	path := filepath.Join(t.TempDir(), "pool_quality.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mkProxy(port uint16) Socks5Proxy {
	return Socks5Proxy{Addr: "127.0.0.1:" + strconv.Itoa(int(port)), Name: "p" + strconv.Itoa(int(port))}
}

// ---- 加权路由：质量分排序 / 降权 / 跳过 / 剔除 ----

func TestPickWeightedProxyQualitySort(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102), mkProxy(28103), mkProxy(28104)}
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 90, "healthy"},
		"b": {28102, 70, "degraded"},
		"c": {28103, 55, "flaky"},
		"d": {28104, 10, "down"},
	})

	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28101" {
		t.Fatalf("picked %s, want 28101 (highest healthy)", got.Addr)
	}
}

// degraded 即使分数更高也排在 healthy 之后。
func TestPickWeightedProxyDegradedWeight(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 85, "degraded"},
		"b": {28102, 60, "healthy"},
	})

	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28102" {
		t.Fatalf("picked %s, want 28102 (healthy tier wins)", got.Addr)
	}
}

// 坏池节点彻底剔除，即使质量分高。
func TestPickWeightedProxyBadPool(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 100, "healthy"},
		"b": {28102, 80, "healthy"},
	})
	socks5HealthMu.Lock()
	socks5Health["127.0.0.1:28101"] = socks5HealthState{badReason: "429：最大额度上限", badCount: 3}
	socks5HealthMu.Unlock()

	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28102" {
		t.Fatalf("picked %s, want 28102 (bad pool skipped)", got.Addr)
	}
}

// 全池冷却 → 回退冷却最早结束的节点（与基线兜底一致）。
func TestPickWeightedProxyAllCoolingFallback(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	now := time.Now()
	socks5HealthMu.Lock()
	socks5Health["127.0.0.1:28101"] = socks5HealthState{until: now.Add(90 * time.Second)}
	socks5Health["127.0.0.1:28102"] = socks5HealthState{until: now.Add(20 * time.Second)}
	socks5HealthMu.Unlock()

	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28102" {
		t.Fatalf("picked %s, want 28102 (earliest cooldown)", got.Addr)
	}
}

// ---- 熔断状态机：open / half-open / closed ----

func TestBreakerOpensAfterThreshold(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold = 3
	poolHalfOpenIntervalSec = 60
	addr := "127.0.0.1:28101"

	// 连续 2 次失败不熔断。
	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	if breakerState(addr) != "closed" {
		t.Fatal("2 failures should not open breaker")
	}
	// 第 3 次 → open。
	applyPoolResult(addr, 503, nil)
	if breakerState(addr) != "open" {
		t.Fatal("3rd failure should open breaker")
	}
	// open 期间持续剔除。
	if breakerState(addr) != "open" {
		t.Fatal("breaker should stay open before half-open interval")
	}
}

func TestBreakerHalfOpenThenRecover(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold = 2
	poolHalfOpenIntervalSec = 60
	addr := "127.0.0.1:28101"

	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	// 手动把到期时间拨到过去，模拟半开窗口到达。
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()

	if got := breakerState(addr); got != "halfopen" {
		t.Fatalf("state=%s, want halfopen", got)
	}
	// 半开探测成功 → 恢复（closed）。
	applyPoolResult(addr, 200, nil)
	if got := breakerState(addr); got != "closed" {
		t.Fatalf("after recovery state=%s, want closed", got)
	}
}

func TestBreakerHalfOpenFailKeepsOpen(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold = 2
	poolHalfOpenIntervalSec = 60
	addr := "127.0.0.1:28101"

	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()

	// 半开探测失败 → 重新 open（计数继续累计）。
	applyPoolResult(addr, 503, nil)
	if got := breakerState(addr); got != "open" {
		t.Fatalf("after failed probe state=%s, want open", got)
	}
}

// 半开放行：到期后 pick 放行 1 个熔断节点。
func TestPickWeightedProxyHalfOpenProbe(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 20, "degraded"},
		"b": {28102, 90, "healthy"},
	})
	// 28101 熔断到期（可半开放行），28102 健康。
	poolBreakerMu.Lock()
	poolBreakers["127.0.0.1:28101"] = &poolBreaker{failures: 3, openUntil: time.Now().Add(-time.Second)}
	poolBreakerMu.Unlock()

	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28101" {
		t.Fatalf("picked %s, want 28101 (half-open probe)", got.Addr)
	}
	// 半开已消费 → 下次不再放行，回到健康节点。
	got2 := pickWeightedProxy(proxies, 0)
	if got2.Addr != "127.0.0.1:28102" {
		t.Fatalf("second pick=%s, want 28102 (probe used)", got2.Addr)
	}
}

// ---- 请求回填：成败对反馈窗口的影响 ----

func TestPoolFeedbackReflectsResults(t *testing.T) {
	resetPoolPerfState()
	addr := "127.0.0.1:28101"
	recordPoolFeedback(addr, true)
	recordPoolFeedback(addr, true)
	recordPoolFeedback(addr, false)
	if got := poolFeedbackScore(addr); got != 66 {
		t.Fatalf("feedback score=%d, want 66 (2/3)", got)
	}
}

// ---- 性能模式开关：关闭时走基线逻辑 ----

func TestPoolPerfModeOffBaseline(t *testing.T) {
	resetPoolPerfState()
	poolPerfMode = false
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	now := time.Now()
	socks5HealthMu.Lock()
	socks5Health["127.0.0.1:28101"] = socks5HealthState{until: now.Add(20 * time.Second)}
	socks5HealthMu.Unlock()

	// 基线逻辑：28101 冷却中 → 跳过，返回 28102。
	got := pickHealthyProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28102" {
		t.Fatalf("baseline picked %s, want 28102", got.Addr)
	}
}

// ---- 质量文件加载：正常 / 损坏 ----

func TestLoadPoolQualityCache(t *testing.T) {
	resetPoolPerfState()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 90, "healthy"},
		"b": {28102, 30, "flaky"},
	})
	loadPoolQualityCache()
	if _, ok := poolQualityOf("127.0.0.1:28101"); !ok {
		t.Fatal("28101 missing from cache")
	}
	e, _ := poolQualityOf("127.0.0.1:28102")
	if e.Score != 30 || e.Level != "flaky" {
		t.Fatalf("28102 entry=%+v, want score 30 flaky", e)
	}
	if _, ok := poolQualityOf("127.0.0.1:9999"); ok {
		t.Fatal("unknown addr should miss")
	}
}

func TestLoadPoolQualityCacheCorrupt(t *testing.T) {
	resetPoolPerfState()
	path := filepath.Join(t.TempDir(), "pool_quality.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	poolQualityPath = path
	loadPoolQualityCache() // 不应 panic
	if poolQualityCache != nil {
		t.Fatalf("corrupt file cache=%v, want nil", poolQualityCache)
	}
}

// TestRaceCandidates 请求级竞速候选：质量优先排序、n 上限、n<=1 不竞速、down/flaky 剔除。
func TestRaceCandidates(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28101), mkProxy(28102), mkProxy(28103), mkProxy(28104)}
	socks5Mu.Unlock()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 95, "healthy"},
		"b": {28102, 70, "degraded"},
		"c": {28103, 55, "flaky"},
		"d": {28104, 10, "down"},
	})

	// down/flaky 剔除，健康优先：expect [28101(95 healthy), 28102(70 degraded)]。
	got := raceCandidates(2)
	if len(got) != 2 ||
		got[0].Addr != "127.0.0.1:28101" ||
		got[1].Addr != "127.0.0.1:28102" {
		t.Fatalf("raceCandidates(2)=%+v, want [28101 28102]", got)
	}

	// n 上限生效；n<=1 不竞速。
	if len(raceCandidates(1)) != 0 {
		t.Fatal("n<=1 should not race")
	}
	four := raceCandidates(4)
	if len(four) != 2 {
		t.Fatalf("raceCandidates(4)=%d, want 2 (flaky/down skipped)", len(four))
	}
}
