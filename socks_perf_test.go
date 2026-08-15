package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// resetPoolPerfState 复位 P2 全局状态（避免测试间污染）。
func resetPoolPerfState() {
	poolPerfMode.Store(true)
	poolBreakerThreshold.Store(3)
	poolHalfOpenIntervalSec.Store(60)
	poolQualityPath = ""
	poolQualityCache = nil
	poolQualityStamp = time.Time{}
	poolQualityLoaded = time.Time{}
	poolFeedback = map[string][]poolFbSample{}
	poolBreakers = map[string]*poolBreaker{}
	poolRacePressureLow.Store(0.5)
	poolRacePressureHigh.Store(1.0)
	racePressureFn = racePressure
	proxyInFlightMu.Lock()
	proxyInFlight = map[string]*atomic.Int64{}
	proxyInFlightMu.Unlock()
	setRaceRandSeed(42) // 固定候选扰动源，保证排序确定性
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
	poolBreakerThreshold.Store(3)
	poolHalfOpenIntervalSec.Store(60)
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
	poolBreakerThreshold.Store(2)
	poolHalfOpenIntervalSec.Store(60)
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
	poolBreakerThreshold.Store(2)
	poolHalfOpenIntervalSec.Store(60)
	addr := "127.0.0.1:28101"

	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()

	// 半开放行探针被消费，随后探测失败 → 重新 open（重新计时）。
	if got := breakerState(addr); got != "halfopen" {
		t.Fatalf("state=%s, want halfopen", got)
	}
	applyPoolResult(addr, 503, nil)
	if got := breakerState(addr); got != "open" {
		t.Fatalf("after failed probe state=%s, want open", got)
	}
}

// G8：跳闸瞬间已在途的请求陆续失败，不应把半开恢复窗口向后顺延。
func TestBreakerLateFailuresDoNotPushOpenUntil(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold.Store(3)
	poolHalfOpenIntervalSec.Store(60)
	addr := "127.0.0.1:28101"

	// 第 3 次失败触发 close→open 跳变。
	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	poolBreakerMu.Lock()
	trippedUntil := poolBreakers[addr].openUntil
	poolBreakerMu.Unlock()
	if trippedUntil.IsZero() {
		t.Fatal("3rd failure should open breaker")
	}

	// 跳闸后在途的迟到失败：只累计计数，openUntil 不得顺延。
	for i := 0; i < 5; i++ {
		applyPoolResult(addr, 503, nil)
	}
	poolBreakerMu.Lock()
	lateFailures := poolBreakers[addr].failures
	lateUntil := poolBreakers[addr].openUntil
	poolBreakerMu.Unlock()
	if lateFailures <= 3 {
		t.Fatalf("failures=%d, want > 3 (late failures must count)", lateFailures)
	}
	if !lateUntil.Equal(trippedUntil) {
		t.Fatalf("late failures moved openUntil %v -> %v, want unchanged", trippedUntil, lateUntil)
	}
	// 到期前仍剔除（open）。
	if got := breakerState(addr); got != "open" {
		t.Fatalf("state=%s, want open", got)
	}
}

// G8：半开探测失败 → 重推 openUntil（重新计时）并复位 probeUsed。
func TestBreakerHalfOpenFailRearmsOpenUntil(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold.Store(2)
	poolHalfOpenIntervalSec.Store(60)
	addr := "127.0.0.1:28101"

	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	// 半开窗口到达并消费探针。
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()
	if got := breakerState(addr); got != "halfopen" {
		t.Fatalf("state=%s, want halfopen", got)
	}

	// 探测失败 → 重新 open：probeUsed 复位、openUntil 重新计时。
	applyPoolResult(addr, 503, nil)
	poolBreakerMu.Lock()
	b := poolBreakers[addr]
	rearmed := !b.openUntil.IsZero() && !b.probeUsed
	poolBreakerMu.Unlock()
	if !rearmed {
		t.Fatalf("probe failure must re-arm openUntil and reset probeUsed")
	}
	if got := breakerState(addr); got != "open" {
		t.Fatalf("state=%s, want open after failed probe", got)
	}
}

