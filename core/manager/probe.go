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
// （便携测试/多开隔离用），否则默认 19000（正式版语义，与 Rust 一致）。
func probeAPIPort() uint16 {
	if s := os.Getenv("OPCODE2API_PROBE_API_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	return 19000
}

// probeSocksPort 探针 SOCKS 端口：优先环境变量 OPCODE2API_PROBE_SOCKS_PORT，否则 29000。
func probeSocksPort() uint16 {
	if s := os.Getenv("OPCODE2API_PROBE_SOCKS_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
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
}

// ScanOptions 扫描参数。
type ScanOptions struct {
	Nodes       []string // 空 = 全部
	APIPort     uint16   // 0 = 默认 19000
	SocksPort   uint16   // 0 = 默认 29000
	TimeoutSec  int      // 单节点预算；<3 按 3
	Concurrency int      // 默认 8，上限 8
}

// ScanController 控制一次扫描的状态机。
type ScanController struct {
	m      *Manager
	runner Runner

	mu       sync.Mutex
	progress ScanProgress
}

// NewScanController 构造扫描控制器。
func NewScanController(m *Manager, runner Runner) *ScanController {
	if runner == nil {
		runner = &realRunner{}
	}
	return &ScanController{m: m, runner: runner, progress: ScanProgress{Status: ScanIdle}}
}

// Snapshot 返回当前进度快照。
func (c *ScanController) Snapshot() ScanProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.progress
	out.Results = append([]ProbeResult{}, c.progress.Results...)
	return out
}

// RequestStop 请求停止（Running → Stopping）。
func (c *ScanController) RequestStop() ScanProgress {
	c.mu.Lock()
	if c.progress.Status == ScanRunning {
		c.progress.Status = ScanStopping
	}
	c.mu.Unlock()
	return c.Snapshot()
}

// Start 启动扫描（已运行则报错）。
func (c *ScanController) Start(opts ScanOptions) (ScanProgress, error) {
	if opts.APIPort == 0 {
		opts.APIPort = probeAPIPort()
	}
	if opts.SocksPort == 0 {
		opts.SocksPort = probeSocksPort()
	}
	// 单节点超时预算：未设置默认 25s（对齐 Rust scan_start unwrap_or(25)）；下限 3s 防非法值。
	if opts.TimeoutSec == 0 {
		opts.TimeoutSec = 25
	} else if opts.TimeoutSec < 3 {
		opts.TimeoutSec = 3
	}
	if opts.Concurrency <= 0 || opts.Concurrency > 8 {
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
