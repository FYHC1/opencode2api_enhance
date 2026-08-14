// 实例池性能模式（P2）：质量加权路由 + 熔断/半开自动恢复。
//
// 与 core/manager 的 P1 链路探活配合：网关子进程经 -pool-quality 参数拿到
// runtime/pool_quality.json（按 SingboxPort 索引的质量分/等级），路由选择时
// 消费它——healthy 优先、degraded 降权、flaky 跳过、down 剔除；请求结果
// 实时回填（熔断计数 + 反馈窗口），失败快速反映、恢复自动回归。
// pool_performance_mode 关闭时路由行为与基线一致（纯游标 + 冷却）。
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/6Kmfi6HP/opencode2api/vendors/opencode"
)

// ---- P2 全局配置（applyConfig 热更新） ----

var (
	// poolPerfMode 性能模式开关（默认开启；关闭 = 基线行为）。
	poolPerfMode = true
	// poolBreakerThreshold 连续失败熔断阈值（默认 3）。
	poolBreakerThreshold = 3
	// poolHalfOpenIntervalSec 熔断后半开放行间隔（秒，默认 60）。
	poolHalfOpenIntervalSec = 60
	// poolQualityPath 质量文件路径（-pool-quality 注入；空 = 无质量文件）。
	poolQualityPath string
	// poolRaceCopies 请求级竞速并行数上限（默认 2；1 = 退化为串行）。
	// S5 起语义为上限：实际副本由压力系数分段动态决定（见 raceCopies）。
	poolRaceCopies = 2
	// raceBudgetMS 竞速整体预算（毫秒，默认 10000；0 回退默认）。
	raceBudgetMS = 10000
	// poolRacePressureLow / poolRacePressureHigh 压力系数分段阈值（S5，默认 0.5 / 1.0）：
	// pressure < low → 全速竞速（用满上限）；low ≤ pressure < high → 温和竞速（2）；
	// pressure ≥ high → 退化单发（等效分散路由）。
	poolRacePressureLow  = 0.5
	poolRacePressureHigh = 1.0
)

// ---- 质量文件缓存（读 runtime/pool_quality.json，节流刷新） ----

type poolQualityEntry struct {
	Score int    // 0~100
	Level string // healthy / degraded / flaky / down
}

var (
	poolQualityMu     sync.RWMutex
	poolQualityCache  map[string]poolQualityEntry
	poolQualityStamp  time.Time // 上次读取时的文件 mtime
	poolQualityLoaded time.Time
)

// loadPoolQualityCache 读取质量文件；mtime 未变且 5s 内已读则跳过。
func loadPoolQualityCache() {
	if poolQualityPath == "" {
		return
	}
	info, err := os.Stat(poolQualityPath)
	if err != nil {
		return
	}
	if !info.ModTime().After(poolQualityStamp) && time.Since(poolQualityLoaded) < 5*time.Second {
		return
	}
	data, err := os.ReadFile(poolQualityPath)
	if err != nil {
		return
	}
	var recs []struct {
		SingboxPort uint16 `json:"singbox_port"`
		Score       int    `json:"score"`
		Level       string `json:"level"`
	}
	if json.Unmarshal(data, &recs) != nil {
		return
	}
	cache := make(map[string]poolQualityEntry, len(recs))
	for _, r := range recs {
		cache[fmt.Sprintf("127.0.0.1:%d", r.SingboxPort)] = poolQualityEntry{Score: r.Score, Level: r.Level}
	}
	poolQualityMu.Lock()
	poolQualityCache = cache
	poolQualityStamp = info.ModTime()
	poolQualityLoaded = time.Now()
	poolQualityMu.Unlock()
}

// poolQualityOf 查询节点质量记录（无记录 = false）。
func poolQualityOf(addr string) (poolQualityEntry, bool) {
	poolQualityMu.RLock()
	defer poolQualityMu.RUnlock()
	e, ok := poolQualityCache[addr]
	return e, ok
}

// ---- 请求结果回填（实测量通道：近 10 分钟成败反馈） ----

type poolFbSample struct {
	ok bool
	ts int64
}

var (
	poolFeedbackMu sync.Mutex
	poolFeedback   = map[string][]poolFbSample{}
)

// recordPoolFeedback 记录一次真实请求结果（成功/失败），修剪窗口外样本。
func recordPoolFeedback(addr string, ok bool) {
	if addr == "" {
		return
	}
	now := time.Now().Unix()
	cutoff := now - 600 // 10 分钟窗口（与质量窗口一致）
	poolFeedbackMu.Lock()
	defer poolFeedbackMu.Unlock()
	samples := append(poolFeedback[addr], poolFbSample{ok: ok, ts: now})
	keep := samples[:0]
	for _, s := range samples {
		if s.ts >= cutoff {
			keep = append(keep, s)
		}
	}
	poolFeedback[addr] = keep
}