// G33：熔断已 open 后阈值热重载调高越过 failures → 半开探测失败仍重推
// （不被新阈值钳制，不永久剔除直到重启）。
func TestBreakerThresholdRaisedProbeFailRearms(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold.Store(3)
	poolHalfOpenIntervalSec.Store(60)
	addr := "127.0.0.1:28101"

	// 连续 3 次失败 → open（failures=3）。
	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	applyPoolResult(addr, 503, nil)
	if got := breakerState(addr); got != "open" {
		t.Fatalf("state=%s, want open", got)
	}
	// 阈值热重载调高越过当前 failures（模拟 applyConfig：3→10）。
	poolBreakerThreshold.Store(10)
	// 半开窗口到达并消费探针。
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()
	if got := breakerState(addr); got != "halfopen" {
		t.Fatalf("state=%s, want halfopen", got)
	}
	// 半开探测失败（failures=4 < 新阈值 10）：仍必须重推 openUntil。
	applyPoolResult(addr, 503, nil)
	poolBreakerMu.Lock()
	b := poolBreakers[addr]
	rearmed := !b.openUntil.IsZero() && !b.probeUsed && b.tripped
	poolBreakerMu.Unlock()
	if !rearmed {
		t.Fatalf("raised-threshold probe failure must re-arm openUntil, got %+v", b)
	}
	if got := breakerState(addr); got != "open" {
		t.Fatalf("state=%s, want open after rearm", got)
	}
}

// G33：阈值热重载调低且 failures 已越过新阈值（未跳闸）→ 后续失败仍能跳闸
// （failures >= threshold && !tripped 生效，而非 ==threshold 精确相等）。
func TestBreakerThresholdLoweredStillTrips(t *testing.T) {
	resetPoolPerfState()
	poolBreakerThreshold.Store(10)
	poolHalfOpenIntervalSec.Store(60)
	addr := "127.0.0.1:28101"

	// 高阈值 10 下累计 5 次失败：5 < 10，未跳闸。
	for i := 0; i < 5; i++ {
		applyPoolResult(addr, 503, nil)
	}
	if got := breakerState(addr); got != "closed" {
		t.Fatalf("state=%s, want closed under threshold 10", got)
	}
	// 阈值热重载调低到 3（failures=5 已越过新阈值）。
	poolBreakerThreshold.Store(3)
	// 后续失败（failures=6）必须跳闸。
	applyPoolResult(addr, 503, nil)
	if got := breakerState(addr); got != "open" {
		t.Fatalf("lowered-threshold next failure must trip, state=%s", got)
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
	poolPerfMode.Store(false)
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

// S5: 单出口退化——仅 1 个候选/代理时不竞速、不跑加权，直接取该节点（请求层兜底）。
func TestSingleCandidateDegrade(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28201)}
	socks5Mu.Unlock()

	// 竞速：候选不足 2 个 → 不竞速（返回 nil，调用方走默认请求）
	if got := raceCandidates(2); len(got) != 0 {
		t.Fatalf("single proxy should not race, got %+v", got)
	}
	// 加权选择：单出口直接返回该代理
	if p := pickWeightedProxy(socks5Proxies, 0); p.Addr != "127.0.0.1:28201" {
		t.Fatalf("pickWeightedProxy single=%+v", p)
	}
	// 健康选择（性能模式开）：同样退化到直接返回
	if p := pickHealthyProxy(socks5Proxies, 0); p.Addr != "127.0.0.1:28201" {
		t.Fatalf("pickHealthyProxy single=%+v", p)
	}
	// 空池不 panic 且返回零值
	if p := pickHealthyProxy(nil, 0); p.Addr != "" {
		t.Fatalf("empty pool pick=%+v", p)
	}
}

// ---- S1 冷启动不竞速：unknown（无探活样本）节点不进候选 ----

