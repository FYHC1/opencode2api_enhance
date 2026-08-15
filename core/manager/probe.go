// 节点扫描探针（Rust probe.rs 移植）。分三文件：
// probe.go（类型+控制器 API）、probe_run.go（并发执行）、probe_node.go（单节点测试）。
package manager

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// ScanStatus 扫描状态。
type ScanStatus string

const (
	ScanIdle     ScanStatus = "idle"
	ScanRunning  ScanStatus = "running"
	ScanStopping ScanStatus = "stopping"
	ScanDone     ScanStatus = "done"
	ScanError    ScanStatus = "error"
)

// probeAPIPort 探针 API 端口：优先环境变量 OPCODE2API_PROBE_API_PORT
// （便携测试/多开隔离用），其次 config.probe_api_port，否则默认 19000（正式版语义）。
func (m *Manager) probeAPIPort() uint16 {
	if s := os.Getenv("OPCODE2API_PROBE_API_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	if m != nil {
		if p := m.loadConfig().ProbeAPIPort; p > 0 {
			return p
		}
	}
	return 19000
}

// probeSocksPort 探针 SOCKS 端口：优先环境变量 OPCODE2API_PROBE_SOCKS_PORT，
// 其次 config.probe_socks_port，否则默认 29000。
func (m *Manager) probeSocksPort() uint16 {
	if s := os.Getenv("OPCODE2API_PROBE_SOCKS_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	if m != nil {
		if p := m.loadConfig().ProbeSocksPort; p > 0 {
			return p
		}
	}
	return 29000
}

