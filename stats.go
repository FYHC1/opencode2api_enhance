// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type ModelStats struct {
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type TokenStatsData struct {
	TotalRequests int64                  `json:"total_requests"`
	Models        map[string]*ModelStats `json:"models"`
}

var (
	tokenStats     = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsMu   sync.Mutex
	tokenStatsPath = "stats.json"
	// CONC-3（H2）：统计写盘合并为单写者——recordXxx 锁内只累加 + 置 dirty，
	// 写盘由进程级后台 goroutine 周期执行，杜绝每请求 spawn 写盘 goroutine。
	tokenStatsDirty    bool
	tokenStatsFlushCnt int64 // 实际落盘次数（测试断言 dirty 机制用）
)

// statsFlushInterval 后台统计写盘周期（atomic：测试可动态注入缩短）。
var statsFlushInterval atomic.Int64

func init() {
	statsFlushInterval.Store(int64(5 * time.Second))
}

// ======================== 节点 Token 统计 ========================
// 网关/代理池模式下按实际选中的 SOCKS5 出口（节点）累计 token 统计，
// 供统计界面展示「统一网关总体 + 各节点明细」。

type NodeStat struct {
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type NodeStatsData struct {
	TotalRequests int64                `json:"total_requests"`
	Nodes         map[string]*NodeStat `json:"nodes"`
}

var (
	nodeStats         = &NodeStatsData{Nodes: map[string]*NodeStat{}}
	nodeStatsMu       sync.Mutex
	nodeStatsPath     = "node_stats.json"
	nodeStatsDirty    bool
	nodeStatsFlushCnt int64 // 实际落盘次数（测试断言 dirty 机制用）
)

// markNodeStatsDirty 置 dirty 并惰性启动进程级单写者（首次记录时）。
func markNodeStatsDirty() {
	nodeStatsMu.Lock()
	nodeStatsDirty = true
	nodeStatsMu.Unlock()
	startStatsWriter()
	wakeStatsWriter()
}

// flushNodeStatsNow 同步落盘当前内存节点统计（管理端重置统计/测试用；
// 常规写盘由后台单写者周期执行）。
func flushNodeStatsNow() {
	nodeStatsMu.Lock()
	nodeStatsDirty = false
	nodeStatsFlushCnt++
	data, err := json.MarshalIndent(nodeStats, "", "  ")
	path := nodeStatsPath
	nodeStatsMu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func loadNodeStats() {
	data, err := os.ReadFile(nodeStatsPath)
	if err != nil {
		return
	}
	var st NodeStatsData
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	nodeStatsMu.Lock()
	if st.Nodes == nil {
		st.Nodes = map[string]*NodeStat{}
	}
	nodeStats = &st
	nodeStatsMu.Unlock()
}

func recordNodeUsage(addr string, promptTokens, completionTokens, totalTokens int64) {
	// 节点级统计只对统一网关进程（代理池路由）有意义；
	// 直连实例走自身 sing-box，其记录无人读取，跳过以避免垃圾文件。
	if addr == "" || !gatewayMode {
		return
	}
	nodeStatsMu.Lock()
	nodeStats.TotalRequests++
	ns, ok := nodeStats.Nodes[addr]
	if !ok {
		ns = &NodeStat{}
		nodeStats.Nodes[addr] = ns
	}
	ns.RequestCount++
	ns.PromptTokens += promptTokens
	ns.CompletionTokens += completionTokens
	ns.TotalTokens += totalTokens
	nodeStatsMu.Unlock()
	// CONC-3（H2）：只置 dirty，写盘交给后台单写者（不再每请求 go save）
	markNodeStatsDirty()
}

// ======================== 数据模型 ========================