// poolFeedbackScore 反馈窗口内成功率 ×100；无样本返回 -1。
func poolFeedbackScore(addr string) int {
	now := time.Now().Unix()
	cutoff := now - 600
	poolFeedbackMu.Lock()
	defer poolFeedbackMu.Unlock()
	samples := poolFeedback[addr]
	if len(samples) == 0 {
		return -1
	}
	var ok, n int
	for _, s := range samples {
		if s.ts < cutoff {
			continue
		}
		n++
		if s.ok {
			ok++
		}
	}
	if n == 0 {
		return -1
	}
	return ok * 100 / n
}

// ---- S5：每节点 in-flight 计数（least-in-flight 均衡） ----

// proxyInFlight 每节点在途请求计数：竞速候选确定时 +1（RaceStarted）、
// 竞速收尾时 -1（RaceFinished）。增/减由 vendors/opencode 的 raceDo 经
// contract.RaceTracker 回调（rootTransport 桥接）触发，对同一批候选 addrs
// 严格成对——raceDo 用 defer 保证所有返回路径（含预算到期/全败/单候选）
// 都会 -1，不会泄漏。无记录的地址按 0 处理（首访并发安全初始化）。
var (
	proxyInFlightMu sync.Mutex
	proxyInFlight   = map[string]*atomic.Int64{}
)

// proxyInflightAdd 对节点在途计数加 delta（首次访问自动创建计数）。
func proxyInflightAdd(addr string, delta int) {
	proxyInFlightMu.Lock()
	c := proxyInFlight[addr]
	if c == nil {
		c = &atomic.Int64{}
		proxyInFlight[addr] = c
	}
	proxyInFlightMu.Unlock()
	c.Add(int64(delta))
}

// proxyInflightOf 返回节点当前在途请求数（无记录 = 0）。
func proxyInflightOf(addr string) int {
	proxyInFlightMu.Lock()
	c := proxyInFlight[addr]
	proxyInFlightMu.Unlock()
	if c == nil {
		return 0
	}
	return int(c.Load())
}

// ---- S5：压力系数（活跃请求数 / 健康节点数） ----

// raceHealthyNodeCount 可竞速健康节点数（压力系数分母）：poolquality 缓存中
// known 且非 down/flaky/unknown 的节点数（与 raceCandidates 的候选口径一致）。
func raceHealthyNodeCount() int {
	loadPoolQualityCache()
	poolQualityMu.RLock()
	defer poolQualityMu.RUnlock()
	n := 0
	for _, e := range poolQualityCache {
		if e.Level == "healthy" || e.Level == "degraded" {
			n++
		}
	}
	return n
}

// racePressure 计算压力系数 = 活跃请求数 / 健康节点数。
// 活跃请求数来自 vendors/opencode 全局计数（Chat/ChatStream 入口成对增减）；
// 除数为 0（无健康节点）→ 按高压力（≥2.0）处理。
func racePressure() float64 {
	healthy := raceHealthyNodeCount()
	if healthy == 0 {
		return 2.0 // 无健康节点 → 高压力
	}
	return float64(opencode.ActiveRequests()) / float64(healthy)
}

// racePressureFn 压力系数计算函数（独立变量，测试可替换构造高压场景）。
var racePressureFn = racePressure

// ---- S5：候选排序随机源（随机扰动 + 高压力摊开） ----