// TestRaceCandidatesUnknownExcluded 冷启动不竞速：无质量记录 / unknown（空窗口）
// 节点不参与竞速候选；全 unknown → nil（退化单发）；探活一轮（有样本）后进入候选。
func TestRaceCandidatesUnknownExcluded(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28101), mkProxy(28102), mkProxy(28103)}
	socks5Mu.Unlock()

	// 场景 1：无质量文件（冷启动，任何节点都无探活样本）→ 不竞速（nil）。
	poolQualityPath = ""
	poolQualityStamp = time.Time{}
	if got := raceCandidates(2); len(got) != 0 {
		t.Fatalf("no quality file: raceCandidates=%+v, want nil (cold start no race)", got)
	}

	// 场景 2：28101 已探活（healthy）→ 进候选；28102 无样本（unknown）与
	// 28103 无记录 → 不进候选。模拟"探活一轮后"只有有探活样本的节点可竞速。
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 90, "healthy"},
		"b": {28102, 100, "unknown"},
	})
	// 重置缓存时间戳，避免 5s 节流/同 mtime 导致第二个文件不重载。
	poolQualityStamp = time.Time{}
	poolQualityLoaded = time.Time{}
	got := raceCandidates(3)
	if len(got) != 1 || got[0].Addr != "127.0.0.1:28101" {
		t.Fatalf("raceCandidates=%+v, want only known healthy 28101", got)
	}

	// 场景 3：全部 unknown → nil（退化单发，不双倍炸上游）。
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 100, "unknown"},
		"b": {28102, 100, "unknown"},
		"c": {28103, 100, "unknown"},
	})
	poolQualityStamp = time.Time{}
	poolQualityLoaded = time.Time{}
	if got := raceCandidates(3); len(got) != 0 {
		t.Fatalf("all unknown: raceCandidates=%+v, want nil", got)
	}
}

// TestPickWeightedProxyUnknownStillRoutable 冷启动单发不受影响：unknown 节点仍可被
// 加权路由选中（不参与竞速 ≠ 彻底剔除，避免冷启动无节点可用）。
func TestPickWeightedProxyUnknownStillRoutable(t *testing.T) {
	resetPoolPerfState()
	proxies := []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 100, "unknown"},
		"b": {28102, 50, "degraded"},
	})
	got := pickWeightedProxy(proxies, 0)
	if got.Addr != "127.0.0.1:28101" {
		t.Fatalf("picked %s, want 28101 (unknown routable in single-flight)", got.Addr)
	}
}

// ---- S5：least-in-flight 均衡 + 候选随机化 ----

// TestRaceCandidatesLeastInflight 候选均衡：in-flight 3/0 → 新请求优先 in-flight=0 的
// 节点（least-in-flight 优先于质量分；两者分数相同排除干扰）。
func TestRaceCandidatesLeastInflight(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28101), mkProxy(28102)}
	socks5Mu.Unlock()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 100, "healthy"},
		"b": {28102, 100, "healthy"},
	})
	// 28101 已有 3 个在途请求，28102 空闲。
	proxyInflightAdd("127.0.0.1:28101", 3)

	got := raceCandidates(2)
	if len(got) != 2 {
		t.Fatalf("raceCandidates=%+v, want 2 candidates", got)
	}
	if got[0].Addr != "127.0.0.1:28102" {
		t.Fatalf("first=%s, want 28102 (least in-flight wins)", got[0].Addr)
	}
}

// TestRaceCandidatesSameInflightScoreOrder 同 in-flight 分数优先：随机扰动（固定 seed）
// 不破坏排序——分差 >3 的候选顺序恒定（扰动幅度 <3 不会翻转）。
func TestRaceCandidatesSameInflightScoreOrder(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28101), mkProxy(28102), mkProxy(28103)}
	socks5Mu.Unlock()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 99, "healthy"},
		"b": {28102, 60, "healthy"},
		"c": {28103, 20, "healthy"},
	})
	// 三者 in-flight 相同（均 0）→ 按质量分降序；连跑多次仍恒定。
	for i := 0; i < 5; i++ {
		setRaceRandSeed(int64(i))
		got := raceCandidates(3)
		if len(got) != 3 {
			t.Fatalf("raceCandidates=%+v, want 3", got)
		}
		want := []string{"127.0.0.1:28101", "127.0.0.1:28102", "127.0.0.1:28103"}
		for j, a := range want {
			if got[j].Addr != a {
				t.Fatalf("iter %d cand[%d]=%s, want %s (score desc, jitter must not flip)", i, j, got[j].Addr, a)
			}
		}
	}
}

