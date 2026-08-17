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
// 常规写盘由后台单写者周期执行）。返回最终写盘错误（重置统计据此判定成败）。
func flushTokenStatsNow() error {
	statsWriteMu.Lock()
	defer statsWriteMu.Unlock()
	tokenStatsMu.Lock()
	tokenStatsDirty = false
	tokenStatsFlushCnt++
	data, err := json.MarshalIndent(tokenStats, "", "  ")
	path := tokenStatsPath
	tokenStatsMu.Unlock()
	if err != nil {
		return err
	}
	if err := writeFileAtomicRetry(path, data, 0o644); err != nil {
		// 落盘失败（Windows 瞬时占用等）：重新置 dirty，后台单写者稍后重试，避免数据静默丢失。
		tokenStatsMu.Lock()
		tokenStatsDirty = true
		tokenStatsMu.Unlock()
		return err
	}
	return nil
}

// writeFileAtomicRetry 原子写（tmp+Rename）并对瞬时跨进程占用重试（Windows：
// 管理器聚合读取/直写与本进程原子替换并发时，Rename 会报 Access denied、
// 直写方会报文件被占用——短暂重试即可越过）。
func writeFileAtomicRetry(path string, data []byte, perm os.FileMode) error {
	var err error
	for i := 0; i < statsWriteRetryAttempts; i++ {
		if err = writeFileAtomic(path, data, perm); err == nil {
			return nil
		}
		if i+1 < statsWriteRetryAttempts {
			time.Sleep(statsWriteRetryDelay)
		}
	}
	return err
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