var (
	raceRandMu sync.Mutex
	raceRand   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// setRaceRandSeed 固定竞速随机源（测试用；保证扰动/摊开的确定性）。
func setRaceRandSeed(seed int64) {
	raceRandMu.Lock()
	defer raceRandMu.Unlock()
	raceRand = rand.New(rand.NewSource(seed))
}

// raceScoreJitter 返回 [0,3) 随机微调：打破同 in-flight 同分平局，
// 避免多请求同时选中同一节点（扰动幅度 <3%，不颠覆质量排序）。
func raceScoreJitter() float64 {
	raceRandMu.Lock()
	defer raceRandMu.Unlock()
	return raceRand.Float64() * 3
}

// raceShuffle 纯随机打乱候选（高压力时跳过质量排序，负载均衡摊开）。
func raceShuffle(cands []raceCandidate) {
	raceRandMu.Lock()
	defer raceRandMu.Unlock()
	raceRand.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
}

// ---- 熔断状态机（open / half-open / closed） ----

type poolBreaker struct {
	failures  int
	openUntil time.Time // 熔断到期；now >= openUntil 且未消费半开放行时放行 1 个
	probeUsed bool      // 半开放行是否已消费
}

var (
	poolBreakerMu sync.Mutex
	poolBreakers  = map[string]*poolBreaker{}
)

// breakerState 返回熔断态：open（剔除）/ halfopen（本次放行 1 个探测）/ closed。
func breakerState(addr string) string {
	poolBreakerMu.Lock()
	defer poolBreakerMu.Unlock()
	b := poolBreakers[addr]
	if b == nil || b.openUntil.IsZero() {
		return "closed"
	}
	if time.Now().Before(b.openUntil) {
		return "open"
	}
	if !b.probeUsed {
		b.probeUsed = true
		return "halfopen"
	}
	return "open"
}

// applyPoolResult 请求结果回填：反馈窗口 + 熔断计数。
// 链路失败（网络错误/5xx/超时）累计连续失败，达阈值 → open；
// 成功（2xx）→ 熔断复位（closed），自动回归池子。
// 业务拒绝（401/402/429）走既有坏池/冷却机制，不触发熔断。
func applyPoolResult(addr string, status int, requestErr error) {
	if addr == "" {
		return
	}
	reqFailed := requestErr != nil || status == 0 || status >= 500 ||
		status == 401 || status == 402 || status == 429
	recordPoolFeedback(addr, !reqFailed)

	linkFailed := requestErr != nil || status == 0 || status >= 500
	poolBreakerMu.Lock()
	defer poolBreakerMu.Unlock()
	if !linkFailed {
		if b := poolBreakers[addr]; b != nil {
			b.failures = 0
			b.openUntil = time.Time{}
			b.probeUsed = false
		}
		return
	}
	b := poolBreakers[addr]
	if b == nil {
		b = &poolBreaker{}
		poolBreakers[addr] = b
	}
	b.failures++
	if b.failures >= poolBreakerThreshold {
		b.openUntil = time.Now().Add(time.Duration(poolHalfOpenIntervalSec) * time.Second)
		b.probeUsed = false
		slog.Warn("pool breaker opened", "addr", addr, "failures", b.failures)
	}
}

// ---- 质量加权路由 ----

// pickWeightedProxy 性能模式下的节点选择：
//   - 坏池/冷却中节点剔除（冷却兜底保留，全池不可用时回退）
//   - 熔断中节点剔除；到期后放行 1 个半开探测，成功即恢复
//   - 质量分：down 剔除、flaky 跳过、degraded 降权、healthy 优先；同档按分排序
//   - 请求反馈与探活分融合（7:3），失败快速反映到排序
func pickWeightedProxy(proxies []Socks5Proxy, start int) Socks5Proxy {
	// S5: 防御性退化——单出口直接返回（调用方 pickHealthyProxy 已前置，此处兜底）。
	if len(proxies) <= 1 {
		if len(proxies) == 1 {
			return proxies[0]
		}
		return Socks5Proxy{}
	}
	now := time.Now()
	loadPoolQualityCache()

	type cand struct {
		proxy Socks5Proxy
		score int
		tier  int // 2 = healthy 档，1 = degraded 档
	}
	var cands []cand
	var halfOpen Socks5Proxy
	var fallback Socks5Proxy
	var fallbackUntil time.Time

	socks5HealthMu.Lock()
	for offset := 0; offset < len(proxies); offset++ {
		proxy := proxies[(start+offset)%len(proxies)]
		state := socks5Health[proxy.Addr]
		if state.badReason != "" {
			continue // 坏池彻底剔除
		}
		if !state.until.IsZero() && now.Before(state.until) {
			if fallback.Addr == "" || state.until.Before(fallbackUntil) {
				fallback = proxy
				fallbackUntil = state.until
			}
			continue // 冷却中
		}
		switch breakerState(proxy.Addr) {
		case "open":
			continue
		case "halfopen":
			if halfOpen.Addr == "" {
				halfOpen = proxy // 放行 1 个半开探测
			}
			continue
		}

		q, known := poolQualityOf(proxy.Addr)
		score, level := 100, "healthy"
		if known {
			score, level = q.Score, q.Level
		}
		// 实测量反馈融合（无反馈样本时保持探活分）。
		if fb := poolFeedbackScore(proxy.Addr); fb >= 0 {
			if known {
				score = (score*7 + fb*3) / 10
			} else {
				score = fb
			}
		}
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		switch level {
		case "down", "flaky":
			continue
		case "degraded":
			cands = append(cands, cand{proxy: proxy, score: score, tier: 1})
		default:
			cands = append(cands, cand{proxy: proxy, score: score, tier: 2})
		}
	}
	socks5HealthMu.Unlock()

	// 半开探测优先（验证恢复）。
	if halfOpen.Addr != "" {
		slog.Info("pool half-open probe", "addr", halfOpen.Addr)
		return halfOpen
	}
	if len(cands) > 0 {
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].tier != cands[j].tier {
				return cands[i].tier > cands[j].tier
			}
			return cands[i].score > cands[j].score
		})
		chosen := cands[0]
		if chosen.proxy.Addr != proxies[start].Addr {
			slog.Info("pool switched", "from", proxies[start].Addr, "to", chosen.proxy.Addr, "score", chosen.score)
		}
		return chosen.proxy
	}
	// 全池无可用候选：回退冷却最早结束的节点（与基线兜底一致）。
	return fallback
}

