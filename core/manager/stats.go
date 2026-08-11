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

// AggregateStats 扫描 runtime 目录聚合统计（语义与 Rust aggregate_stats 一致）。
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
	instances := []InstanceStat{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(m.paths.RuntimeDir, name)
		statsPath := filepath.Join(dir, "stats.json")
		data, err := os.ReadFile(statsPath)
		if err != nil {
			continue
		}
		var goStats GoStatsData
		if json.Unmarshal(data, &goStats) != nil {
			continue
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
		instances = append(instances, InstanceStat{
			Name:             display,
			Exists:           known[name] || name == "_unified-gateway",
			Requests:         requests,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      total,
			Models:           models,
			Nodes:            nodes,
		})
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
func (m *Manager) ResetStats(clearDeleted bool) ResetStatsResult {
	res := ResetStatsResult{Failed: []string{}}
	defaultPW := m.effectiveDefaultPassword()

	instances := m.ListInstances()
	known := map[string]bool{}
	for _, inst := range instances {
		known[inst.Name] = true
		statsPath := filepath.Join(m.paths.RuntimeDir, inst.Name, "stats.json")
		if inst.Status.State == "Running" {
			pw := inst.Password
			if pw == "" {
				pw = defaultPW
			}
			status, _, err := httpDeleteJSON(inst.Port, "/api/reset-stats", 6, pw)
			if err == nil && status >= 200 && status < 300 {
				res.ResetCount++
			} else if err != nil {
				res.Failed = append(res.Failed, inst.Name+": "+err.Error())
			} else {
				res.Failed = append(res.Failed, inst.Name+": HTTP "+strconv.Itoa(status))
			}
		} else if _, err := os.Stat(statsPath); err == nil {
			if err := writeEmptyStatsFile(statsPath, false); err != nil {
				res.Failed = append(res.Failed, inst.Name+": 覆写 stats.json 失败 ("+err.Error()+")")
			} else {
				res.ResetCount++
			}
		}
	}

	// 统一网关
	gwDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	gwOK := false
	if status, _, err := httpDeleteJSON(unifiedGatewayPort, "/api/reset-stats", 6, effectiveGatewayKey(m.loadConfig())); err == nil && status >= 200 && status < 300 {
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
