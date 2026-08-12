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
	"os"
	"sort"
	"sync"
	"time"
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
	// poolRaceCopies 请求级竞速并行数（默认 2；1 = 退化为串行）。
	poolRaceCopies = 2
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

// raceCandidates 请求级竞速候选：返回质量优先的至多 n 个可用代理
// （跳过坏池/冷却中/熔断/down/flaky；healthy 优先、degraded 次之、分高在前）。
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
	if len(proxies) == 0 {
		return nil
	}

	type cand struct {
		proxy Socks5Proxy
		score int
		tier  int
	}
	var cands []cand
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
		score, level := 100, "healthy"
		if known {
			score, level = q.Score, q.Level
		}
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

	if len(cands) == 0 {
		if fallback.Addr != "" {
			return []Socks5Proxy{fallback}
		}
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].tier != cands[j].tier {
			return cands[i].tier > cands[j].tier
		}
		return cands[i].score > cands[j].score
	})
	out := make([]Socks5Proxy, 0, n)
	for i := 0; i < len(cands) && len(out) < n; i++ {
		out = append(out, cands[i].proxy)
	}
	return out
}
