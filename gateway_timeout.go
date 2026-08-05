package main

import (
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"time"
)

// ======================== 流内超时配置（区间随机） ========================
// 从 model-gateway 分析结论：SSE 流一旦建立，现有代码无 TTFT/静默超时，
// 上游挂起时 reader.ReadString 无限阻塞。本模块为每个请求在 [min,max]
// 区间内随机取一个超时值，避免固定超时被上游识别为定时扫描/竞速探测，
// 同时 min 下限保护防止过密重试。

// 区间默认值（生产）
const (
	DefaultTTFTMin    = 15 * time.Second
	DefaultTTFTMax    = 25 * time.Second
	DefaultSilenceMin = 30 * time.Second
	DefaultSilenceMax = 60 * time.Second
	DefaultProbeMin   = 2
	DefaultProbeMax   = 3
	// DefaultCallLogMax 日志保留上限（条），前端设置页可改
	DefaultCallLogMax = 5000
)

type TimeoutConfig struct {
	TTFTRange    [2]time.Duration
	SilenceRange [2]time.Duration
	ProbeRange   [2]int
}

func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		TTFTRange:    [2]time.Duration{DefaultTTFTMin, DefaultTTFTMax},
		SilenceRange: [2]time.Duration{DefaultSilenceMin, DefaultSilenceMax},
		ProbeRange:   [2]int{DefaultProbeMin, DefaultProbeMax},
	}
}

// randDuration 返回 [min,max] 均匀随机值（含端点）
func randDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int64N(int64(max-min)+1))
}

func (c TimeoutConfig) RandomTTFT() time.Duration {
	return randDuration(c.TTFTRange[0], c.TTFTRange[1])
}

func (c TimeoutConfig) RandomSilence() time.Duration {
	return randDuration(c.SilenceRange[0], c.SilenceRange[1])
}

func (c TimeoutConfig) RandomProbeN() int {
	if c.ProbeRange[1] <= c.ProbeRange[0] {
		return c.ProbeRange[0]
	}
	return c.ProbeRange[0] + rand.IntN(c.ProbeRange[1]-c.ProbeRange[0]+1)
}

// ======================== 全流程调用日志 ========================
// 记录每个请求的完整决策链：接口/模型/节点/路由模式/连接结果/超时/切换/结果。
// 前端日志页按「成功一行简短、异常整块详细」渲染，每条以【成功】/【失败】开头。

type CallEvent struct {
	Type   string    `json:"type"`
	Node   string    `json:"node,omitempty"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

type CallRecord struct {
	ReqID         string      `json:"req_id"`
	TS            string      `json:"ts"`
	Path          string      `json:"path"`
	Model         string      `json:"model"`
	Stream        bool        `json:"stream"`
	RouteMode     string      `json:"route_mode"`
	Nodes         []string    `json:"nodes"`
	Events        []CallEvent `json:"events"`
	Status        string      `json:"status"`
	PromptTok     int64       `json:"prompt_tokens"`
	CompletionTok int64       `json:"completion_tokens"`
	DurationMS    int64       `json:"duration_ms"`
	ErrMsg        string      `json:"err_msg,omitempty"`
}

func CallStatusText(rec CallRecord) string {
	if rec.Status == "ok" {
		return "【成功】"
	}
	return "【失败】"
}

type EventLog struct {
	mu         sync.Mutex
	maxRecords int
	records    []CallRecord
	path       string // 非空时同步落盘 JSONL
}

func NewEventLog(maxRecords int) *EventLog {
	return &EventLog{maxRecords: maxRecords}
}

// SetPath 启用 JSONL 落盘（路径的父目录需存在）
func (l *EventLog) SetPath(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.path = path
}

func (l *EventLog) MaxRecords() int { return l.maxRecords }

func (l *EventLog) Append(rec CallRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
	if len(l.records) > l.maxRecords {
		l.records = l.records[len(l.records)-l.maxRecords:]
	}
	if l.path != "" {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) ReadAll() []CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]CallRecord, len(l.records))
	copy(out, l.records)
	return out
}

// LoadCallLogFromFile 从 JSONL 恢复（重启后读取历史）
func LoadCallLogFromFile(path string) (*EventLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEventLog(DefaultCallLogMax), nil
		}
		return nil, err
	}
	l := NewEventLog(DefaultCallLogMax)
	for _, line := range splitJSONLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec CallRecord
		if json.Unmarshal(line, &rec) == nil {
			l.records = append(l.records, rec)
		}
	}
	if len(l.records) > l.maxRecords {
		l.records = l.records[len(l.records)-l.maxRecords:]
	}
	return l, nil
}

func splitJSONLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}

// ======================== 全局状态 ========================

var (
	timeoutCfg     = DefaultTimeoutConfig()
	callLog        = NewEventLog(DefaultCallLogMax)
	callLogPath    = "call_log.jsonl"
	callLogMu      sync.RWMutex
	callLogEnabled = true // 仅网关/代理池模式启用（避免直连实例产生无人读取的日志）
)

// initCallLog 在进程启动时加载历史并启用落盘
func initCallLog() {
	loaded, err := LoadCallLogFromFile(callLogPath)
	if err == nil {
		callLog = loaded
	}
	callLog.SetPath(callLogPath)
}

// recordCall 便捷包装：追加一条调用日志（失败不阻塞请求）
func recordCall(rec CallRecord) {
	if !callLogEnabled {
		return
	}
	if err := callLog.Append(rec); err != nil {
		slog.Error("call log append failed", "error", err)
	}
}

// setTimeoutConfigFromApp 从 AppConfig 读取区间配置并应用（热加载）
func setTimeoutConfigFromApp(cfg AppConfig) {
	if cfg.TTFTMinMS > 0 && cfg.TTFTMaxMS >= cfg.TTFTMinMS {
		timeoutCfg.TTFTRange = [2]time.Duration{
			time.Duration(cfg.TTFTMinMS) * time.Millisecond,
			time.Duration(cfg.TTFTMaxMS) * time.Millisecond,
		}
	}
	if cfg.SilenceMinMS > 0 && cfg.SilenceMaxMS >= cfg.SilenceMinMS {
		timeoutCfg.SilenceRange = [2]time.Duration{
			time.Duration(cfg.SilenceMinMS) * time.Millisecond,
			time.Duration(cfg.SilenceMaxMS) * time.Millisecond,
		}
	}
	if cfg.ProbeMin > 0 && cfg.ProbeMax >= cfg.ProbeMin {
		timeoutCfg.ProbeRange = [2]int{cfg.ProbeMin, cfg.ProbeMax}
	}
	if cfg.CallLogMax > 0 {
		callLogMu.Lock()
		callLog = NewEventLog(cfg.CallLogMax)
		callLog.SetPath(callLogPath)
		callLogMu.Unlock()
	}
}