// raceCandidate 竞速候选（S5 扩展：scoreJitter 打破同分平局）。
type raceCandidate struct {
	proxy      Socks5Proxy
	score      int
	tier       int
	scoreJitter float64 // [0,3) 随机扰动，排序时与 score 相加
}

// raceCandidates 请求级竞速候选：返回至多 n 个可用代理
// （跳过坏池/冷却中/熔断/down/flaky）。
// S5 候选均衡：in-flight 升序优先（least-in-flight），同 in-flight 按
// 质量分（score + [0,3) 随机扰动）降序，同分 healthy 优先于 degraded；
// 高压力（pressure ≥ 2.0）时跳过质量排序、纯随机摊开（负载均衡）。
// 无任何可用候选时回退一个"冷却最早结束"的节点；n<=1 或池空返回 nil（不竞速）。
func raceCandidates(n int) []Socks5Proxy {
	if n <= 1 || !poolPerfMode {
		return nil
	}
	now := time.Now()
	loadPoolQualityCache()

	socks5Mu.RLock()
	proxies := append([]Socks5Proxy(nil), socks5Proxies...)
	socks5Mu.RUnlock()
	// S5: 单出口退化——候选 <2 时竞速无意义（扇出 1 个等于串行），直接走默认请求。
	if len(proxies) < 2 {
		return nil
	}

	var cands []raceCandidate
	var fallback Socks5Proxy
	var fallbackUntil time.Time

	socks5HealthMu.Lock()
	for _, proxy := range proxies {
		state := socks5Health[proxy.Addr]
		if state.badReason != "" {
			continue
		}
		if breakerState(proxy.Addr) != "closed" {
			continue // 熔断中不参与竞速（半开放行语义留给单发路径）
		}
		if !state.until.IsZero() && now.Before(state.until) {
			if fallback.Addr == "" || state.until.Before(fallbackUntil) {
				fallback = proxy
				fallbackUntil = state.until
			}
			continue
		}
		q, known := poolQualityOf(proxy.Addr)
		// S1 冷启动不竞速：候选必须已有探活样本（known=true 且非 unknown），
		// 无质量记录/空窗口节点不参与竞速，避免冷启动双倍并行炸上游。
		if !known || q.Level == "unknown" {
			continue
		}
		score, level := q.Score, q.Level
		if fb := poolFeedbackScore(proxy.Addr); fb >= 0 {
			score = (score*7 + fb*3) / 10
		}
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		switch level {
		case "down", "flaky":
			continue
		case "degraded":
			cands = append(cands, raceCandidate{proxy: proxy, score: score, tier: 1, scoreJitter: raceScoreJitter()})
		default:
			cands = append(cands, raceCandidate{proxy: proxy, score: score, tier: 2, scoreJitter: raceScoreJitter()})
		}
	}
	socks5HealthMu.Unlock()

	if len(cands) == 0 {
		if fallback.Addr != "" {
			return []Socks5Proxy{fallback}
		}
		return nil
	}
	if racePressureFn() >= 2.0 {
		// 高压力：跳过质量排序，纯随机摊开（把扇出均匀撒到全部健康节点）。
		raceShuffle(cands)
	} else {
		sort.SliceStable(cands, func(i, j int) bool {
			// 1) in-flight 升序优先（治流量聚集）；
			// 2) 同 in-flight 按质量分（含随机扰动）降序；
			// 3) 同分按档位（healthy > degraded）降序。
			if fi, fj := proxyInflightOf(cands[i].proxy.Addr), proxyInflightOf(cands[j].proxy.Addr); fi != fj {
				return fi < fj
			}
			if si, sj := float64(cands[i].score)+cands[i].scoreJitter, float64(cands[j].score)+cands[j].scoreJitter; si != sj {
				return si > sj
			}
			return cands[i].tier > cands[j].tier
		})
	}
	out := make([]Socks5Proxy, 0, n)
	for i := 0; i < len(cands) && len(out) < n; i++ {
		out = append(out, cands[i].proxy)
	}
	return out
}
