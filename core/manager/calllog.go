// 调用日志读取（Rust call_log.rs 移植）：解析 runtime/_unified-gateway/call_log.jsonl，
// 保持 Go 网关写盘的 snake_case 字段名。
package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CallLogEvent 单条事件。
type CallLogEvent struct {
	Type   string `json:"type"`
	Node   string `json:"node,omitempty"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at,omitempty"`
}

// CallLogRecord 单条调用记录（字段与 main 包 CallRecord 一致）。
type CallLogRecord struct {
	ReqID            string         `json:"req_id"`
	TS               string         `json:"ts"`
	Path             string         `json:"path,omitempty"`
	Model            string         `json:"model,omitempty"`
	Stream           bool           `json:"stream,omitempty"`
	RouteMode        string         `json:"route_mode,omitempty"`
	Nodes            []string       `json:"nodes,omitempty"`
	Events           []CallLogEvent `json:"events,omitempty"`
	Status           string         `json:"status,omitempty"`
	PromptTokens     int64          `json:"prompt_tokens,omitempty"`
	CompletionTokens int64          `json:"completion_tokens,omitempty"`
	DurationMS       int64          `json:"duration_ms,omitempty"`
	ErrMsg           string         `json:"err_msg,omitempty"`
}

// StatusText 状态前缀（前端着色用）。
func (r CallLogRecord) StatusText() string {
	if r.Status == "ok" {
		return "【成功】"
	}
	return "【失败】"
}

// HasIssue 是否有切换/异常事件（前端"只看失败"过滤）。
func (r CallLogRecord) HasIssue() bool {
	if r.Status != "ok" {
		return true
	}
	for _, e := range r.Events {
		switch e.Type {
		case "switch", "ttft_timeout", "silence_timeout", "stream_interrupt",
			"stream_error", "connect_error", "upstream_error", "all_failed":
			return true
		}
	}
	return false
}

// CallLogPath 返回统一网关日志路径。
func (m *Manager) CallLogPath() string {
	return filepath.Join(m.paths.RuntimeDir, "_unified-gateway", "call_log.jsonl")
}

// ReadCallLog 读取最新 max 条（按文件顺序，最后为最新）；缺失/损坏 → 空。
func (m *Manager) ReadCallLog(max int) []CallLogRecord {
	if max <= 0 {
		max = 1
	}
	if max > 50000 {
		max = 50000
	}
	data, err := os.ReadFile(m.CallLogPath())
	if err != nil {
		return []CallLogRecord{}
	}
	records := make([]CallLogRecord, 0, 64)
	for _, line := range splitCallLog(data, '\n') {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec CallLogRecord
		if json.Unmarshal(line, &rec) == nil {
			if len(records) >= max {
				records = records[1:]
			}
			records = append(records, rec)
		}
	}
	return records
}

// ClearCallLog 清空统一网关调用日志（删除文件；Go 网关 Append 只追加，内存环形缓冲不回写）。
func (m *Manager) ClearCallLog() error {
	path := m.CallLogPath()
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("删除日志文件失败: %w", err)
		}
	}
	return nil
}

// splitCallLog 按字节切行。
func splitCallLog(data []byte, sep byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == sep {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