// TestRaceCandidatesHighPressureShuffle 高压力（≥2.0）跳过质量排序、纯随机摊开：
// 多个固定 seed 下首元素不再恒为最高分节点（shuffle 生效），候选集保持完整。
func TestRaceCandidatesHighPressureShuffle(t *testing.T) {
	resetPoolPerfState()
	racePressureFn = func() float64 { return 2.5 } // 构造高压（≥2.0）
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28101), mkProxy(28102), mkProxy(28103), mkProxy(28104)}
	socks5Mu.Unlock()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28101, 100, "healthy"},
		"b": {28102, 90, "healthy"},
		"c": {28103, 80, "healthy"},
		"d": {28104, 70, "healthy"},
	})
	all := map[string]bool{
		"127.0.0.1:28101": true, "127.0.0.1:28102": true,
		"127.0.0.1:28103": true, "127.0.0.1:28104": true,
	}
	firsts := map[string]bool{}
	for i := 0; i < 40; i++ {
		setRaceRandSeed(int64(i))
		got := raceCandidates(4)
		if len(got) != 4 {
			t.Fatalf("high pressure raceCandidates=%+v, want all 4", got)
		}
		for _, c := range got {
			if !all[c.Addr] {
				t.Fatalf("unexpected candidate %s", c.Addr)
			}
		}
		firsts[got[0].Addr] = true
	}
	// shuffle 生效：40 次中首元素不只一种（最高分节点不会永远打头）。
	if len(firsts) < 2 {
		t.Fatalf("high pressure should shuffle: first element always %v", firsts)
	}
}

// ---- S3：恢复探测进竞速（候选不足时放行 1 个，候选充足时不选不偷） ----

// 候选不足 → 放行 1 个熔断半开放行节点（open 到期未消费探针的）兜底，恢复探测不饿死。
func TestRaceCandidatesHalfOpenFallback(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28501), mkProxy(28502)}
	socks5Mu.Unlock()
	addr := "127.0.0.1:28501"
	for i := 0; i < 3; i++ {
		applyPoolResult(addr, 503, nil)
	}
	// 拨到期：半开放行窗口到达，探针未消费。
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()

	// 无健康候选（28501 半开、28502 无质量记录）→ 半开放行兜底进候选。
	got := raceCandidates(2)
	if len(got) != 1 || got[0].Addr != addr {
		t.Fatalf("raceCandidates=%+v, want [%s] (half-open fallback)", got, addr)
	}
	// 配额已被竞速消费：再查为 open（probeUsed，等待下次结果触发新窗口）。
	if s := breakerState(addr); s != "open" {
		t.Fatalf("after race fallback breaker=%s, want open (probe consumed)", s)
	}
}

// 候选不足 → 放行 1 个链路类坏池过期节点兜底；账号类永不进竞速兜底。
func TestRaceCandidatesBadPoolFallback(t *testing.T) {
	resetPoolPerfState()
	linkBad := mkProxy(28511)
	acctBad := mkProxy(28512)
	other := mkProxy(28513)
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{linkBad, acctBad, other}
	socks5Mu.Unlock()
	for i := 0; i < 3; i++ {
		markSocks5Result(linkBad.Addr, http.StatusServiceUnavailable, nil)
		markSocks5Result(acctBad.Addr, http.StatusTooManyRequests, nil)
	}
	// 链路类拨到期；账号类 badUntil 恒零（无法过期，永不放行）。
	socks5HealthMu.Lock()
	st := socks5Health[linkBad.Addr]
	st.badUntil = time.Now().Add(-time.Second)
	socks5Health[linkBad.Addr] = st
	socks5HealthMu.Unlock()

	// 无健康候选（其余节点无质量记录）→ 链路类坏池过期节点兜底进候选。
	got := raceCandidates(2)
	if len(got) != 1 || got[0].Addr != linkBad.Addr {
		t.Fatalf("raceCandidates=%+v, want [%s] (link bad-pool fallback)", got, linkBad.Addr)
	}
	socks5HealthMu.Lock()
	probeUsed := socks5Health[linkBad.Addr].badProbeUsed
	if !probeUsed {
		t.Fatal("bad-pool probe quota must be consumed after fallback")
	}
	socks5HealthMu.Unlock()

	// 链路类配额已消费、账号类永不放行 → 再跑无候选（nil）。
	if got := raceCandidates(2); len(got) != 0 {
		t.Fatalf("raceCandidates=%+v, want nil (no probe left, account never released)", got)
	}
}

