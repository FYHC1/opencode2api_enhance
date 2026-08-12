// 实例池链路级主动探活 + 滑动窗口质量评分（性能模式 P1）。
//
// 与 health.go 的纯 TCP 巡检互补：TCP 只判实例进程/端口存活，测不出链路抖动
// （用户场景端口恒通、巡检恒健康）；这里经实例 sing-box SOCKS 出口发真实
// HTTP 探测（GET /v1/models），度量整条链路（本地 sing-box → 远端厂商 API）
// 的延迟与成败，以滑动窗口样本计算每实例质量分（0~100）与等级。
package manager

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	qualityHealthy  = "healthy"
	qualityDegraded = "degraded"
	qualityFlaky    = "flaky"
	qualityDown     = "down"

	// defaultProbeTarget 默认探活目标：opencode 厂商模型列表端点
	// （与 vendors/opencode 的 zenModelsURL 一致；配置 base_url 可覆盖）。
	defaultProbeTarget = "https://opencode.ai/zen/v1/models"

	// poolProbeWorkers 探活并发上限（避免探测风暴）。
	poolProbeWorkers = 4

	// 配置默认值（config.go 中未显式设置时生效）。
	defaultProbeIntervalSec = 45
	defaultProbeTimeoutSec  = 3
	defaultQualityWindowMin = 10
)

// ProbeSample 单次链路探测样本（滑动窗口的基本单元）。
type ProbeSample struct {
	OK        bool  `json:"ok"`         // 探测成功（链路通）
	LatencyMS int64 `json:"latency_ms"` // 探测耗时（毫秒）
	TS        int64 `json:"ts"`         // 探测时刻（Unix 秒）
}

// QualityRecord 单实例质量状态（持久化于 runtime/pool_quality.json）。
type QualityRecord struct {
	Name                string        `json:"name"`
	Port                uint16        `json:"port"`
	SingboxPort         uint16        `json:"singbox_port"`
	Score               int           `json:"score"`                // 0~100
	Level               string        `json:"level"`                // healthy / degraded / flaky / down
	SuccessRate         float64       `json:"success_rate"`         // 窗口内成功率 0~1
	AvgLatencyMS        int64         `json:"avg_latency_ms"`       // 窗口内平均延迟
	ConsecutiveFailures int           `json:"consecutive_failures"` // 从最新样本回溯的连续失败数
	LastProbeTS         int64         `json:"last_probe_ts"`
	Samples             []ProbeSample `json:"samples"` // 滑动窗口内样本
}

// PoolQualitySummary 质量汇总视图（管理 API / 后续 UI 展示）。
type PoolQualitySummary struct {
	Total      int             `json:"total"`
	Probed     int             `json:"probed"`
	Healthy    int             `json:"healthy"`
	Degraded   int             `json:"degraded"`
	Flaky      int             `json:"flaky"`
	Down       int             `json:"down"`
	LastScanTS int64           `json:"last_scan_ts"`
	Records    []QualityRecord `json:"records"`
}

// ---- 配置生效值 ----

// poolProbeInterval 探活间隔生效值（秒）。
func poolProbeInterval(cfg Config) int {
	if cfg.PoolProbeIntervalSec > 0 {
		return cfg.PoolProbeIntervalSec
	}
	return defaultProbeIntervalSec
}

