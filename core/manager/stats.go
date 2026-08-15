// 统计聚合（Rust commands.rs aggregate_stats / reset_stats 移植）：
// 扫描 runtime/ 各实例目录的 stats.json / node_stats.json（Go 网关写盘格式），
// 汇总为 StatsSummary（前端 /api/admin/stats 契约）。
package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModelStat 单模型统计（前端契约）。
type ModelStat struct {
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

// GatewayNodeStat 统一网关按节点（SOCKS5 出口）拆分统计。
type GatewayNodeStat struct {
	Name             string `json:"name"`
	Addr             string `json:"addr"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

// InstanceStat 单实例统计（exists=false 表示目录存在但已从实例列表删除）。
type InstanceStat struct {
	Name             string            `json:"name"`
	Exists           bool              `json:"exists"`
	Requests         int64             `json:"requests"`
	PromptTokens     int64             `json:"prompt_tokens"`
	CompletionTokens int64             `json:"completion_tokens"`
	TotalTokens      int64             `json:"total_tokens"`
	Models           []ModelStat       `json:"models"`
	Nodes            []GatewayNodeStat `json:"nodes,omitempty"`
}

// StatsSummary 汇总视图。
type StatsSummary struct {
	TotalRequests         int64          `json:"total_requests"`
	TotalPromptTokens     int64          `json:"total_prompt_tokens"`
	TotalCompletionTokens int64          `json:"total_completion_tokens"`
	TotalTokens           int64          `json:"total_tokens"`
	Instances             []InstanceStat `json:"instances"`
}

// GoStatsData / GoNodeStatsData 与 main 包写盘结构一致。
type GoModelStats struct {
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type GoStatsData struct {
	TotalRequests int64                    `json:"total_requests"`
	Models        map[string]*GoModelStats `json:"models"`
}

type GoNodeStat struct {
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type GoNodeStatsData struct {
	TotalRequests int64                  `json:"total_requests"`
	Nodes         map[string]*GoNodeStat `json:"nodes"`
}

// unifiedGatewayName 统一网关目录的展示名。
const unifiedGatewayName = "统一网关"

// statsReadConcurrency 统计聚合并发读取文件上限（避免瞬间打开过多文件句柄）。
const statsReadConcurrency = 8

// statsResetConcurrency 统计重置并发 HTTP DELETE 上限（避免瞬间打爆实例 HTTP）。
const statsResetConcurrency = 4

// AggregateStats 扫描 runtime 目录聚合统计（语义与 Rust aggregate_stats 一致）。
// 各实例目录 stats.json 并发读取（semaphore ≤8），结果按下标收集后按既有目录顺序
// 合并再按 TotalTokens 降序排序；known/portToName 仅并发只读共享。
func (m *Manager) AggregateStats() StatsSummary {
	known := map[string]bool{}
	for _, inst := range m.ListInstances() {
		known[inst.Name] = true
	}
	portToName := map[uint16]string{}
	for _, inst := range m.ListInstances() {
		portToName[inst.SingboxPort] = inst.Name
	}
	entries, err := os.ReadDir(m.paths.RuntimeDir)
	if err != nil {
		return StatsSummary{}
	}
	// 并发读各目录：每 goroutine 只写下标 i 自己的槽位（无共享写竞态）。
	// 解析失败的下标保持零值（Name 为空），合并阶段按 Name 非空过滤跳过。
	results := make([]InstanceStat, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, statsReadConcurrency)
	for i := range entries {
		if !entries[i].IsDir() {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			name := entries[i].Name()
			dir := filepath.Join(m.paths.RuntimeDir, name)
			if stat, ok := readInstanceStatFile(dir, name, known, portToName); ok {
				results[i] = stat
			}
		}()
	}
	wg.Wait()

	// 按下标（os.ReadDir 的目录顺序）合并，保持既有顺序语义。
	instances := []InstanceStat{}
	for i := range results {
		if results[i].Name != "" {
			instances = append(instances, results[i])
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].TotalTokens > instances[j].TotalTokens })

	sum := StatsSummary{Instances: instances}
	for _, inst := range instances {
		sum.TotalRequests += inst.Requests
		sum.TotalPromptTokens += inst.PromptTokens
		sum.TotalCompletionTokens += inst.CompletionTokens
		sum.TotalTokens += inst.TotalTokens
	}
	return sum
}

// readInstanceStatFile 读取单个实例目录的统计文件（统一网关额外合并 node_stats.json）。
// 读取/解析失败返回 ok=false（跳过，与串行语义一致）；known/portToName 仅只读。
func readInstanceStatFile(dir, name string, known map[string]bool, portToName map[uint16]string) (InstanceStat, bool) {
	statsPath := filepath.Join(dir, "stats.json")
	data, err := os.ReadFile(statsPath)
	if err != nil {
		return InstanceStat{}, false
	}
	var goStats GoStatsData
	if json.Unmarshal(data, &goStats) != nil {
		return InstanceStat{}, false
	}
	var requests, prompt, completion, total int64
	models := []ModelStat{}
	for model, ms := range goStats.Models {
		if ms == nil {
			continue
		}
		requests += ms.RequestCount
		prompt += ms.PromptTokens
		completion += ms.CompletionTokens
		total += ms.TotalTokens
		models = append(models, ModelStat{
			Model:            model,
			Requests:         ms.RequestCount,
			PromptTokens:     ms.PromptTokens,
			CompletionTokens: ms.CompletionTokens,
			TotalTokens:      ms.TotalTokens,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].TotalTokens > models[j].TotalTokens })

	display := name
	if name == "_unified-gateway" {
		display = unifiedGatewayName
	}
	nodes := []GatewayNodeStat{}
	if name == "_unified-gateway" {
		if nd, err := os.ReadFile(filepath.Join(dir, "node_stats.json")); err == nil {
			var gns GoNodeStatsData
			if json.Unmarshal(nd, &gns) == nil {
				for addr, ns := range gns.Nodes {
					if ns == nil {
						continue
					}
					nodeName := addr
					if portStr, ok := strings.CutPrefix(addr, "127.0.0.1:"); ok {
						if p, err := strconv.ParseUint(portStr, 10, 16); err == nil {
							if n, ok := portToName[uint16(p)]; ok {
								nodeName = n
							}
						}
					}
					nodes = append(nodes, GatewayNodeStat{
						Name:             nodeName,
						Addr:             addr,
						Requests:         ns.RequestCount,
						PromptTokens:     ns.PromptTokens,
						CompletionTokens: ns.CompletionTokens,
						TotalTokens:      ns.TotalTokens,
					})
				}
				sort.Slice(nodes, func(i, j int) bool { return nodes[i].TotalTokens > nodes[j].TotalTokens })
			}
		}
	}
	return InstanceStat{
		Name:             display,
		Exists:           known[name] || name == "_unified-gateway",
		Requests:         requests,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Models:           models,
		Nodes:            nodes,
	}, true
}

// ResetStatsResult 重置统计的结果汇总（前端契约）。
type ResetStatsResult struct {
	ResetCount   int      `json:"reset_count"`
	DeletedCount int      `json:"deleted_count"`
	Failed       []string `json:"failed"`
}

// writeEmptyStatsFile 覆写为空统计文件（与 Rust 语义一致）。
func writeEmptyStatsFile(path string, isNodes bool) error {
	var v any
	if isNodes {
		v = map[string]any{"total_requests": 0, "nodes": map[string]any{}}
	} else {
		v = map[string]any{"total_requests": 0, "models": map[string]any{}}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ResetStats 重置全部实例与统一网关统计（运行中走 HTTP DELETE，其余覆写磁盘）
// clearDeleted=是否清除已删除实例的历史统计目录。
// 注：HTTP 复位依赖 tcp 包（P4-1 的 httpDo），运行实例复位在 P4-2 装配后完整可用。
// 运行实例的 DELETE 并发发送（semaphore ≤4，避免瞬间打爆实例 HTTP），
// 处理结果按下标收集后按实例列表顺序合并（Failed 顺序稳定）。
func (m *Manager) ResetStats(clearDeleted bool) ResetStatsResult {
	res := ResetStatsResult{Failed: []string{}}
	defaultPW := m.effectiveDefaultPassword()

	instances := m.ListInstances()
	known := map[string]bool{}
	// 单实例处理结果：reset=成功复位、failed=失败文案（零值 = 无文件无需处理）。
	type instReset struct {
		reset  bool
		failed string
	}
	results := make([]instReset, len(instances))
	var wg sync.WaitGroup
	sem := make(chan struct{}, statsResetConcurrency)
	for i := range instances {
		inst := instances[i]
		known[inst.Name] = true
		statsPath := filepath.Join(m.paths.RuntimeDir, inst.Name, "stats.json")
		if inst.Status.State == "Running" {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				pw := inst.Password
				if pw == "" {
					pw = defaultPW
				}
				status, _, err := httpDeleteJSON(inst.Port, "/api/reset-stats", 6*time.Second, pw)
				if err == nil && status >= 200 && status < 300 {
					results[i] = instReset{reset: true}
				} else if err != nil {
					results[i] = instReset{failed: inst.Name + ": " + err.Error()}
				} else {
					results[i] = instReset{failed: inst.Name + ": HTTP " + strconv.Itoa(status)}
				}
			}()
		} else if _, err := os.Stat(statsPath); err == nil {
			if err := writeEmptyStatsFile(statsPath, false); err != nil {
				results[i] = instReset{failed: inst.Name + ": 覆写 stats.json 失败 (" + err.Error() + ")"}
			} else {
				results[i] = instReset{reset: true}
			}
		}
	}
	wg.Wait()
	for i := range results {
		if results[i].reset {
			res.ResetCount++
		}
		if results[i].failed != "" {
			res.Failed = append(res.Failed, results[i].failed)
		}
	}

	// 统一网关
	gwDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	gwOK := false
	if status, _, err := httpDeleteJSON(unifiedGatewayPort, "/api/reset-stats", 6*time.Second, effectiveGatewayKey(m.loadConfig())); err == nil && status >= 200 && status < 300 {
		gwOK = true
	}
	if gwOK {
		res.ResetCount++
	} else {
		any := false
		if data, err := os.ReadFile(filepath.Join(gwDir, "stats.json")); err == nil && len(data) > 0 {
			if writeEmptyStatsFile(filepath.Join(gwDir, "stats.json"), false) == nil {
				any = true
			}
		}
		if data, err := os.ReadFile(filepath.Join(gwDir, "node_stats.json")); err == nil && len(data) > 0 {
			if writeEmptyStatsFile(filepath.Join(gwDir, "node_stats.json"), true) == nil {
				any = true
			}
		}
		if any {
			res.ResetCount++
		}
	}

	if clearDeleted {
		if entries, err := os.ReadDir(m.paths.RuntimeDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if name == "_unified-gateway" || name == "_probe" || known[name] {
					continue
				}
				dir := filepath.Join(m.paths.RuntimeDir, name)
				if _, err := os.Stat(filepath.Join(dir, "stats.json")); err == nil {
					if os.RemoveAll(dir) == nil {
						res.DeletedCount++
					}
				} else if _, err := os.Stat(filepath.Join(dir, "node_stats.json")); err == nil {
					if os.RemoveAll(dir) == nil {
						res.DeletedCount++
					}
				}
			}
		}
	}
	return res
}
