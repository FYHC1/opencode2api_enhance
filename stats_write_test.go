// CONC-3（H2）测试：统计写盘合并为单写者——并发记录 + flush 正确性、
// dirty 机制（无新计数不重复写盘）、并发记录 + 并发 flush（-race）。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// snapshotStatsGlobals 隔离 token/node 统计全局（路径/内存态），测试结束恢复。
func snapshotStatsGlobals(t *testing.T) {
	t.Helper()
	oldPath, oldNodePath := tokenStatsPath, nodeStatsPath
	oldInterval := statsFlushInterval.Load()
	oldToken, oldNode := tokenStats, nodeStats
	oldMode := gatewayMode
	t.Cleanup(func() {
		tokenStatsMu.Lock()
		tokenStatsPath, tokenStats, tokenStatsDirty = oldPath, oldToken, false
		tokenStatsMu.Unlock()
		nodeStatsMu.Lock()
		nodeStatsPath, nodeStats, nodeStatsDirty = oldNodePath, oldNode, false
		nodeStatsMu.Unlock()
		statsFlushInterval.Store(oldInterval)
		gatewayMode = oldMode
	})
}

// 并发 recordTokenUsage/recordNodeUsage N 次 → flush 后 stats.json/node_stats.json
// 内容正确、可解析（并发无交错损坏）；期间叠加并发 flush 检验互斥（-race）。
func TestStatsConcurrentRecordFlushCorrect(t *testing.T) {
	dir := t.TempDir()
	snapshotStatsGlobals(t)
	tokenStatsMu.Lock()
	tokenStatsPath = filepath.Join(dir, "stats.json")
	statsFlushInterval.Store(int64(time.Hour)) // 停用后台周期，本测试全部走显式 flush
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsDirty = false
	tokenStatsFlushCnt = 0
	tokenStatsMu.Unlock()
	nodeStatsMu.Lock()
	nodeStatsPath = filepath.Join(dir, "node_stats.json")
	nodeStats = &NodeStatsData{Nodes: map[string]*NodeStat{}}
	nodeStatsDirty = false
	nodeStatsFlushCnt = 0
	nodeStatsMu.Unlock()
	gatewayMode = true

	const n = 600
	const models = 3
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recordTokenUsage("m"+string(rune('a'+(i%models))), 10, 20, 30, "127.0.0.1:28100")
		}(i)
	}
	wg.Wait()

	flushTokenStatsNow()
	flushNodeStatsNow()

	// token 统计：文件可解析（无交错损坏）、计数正确
	data, err := os.ReadFile(tokenStatsPath)
	if err != nil {
		t.Fatalf("token stats.json 未落盘: %v", err)
	}
	var st TokenStatsData
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("stats.json 损坏（并发写交错）: %v", err)
	}
	if st.TotalRequests != n {
		t.Fatalf("total_requests = %d, want %d", st.TotalRequests, n)
	}
	var sum int64
	for _, ms := range st.Models {
		sum += ms.RequestCount
	}
	if sum != n {
		t.Fatalf("模型计数和 = %d, want %d", sum, n)
	}

	// 节点统计（gatewayMode=true 时一并记录）
	nd, err := os.ReadFile(nodeStatsPath)
	if err != nil {
		t.Fatalf("node_stats.json 未落盘: %v", err)
	}
	var nst NodeStatsData
	if err := json.Unmarshal(nd, &nst); err != nil {
		t.Fatalf("node_stats.json 损坏: %v", err)
	}
	if nst.TotalRequests != n {
		t.Fatalf("node total_requests = %d, want %d", nst.TotalRequests, n)
	}
	if ns := nst.Nodes["127.0.0.1:28100"]; ns == nil || ns.TotalTokens != 30*n {
		t.Fatalf("node stats = %+v, want total_tokens = %d", nst.Nodes, 30*n)
	}

	// 第二阶段：记录与 flush 并发（sync 入口 + 计数互斥安全），最终收敛正确
	const extra = 200
	for i := 0; i < extra; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordTokenUsage("m-a", 1, 2, 3, "127.0.0.1:28100")
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			flushTokenStatsNow()
		}()
	}
	wg.Wait()
	flushTokenStatsNow()

	data, err = os.ReadFile(tokenStatsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("并发 flush 后台期文件损坏: %v", err)
	}
	if st.TotalRequests != n+extra {
		t.Fatalf("并发 flush 后 total_requests = %d, want %d", st.TotalRequests, n+extra)
	}
}

// dirty 机制：无新计数时后台不重复写盘（flush 次数不变）；有新计数则在
// ≤ ticker 周期内自动写盘。
func TestStatsDirtySkipsCleanWrites(t *testing.T) {
	dir := t.TempDir()
	snapshotStatsGlobals(t)
	tokenStatsMu.Lock()
	tokenStatsPath = filepath.Join(dir, "stats.json")
	statsFlushInterval.Store(int64(20 * time.Millisecond))
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsDirty = false
	tokenStatsFlushCnt = 0
	tokenStatsMu.Unlock()

	recordTokenUsage("m1", 1, 2, 3, "") // proxyAddr 空：不触发节点统计
	flushTokenStatsNow()

	// 先等若干周期，让后台在途写盘落定，再快照次数
	time.Sleep(3 * time.Duration(statsFlushInterval.Load()))
	tokenStatsMu.Lock()
	before := tokenStatsFlushCnt
	tokenStatsMu.Unlock()

	// 无新计数：多个周期不得重复写盘
	time.Sleep(5 * time.Duration(statsFlushInterval.Load()))
	tokenStatsMu.Lock()
	after := tokenStatsFlushCnt
	tokenStatsMu.Unlock()
	if after != before {
		t.Fatalf("无新计数仍写盘 %d 次（before=%d, after=%d）", after-before, before, after)
	}

	// 有新计数：后台自动写盘（≤ 1 ticker 周期 + 余量）
	recordTokenUsage("m1", 1, 2, 3, "")
	deadline := time.Now().Add(3 * time.Duration(statsFlushInterval.Load()))
	for {
		tokenStatsMu.Lock()
		c := tokenStatsFlushCnt
		tokenStatsMu.Unlock()
		if c > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("新计数后后台未在周期内自动写盘")
		}
		time.Sleep(2 * time.Millisecond)
	}
}