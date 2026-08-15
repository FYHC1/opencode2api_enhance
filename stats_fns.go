// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

func loadTokenStats() {
	data, err := os.ReadFile(tokenStatsPath)
	if err != nil {
		return
	}
	var st TokenStatsData
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	tokenStatsMu.Lock()
	if st.Models == nil {
		st.Models = map[string]*ModelStats{}
	}
	tokenStats = &st
	tokenStatsMu.Unlock()
}

// markTokenStatsDirty 置 dirty 并惰性启动进程级单写者（首次记录时）。
func markTokenStatsDirty() {
	tokenStatsMu.Lock()
	tokenStatsDirty = true
	tokenStatsMu.Unlock()
	startStatsWriter()
	wakeStatsWriter()
}

// flushTokenStatsNow 同步落盘当前内存统计（管理端重置统计/测试用；
// 常规写盘由后台单写者周期执行）。
func flushTokenStatsNow() {
	tokenStatsMu.Lock()
	tokenStatsDirty = false
	tokenStatsFlushCnt++
	data, err := json.MarshalIndent(tokenStats, "", "  ")
	path := tokenStatsPath
	tokenStatsMu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

var (
	statsWriterOnce sync.Once
	statsWriterWake = make(chan struct{}, 1)
)

// startStatsWriter 惰性启动进程级统计单写者（首次置 dirty 时）。
func startStatsWriter() {
	statsWriterOnce.Do(func() { go statsWriterLoop() })
}

// wakeStatsWriter 通知写者立即检查 dirty（新计数唤醒，不依赖 ticker 周期；
// 有未消费信号时合并，保证批量写）。
func wakeStatsWriter() {
	select {
	case statsWriterWake <- struct{}{}:
	default:
	}
}

// statsWriterLoop 进程级统计单写者：ticker/wake 后检查 dirty，仅 dirty 时落盘。
// 间隔每次动态读取（atomic），便于测试缩短注入。
func statsWriterLoop() {
	for {
		select {
		case <-statsWriterWake:
		case <-time.After(time.Duration(statsFlushInterval.Load())):
		}
		tokenStatsMu.Lock()
		td := tokenStatsDirty
		tokenStatsMu.Unlock()
		if td {
			flushTokenStatsNow()
		}
		nodeStatsMu.Lock()
		nd := nodeStatsDirty
		nodeStatsMu.Unlock()
		if nd {
			flushNodeStatsNow()
		}
	}
}

func recordTokenUsage(model string, promptTokens, completionTokens, totalTokens int64, proxyAddr string) {
	tokenStatsMu.Lock()
	tokenStats.TotalRequests++
	ms, ok := tokenStats.Models[model]
	if !ok {
		ms = &ModelStats{}
		tokenStats.Models[model] = ms
	}
	ms.RequestCount++
	ms.PromptTokens += promptTokens
	ms.CompletionTokens += completionTokens
	ms.TotalTokens += totalTokens
	tokenStatsMu.Unlock()
	recordNodeUsage(proxyAddr, promptTokens, completionTokens, totalTokens)
	// CONC-3（H2）：只置 dirty，写盘交给后台单写者（不再每请求 go save）
	markTokenStatsDirty()
}

// ======================== Thinking/Reasoning 判断 ========================