// poolProbeTimeout 单次探测超时生效值。
func poolProbeTimeout(cfg Config) time.Duration {
	sec := cfg.PoolProbeTimeoutSec
	if sec <= 0 {
		sec = defaultProbeTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// poolQualityWindowSec 质量滑动窗口生效值（秒）。
func poolQualityWindowSec(cfg Config) int64 {
	min := cfg.PoolQualityWindowMin
	if min <= 0 {
		min = defaultQualityWindowMin
	}
	return int64(min) * 60
}

// poolProbeEnabled 探活开关生效值（未显式设置默认开启）。
func poolProbeEnabled(cfg Config) bool {
	if cfg.PoolProbeEnabled != nil {
		return *cfg.PoolProbeEnabled
	}
	return true
}

// probeTargetURL 探测目标：优先配置 base_url（补 /v1/models），否则默认厂商端点。
func probeTargetURL(cfg Config) string {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return defaultProbeTarget
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

// ---- 评分模型 ----

// computeQuality 由窗口内样本计算质量分与等级（纯函数，便于单测）。
// 窗口外的样本滑出不计分；空窗口（新实例）乐观计 100/healthy，不误伤新节点。
func computeQuality(rec *QualityRecord, samples []ProbeSample, now, windowSec int64) {
	cutoff := now - windowSec
	var win []ProbeSample
	for _, s := range samples {
		if s.TS >= cutoff {
			win = append(win, s)
		}
	}
	rec.Samples = win
	if len(win) == 0 {
		rec.Score = 100
		rec.Level = qualityHealthy
		rec.SuccessRate = 0
		rec.AvgLatencyMS = 0
		rec.ConsecutiveFailures = 0
		rec.LastProbeTS = 0
		return
	}

	var ok int
	var latSum int64
	lastTS := win[0].TS
	for i := range win {
		if win[i].OK {
			ok++
		}
		latSum += win[i].LatencyMS
		if win[i].TS > lastTS {
			lastTS = win[i].TS
		}
	}
	// 从最新样本回溯的连续失败数。
	cf := 0
	for i := len(win) - 1; i >= 0 && !win[i].OK; i-- {
		cf++
	}
	rate := float64(ok) / float64(len(win))
	avg := latSum / int64(len(win))

	// 基础分 = 成功率；延迟分档惩罚；连续失败逐次减 15。
	score := rate * 100
	switch {
	case avg > 8000:
		score *= 0.3
	case avg > 3000:
		score *= 0.5
	case avg > 1000:
		score *= 0.8
	}
	score -= float64(cf) * 15
	if score < 0 {
		score = 0
	}

	rec.Score = int(math.Round(score))
	rec.SuccessRate = rate
	rec.AvgLatencyMS = avg
	rec.ConsecutiveFailures = cf
	rec.LastProbeTS = lastTS

	switch {
	case cf >= 3 || (rate == 0 && len(win) >= 2):
		rec.Level = qualityDown
	case cf == 2 || rec.Score < 50:
		rec.Level = qualityFlaky
	case rec.Score < 90 || rate < 0.9:
		rec.Level = qualityDegraded
	default:
		rec.Level = qualityHealthy
	}
}

func summarizeQuality(recs []QualityRecord, now int64) PoolQualitySummary {
	summary := PoolQualitySummary{LastScanTS: now, Records: []QualityRecord{}}
	for _, rec := range recs {
		summary.Records = append(summary.Records, rec)
		summary.Total++
		if len(rec.Samples) > 0 {
			summary.Probed++
		}
		switch rec.Level {
		case qualityHealthy:
			summary.Healthy++
		case qualityDegraded:
			summary.Degraded++
		case qualityFlaky:
			summary.Flaky++
		case qualityDown:
			summary.Down++
		}
	}
	return summary
}

// ---- 持久化（runtime/pool_quality.json，损坏容错） ----

func (m *Manager) poolQualityFilePath() string {
	return filepath.Join(m.paths.RuntimeDir, "pool_quality.json")
}

func (m *Manager) loadPoolQuality() []QualityRecord {
	data, err := os.ReadFile(m.poolQualityFilePath())
	if err != nil {
		return nil
	}
	var recs []QualityRecord
	if json.Unmarshal(data, &recs) != nil {
		return nil
	}
	return recs
}

func (m *Manager) savePoolQuality(recs []QualityRecord) {
	_ = os.MkdirAll(m.paths.RuntimeDir, 0o755)
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.poolQualityFilePath(), data, 0o644)
}

// ---- 探活调度 ----

// RunPoolQualityOnce 单轮链路探活：并发探测全部 Running 实例并经 sing-box
// SOCKS 出口发真实 HTTP 请求，更新质量记录并持久化。
func (m *Manager) RunPoolQualityOnce(runner Runner) PoolQualitySummary {
	if runner == nil {
		runner = m.Run()
	}
	cfg := m.loadConfig()
	timeout := poolProbeTimeout(cfg)
	windowSec := poolQualityWindowSec(cfg)
	target := probeTargetURL(cfg)
	now := time.Now().Unix()

	saved := m.loadPoolQuality()
	byName := make(map[string]*QualityRecord, len(saved))
	for i := range saved {
		rec := saved[i]
		byName[rec.Name] = &rec
	}

	var running []Instance
	for _, inst := range m.ListInstances() {
		if inst.Status.State == "Running" {
			running = append(running, inst)
		}
	}

	// 并发探测（poolProbeWorkers 上限），单实例失败不阻塞整轮。
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, poolProbeWorkers)
	for i := range running {
		inst := running[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			sample := probeInstanceOnce(inst, target, timeout)
			mu.Lock()
			rec := byName[inst.Name]
			if rec == nil {
				rec = &QualityRecord{Name: inst.Name}
			}
			rec.Port, rec.SingboxPort = inst.Port, inst.SingboxPort
			rec.Samples = append(rec.Samples, sample)
			byName[inst.Name] = rec
			mu.Unlock()
		}()
	}
	wg.Wait()

	out := make([]QualityRecord, 0, len(running))
	for _, inst := range running {
		rec := byName[inst.Name]
		if rec == nil {
			rec = &QualityRecord{Name: inst.Name, Port: inst.Port, SingboxPort: inst.SingboxPort}
		}
		computeQuality(rec, rec.Samples, now, windowSec)
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	summary := summarizeQuality(out, now)
	m.savePoolQuality(out)
	return summary
}

// StartPoolQualityLoop 后台链路探活循环：与 StartHealthLoop 并行。
// 按 pool_probe_interval_sec 间隔运行；未启用或间隔 <=0 时睡 30s 重读配置。
func (m *Manager) StartPoolQualityLoop() {
	go func() {
		for {
			cfg := m.loadConfig()
			if !poolProbeEnabled(cfg) || poolProbeInterval(cfg) <= 0 {
				time.Sleep(30 * time.Second)
				continue
			}
			started := time.Now()
			m.RunPoolQualityOnce(m.Run())
			elapsed := time.Since(started)
			wait := time.Duration(poolProbeInterval(cfg))*time.Second - elapsed
			if wait < time.Second {
				wait = time.Second
			}
			time.Sleep(wait)
		}
	}()
}

// ---- 管理 API ----

// PoolQualityHandler GET 返回最近一轮质量汇总（未跑过返回空视图）。
func (m *Manager) PoolQualityHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, m.poolQualityView())
	}
}

// poolQualityView 由持久化记录构建汇总视图（仅保留当前 Running 实例）。
func (m *Manager) poolQualityView() PoolQualitySummary {
	runningNames := map[string]bool{}
	for _, inst := range m.ListInstances() {
		if inst.Status.State == "Running" {
			runningNames[inst.Name] = true
		}
	}
	var recs []QualityRecord
	for _, rec := range m.loadPoolQuality() {
		if runningNames[rec.Name] {
			recs = append(recs, rec)
		}
	}
	now := time.Now().Unix()
	summary := summarizeQuality(recs, now)
	if summary.LastScanTS == 0 {
		summary.LastScanTS = now
	}
	return summary
}

// PoolProbeTriggerHandler POST 手动触发一轮链路探活。
func (m *Manager) PoolProbeTriggerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, m.RunPoolQualityOnce(m.Run()))
	}
}