// ProbeResult 单节点探测结果（前端契约）。
type ProbeResult struct {
	Node       string `json:"node"`
	NodeType   string `json:"node_type"`
	Server     string `json:"server"`
	Port       uint16 `json:"port"`
	OK         bool   `json:"ok"`
	Category   string `json:"category"`
	StatusCode int    `json:"status_code,omitempty"`
	ModelCount *int   `json:"model_count,omitempty"`
	Message    string `json:"message,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
}

// ScanProgress 扫描进度（前端契约）。
type ScanProgress struct {
	Status      ScanStatus    `json:"status"`
	Total       int           `json:"total"`
	Current     int           `json:"current"`
	CurrentNode string        `json:"current_node,omitempty"`
	Results     []ProbeResult `json:"results"`
	Error       string        `json:"error,omitempty"`
	APIPort     uint16        `json:"api_port"`
	SocksPort   uint16        `json:"socks_port"`
	StartedMS   int64         `json:"started_ms,omitempty"`
	FinishedMS  int64         `json:"finished_ms,omitempty"`
	Concurrency int           `json:"concurrency"`
	// Stopping 期间统计（V2 stop-scan 悬浮窗进度）：停止时活跃探针数 / 已中断探针对数。
	StoppingCount int `json:"stopping_count,omitempty"`
	StoppedCount  int `json:"stopped_count,omitempty"`
}

// ScanOptions 扫描参数。
type ScanOptions struct {
	Nodes       []string // 空 = 全部
	APIPort     uint16   // 0 = 默认 19000
	SocksPort   uint16   // 0 = 默认 29000
	TimeoutSec  int      // 单节点预算；<3 按 3
	Concurrency int      // 默认 8，上限 8
}

// probeProcs 一个 worker 当前正在探测的探针进程对（V1 停止中断用）。
type probeProcs struct {
	sbPID int
	ocPID int
}

// ScanController 控制一次扫描的状态机。
type ScanController struct {
	m      *Manager
	runner Runner

	mu       sync.Mutex
	progress ScanProgress

	// activeProbes 活跃探针登记：worker 索引 → 正在探测的进程对。
	// 停止扫描时按 stop_scan_concurrency 并发上限 kill，使 probeNode 快速失败。
	activeMu     sync.Mutex
	activeProbes map[int]probeProcs
}

// NewScanController 构造扫描控制器。
func NewScanController(m *Manager, runner Runner) *ScanController {
	if runner == nil {
		runner = &realRunner{}
	}
	return &ScanController{
		m:            m,
		runner:       runner,
		progress:     ScanProgress{Status: ScanIdle},
		activeProbes: map[int]probeProcs{},
	}
}

// Snapshot 返回当前进度快照。
func (c *ScanController) Snapshot() ScanProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.progress
	out.Results = append([]ProbeResult{}, c.progress.Results...)
	return out
}

// registerProbe 登记 worker 正在探测的进程对（两个探针 spawn 成功后调用）。
func (c *ScanController) registerProbe(worker int, sbPID, ocPID int) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	c.activeProbes[worker] = probeProcs{sbPID: sbPID, ocPID: ocPID}
}

// unregisterProbe 注销 worker 的探测登记（probeNode 任何返回路径的 defer，防残留）。
func (c *ScanController) unregisterProbe(worker int) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	delete(c.activeProbes, worker)
}

// RequestStop 请求停止（Running → Stopping）：置标志后并发中断正在运行的探针进程。
func (c *ScanController) RequestStop() ScanProgress {
	c.mu.Lock()
	if c.progress.Status == ScanRunning {
		c.progress.Status = ScanStopping
	}
	c.mu.Unlock()
	c.interruptProbes()
	return c.Snapshot()
}

// interruptProbes 中断活跃探针：按 stop_scan_concurrency 并发上限 kill 正在探测的进程对
// （同时最多 kill 上限对，防一次性斩断全部 worker 的资源尖峰）；完成后清空登记。
// 探针进程被杀 → waitForPort 中止 / freeCompletion 连接拒绝 → probeNode 快速失败返回，
// 其 defer 再 kill 一次（幂等）并注销登记（此时登记已被清空，注销为空操作）。
// 返回 (中断前活跃对数, 已中断对数)。
func (c *ScanController) interruptProbes() (int, int) {
	cfg := Config{}
	if c.m != nil {
		cfg = c.m.loadConfig()
	}
	limit := stopScanConcurrencyOf(cfg)
	if limit < 1 {
		limit = 1
	}
	c.activeMu.Lock()
	pairs := make([]probeProcs, 0, len(c.activeProbes))
	for _, p := range c.activeProbes {
		pairs = append(pairs, p)
	}
	c.activeMu.Unlock()

	// 带缓冲 channel semaphore 限流：每对探针进程的 kill 占一个并发位。
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, p := range pairs {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_ = c.runner.Kill(p.sbPID)
			_ = c.runner.Kill(p.ocPID)
		}()
	}
	wg.Wait()

	// 清空登记（stop 后 probeNode 不再新登记：worker 循环每轮先查 isStopping）。
	c.activeMu.Lock()
	c.activeProbes = map[int]probeProcs{}
	c.activeMu.Unlock()

	c.mu.Lock()
	c.progress.StoppingCount = len(pairs)
	c.progress.StoppedCount = len(pairs)
	c.mu.Unlock()
	return len(pairs), len(pairs)
}

// Start 启动扫描（已运行则报错）。
func (c *ScanController) Start(opts ScanOptions) (ScanProgress, error) {
	if opts.APIPort == 0 {
		opts.APIPort = c.m.probeAPIPort()
	}
	if opts.SocksPort == 0 {
		opts.SocksPort = c.m.probeSocksPort()
	}
	// 单节点超时预算：未设置默认 25s（对齐 Rust scan_start unwrap_or(25)）；下限 3s 防非法值。
	if opts.TimeoutSec == 0 {
		opts.TimeoutSec = 25
	} else if opts.TimeoutSec < 3 {
		opts.TimeoutSec = 3
	}
	// 扫描并发：未指定用配置（默认 8），上限 8 防探测风暴（D3）。
	if opts.Concurrency <= 0 {
		opts.Concurrency = scanConcurrencyOf(c.m.loadConfig())
	}
	if opts.Concurrency > 8 {
		opts.Concurrency = 8
	}
	c.mu.Lock()
	if c.progress.Status == ScanRunning {
		c.mu.Unlock()
		return c.Snapshot(), fmt.Errorf("节点扫描已在进行中，请等待结束或先停止")
	}
	c.progress = ScanProgress{
		Status:      ScanRunning,
		APIPort:     opts.APIPort,
		SocksPort:   opts.SocksPort,
		StartedMS:   time.Now().UnixMilli(),
		Concurrency: opts.Concurrency,
	}
	c.mu.Unlock()

	filter := map[string]bool{}
	for _, n := range opts.Nodes {
		filter[n] = true
	}
	var targets []ClashNode
	for _, n := range c.m.ListNodesWithGroup() {
		if len(filter) > 0 && !filter[n.Name] {
			continue
		}
		if n.Password == "" && n.UUID == "" {
			continue
		}
		targets = append(targets, n)
	}
	// 对齐 Rust：筛选后无可用节点（全部无凭据）→ 明确报错而非静默空扫
	if len(targets) == 0 {
		c.mu.Lock()
		c.progress.Status = ScanIdle
		c.mu.Unlock()
		return c.Snapshot(), fmt.Errorf("没有可扫描的节点（需含 password/uuid 等凭据）")
	}
	go c.run(opts, targets)
	return c.Snapshot(), nil
}