// 候选充足 → 半开放行不参与候选，且不被竞速偷走探针（单发路径恢复不受影响，不回归）。
func TestRaceCandidatesHalfOpenNotSelected(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28521), mkProxy(28522), mkProxy(28523)}
	socks5Mu.Unlock()
	addr := "127.0.0.1:28521"
	for i := 0; i < 3; i++ {
		applyPoolResult(addr, 503, nil)
	}
	poolBreakerMu.Lock()
	poolBreakers[addr].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"a": {28521, 90, "healthy"},
		"b": {28522, 90, "healthy"},
		"c": {28523, 90, "healthy"},
	})

	got := raceCandidates(2)
	if len(got) != 2 {
		t.Fatalf("raceCandidates=%+v, want 2 healthy candidates", got)
	}
	for _, p := range got {
		if p.Addr == addr {
			t.Fatalf("half-open node must not be selected while candidates sufficient: %+v", got)
		}
	}
	// 探针未被消费：breakerState 仍是 halfopen（单发路径可继续使用）。
	if s := breakerState(addr); s != "halfopen" {
		t.Fatalf("breaker=%s, want halfopen (probe must not be stolen)", s)
	}
}

// 候选不足 n（非零）→ 补 1 个恢复探针到候选集。
func TestRaceCandidatesProbeFillsShortfall(t *testing.T) {
	resetPoolPerfState()
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{mkProxy(28531), mkProxy(28532), mkProxy(28533)}
	socks5Mu.Unlock()
	half := "127.0.0.1:28531"
	for i := 0; i < 3; i++ {
		applyPoolResult(half, 503, nil)
	}
	poolBreakerMu.Lock()
	poolBreakers[half].openUntil = time.Now().Add(-time.Second)
	poolBreakerMu.Unlock()
	// 只有 28532 有质量记录：候选 1 < n=3 → 补 1 个半开探针 → 共 2。
	poolQualityPath = writePoolQualityFile(t, map[string]struct {
		Port  uint16
		Score int
		Level string
	}{
		"b": {28532, 90, "healthy"},
	})

	got := raceCandidates(3)
	if len(got) != 2 {
		t.Fatalf("raceCandidates=%+v, want [28532 + half-open probe]", got)
	}
	hasHalf := false
	for _, p := range got {
		if p.Addr == half {
			hasHalf = true
		}
	}
	if !hasHalf {
		t.Fatalf("shortfall must be filled with half-open probe, got %+v", got)
	}
}

// G16：压力阈值倒挂钳制——Low >= High 的分段组合在 applyConfig 处被忽略
// （保持默认/上一次合法值，.Store 原子写），避免 raceCopies 判定反转。
func TestApplyConfigPressureClamp(t *testing.T) {
	resetPoolPerfState()

	// 双侧合法：0.3 < 0.9 → 都写入。
	applyConfig(AppConfig{PoolRacePressureLow: 0.3, PoolRacePressureHigh: 0.9})
	if got := poolRacePressureLow.Load().(float64); got != 0.3 {
		t.Fatalf("low=%v, want 0.3", got)
	}
	if got := poolRacePressureHigh.Load().(float64); got != 0.9 {
		t.Fatalf("high=%v, want 0.9", got)
	}

	// 双侧倒挂：Low=0.8 >= High=0.5 → 整对忽略，保持上一次合法组合。
	applyConfig(AppConfig{PoolRacePressureLow: 0.8, PoolRacePressureHigh: 0.5})
	if got := poolRacePressureLow.Load().(float64); got != 0.3 {
		t.Fatalf("inverted low=%v, want 0.3（保持）", got)
	}
	if got := poolRacePressureHigh.Load().(float64); got != 0.9 {
		t.Fatalf("inverted high=%v, want 0.9（保持）", got)
	}

	// 单侧与另一侧（当前值）倒挂 → 忽略该侧；合法单侧 → 写入。
	resetPoolPerfState()
	applyConfig(AppConfig{PoolRacePressureHigh: 0.4}) // 当前 Low 默认 0.5，0.4 < 0.5 倒挂
	if got := poolRacePressureHigh.Load().(float64); got != 1.0 {
		t.Fatalf("high-alone-inverted=%v, want 1.0（默认保持）", got)
	}
	applyConfig(AppConfig{PoolRacePressureLow: 0.2}) // 0.2 < 1.0 合法
	if got := poolRacePressureLow.Load().(float64); got != 0.2 {
		t.Fatalf("low-alone=%v, want 0.2", got)
	}
	applyConfig(AppConfig{PoolRacePressureLow: 2.0}) // 2.0 >= 1.0 倒挂
	if got := poolRacePressureLow.Load().(float64); got != 0.2 {
		t.Fatalf("low-alone-inverted=%v, want 0.2（保持）", got)
	}
}
