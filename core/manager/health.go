// 后台健康巡检（Rust health.rs 移植）：
// 周期探测实例 API 端口可达性，连续失败达阈值自动重启（stop→start）。
//
// 探测方式：TCP connect（1s 超时）到实例 API 端口——实例存在 401 门禁时
// HTTP GET 需要携带密钥，TCP 连通性即可判定进程存活且端口在监听，
// 与实例启动 wait_for_port 的判据一致。
//
// 端点：POST /api/admin/health/check（手动触发一轮）、GET /api/admin/health/summary。
package manager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// HealthRecord 单实例健康记录（持久化于 runtime/health.json）。
type HealthRecord struct {
	Name                string `json:"name"`
	Healthy             bool   `json:"healthy"`
	LastCheckTS         int64  `json:"last_check_ts"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
}

// HealthSummary 巡检汇总（前端展示）。
type HealthSummary struct {
	Total      int            `json:"total"`
	Healthy    int            `json:"healthy"`
	Unhealthy  int            `json:"unhealthy"`
	Records    []HealthRecord `json:"records"`
	LastScanTS int64          `json:"last_scan_ts"`
}

// healthFilePath runtime/health.json。
func (m *Manager) healthFilePath() string {
	return filepath.Join(m.paths.RuntimeDir, "health.json")
}

// loadHealthRecords 读取健康记录（缺失/损坏返回空）。
func (m *Manager) loadHealthRecords() []HealthRecord {
	data, err := os.ReadFile(m.healthFilePath())
	if err != nil {
		return nil
	}
	var recs []HealthRecord
	if json.Unmarshal(data, &recs) != nil {
		return nil
	}
	return recs
}

// saveHealthRecords 持久化健康记录。
func (m *Manager) saveHealthRecords(recs []HealthRecord) {
	_ = os.MkdirAll(m.paths.RuntimeDir, 0o755)
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.healthFilePath(), data, 0o644)
}

// probePort 探测实例 API 端口是否可连接（TCP connect，timeout 内成功即健康）。
func probePort(port uint16, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// RunHealthCheckOnce 单轮巡检：探测 Running 实例的 API 端口，达阈值自动重启。
func (m *Manager) RunHealthCheckOnce(runner Runner) HealthSummary {
	if runner == nil {
		runner = m.Run()
	}
	byName := map[string]*HealthRecord{}
	saved := m.loadHealthRecords()
	for i := range saved {
		rec := saved[i]
		byName[rec.Name] = &rec
	}

	instances := m.ListInstances()
	now := time.Now().Unix()
	summary := HealthSummary{LastScanTS: now}

	// 仅巡检当前 Running 的实例
	var running []Instance
	for i := range instances {
		if instances[i].Status.State == "Running" {
			running = append(running, instances[i])
		}
	}

	for i := range running {
		inst := running[i]
		rec := byName[inst.Name]
		if rec == nil {
			rec = &HealthRecord{Name: inst.Name, Healthy: true, LastCheckTS: now}
			byName[inst.Name] = rec
		}
		rec.LastCheckTS = now
		if probePort(inst.Port, time.Second) {
			rec.Healthy = true
			rec.ConsecutiveFailures = 0
			rec.LastError = ""
		} else {
			rec.Healthy = false
			rec.ConsecutiveFailures++
			rec.LastError = "API 端口 127.0.0.1:" + itoa(inst.Port) + " 不可达"
		}
	}

	// 依据配置自动重启：仅对「当前 Running 且连续失败达阈值」的实例重启，
	// 避免把已停止实例（含陈旧的失败计数记录）误启。
	threshold := m.loadConfig().HealthRestartThreshold
	runningNames := map[string]bool{}
	for i := range running {
		runningNames[running[i].Name] = true
	}
	var toRestart []string
	for _, rec := range byName {
		if runningNames[rec.Name] && threshold > 0 && rec.ConsecutiveFailures >= threshold {
			toRestart = append(toRestart, rec.Name)
		}
	}
	for _, name := range toRestart {
		stopped := m.StopInstance(runner, name) == nil
		started := false
		if stopped {
			started = m.StartInstance(runner, name) == nil
		}
		if rec := byName[name]; rec != nil {
			if started {
				rec.Healthy = true
				rec.ConsecutiveFailures = 0
				rec.LastError = "已自动重启"
			} else {
				rec.Healthy = false
				rec.LastError = "自动重启失败"
			}
		}
	}

	// 只保留当前 Running 实例的记录：已停止/已删除实例的陈旧记录会污染
	// total/healthy/unhealthy 统计（例如停掉一个实例后仍显示其旧健康记录）。
	var recordsOut []HealthRecord
	for _, rec := range byName {
		if runningNames[rec.Name] {
			recordsOut = append(recordsOut, *rec)
		}
	}
	sort.Slice(recordsOut, func(i, j int) bool { return recordsOut[i].Name < recordsOut[j].Name })
	summary.Total = len(running)
	for i := range recordsOut {
		if recordsOut[i].Healthy {
			summary.Healthy++
		} else {
			summary.Unhealthy++
		}
	}
	summary.Records = recordsOut
	m.saveHealthRecords(recordsOut)
	return summary
}

// StartHealthLoop 后台巡检循环：按配置间隔（health_check_interval_sec，0 则不巡检）运行。
// 返回停止函数（G22：测试/热重建可关闭 goroutine；生产启动忽略返回值）。
func (m *Manager) StartHealthLoop() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			interval := m.loadConfig().HealthCheckIntervalSec
			if interval <= 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
				}
				continue
			}
			started := time.Now()
			m.RunHealthCheckOnce(m.Run())
			elapsed := time.Since(started)
			wait := time.Duration(interval)*time.Second - elapsed
			if wait < time.Second {
				wait = time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
	return cancel
}

// HealthCheckHandler POST 手动触发一轮巡检。
func (m *Manager) HealthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, m.RunHealthCheckOnce(m.Run()))
	}
}

// HealthSummaryHandler GET 返回最近一次巡检汇总（未跑过返回空）。
func (m *Manager) HealthSummaryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		summary := HealthSummary{Records: []HealthRecord{}}
		recs := m.loadHealthRecords()
		runningNames := map[string]bool{}
		for _, inst := range m.ListInstances() {
			if inst.Status.State == "Running" {
				runningNames[inst.Name] = true
			}
		}
		summary.Total = len(runningNames)
		for _, rec := range recs {
			if !runningNames[rec.Name] {
				continue
			}
			summary.Records = append(summary.Records, rec)
			if rec.Healthy {
				summary.Healthy++
			} else {
				summary.Unhealthy++
			}
		}
		sort.Slice(summary.Records, func(i, j int) bool { return summary.Records[i].Name < summary.Records[j].Name })
		if summary.LastScanTS == 0 {
			summary.LastScanTS = time.Now().Unix()
		}
		writeJSON(w, summary)
	}
}