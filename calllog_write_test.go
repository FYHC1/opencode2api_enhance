// CONC-3（M4 写侧）测试：EventLog 落盘移出请求路径——Append 只入队、
// Flush 同步排空、SetMaxRecords 热加载截断、写侧轮转、并发 Append+Flush（-race）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEventLogAppendDoesNotWriteUntilFlush：Append 锁内只入队待写缓冲，
// 写盘发生在 Flush 或后台写者；未 Flush 前文件不存在。
func TestEventLogAppendDoesNotWriteUntilFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	callLogWriteInterval.Store(int64(time.Hour)) // 停用后台周期，隔离验证 Flush 语义
	defer callLogWriteInterval.Store(oldInterval)

	l := NewEventLog(10)
	l.SetPath(path)
	defer l.Stop()

	const n = 50
	for i := 0; i < n; i++ {
		if err := l.Append(CallRecord{ReqID: fmt.Sprintf("r%d", i), Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Append 后不应立即写盘（异步单写者）")
	}

	l.Flush()
	recs := readRecords(t, path)
	if len(recs) != n {
		t.Fatalf("flush 后行数 = %d, want %d", len(recs), n)
	}
	if recs[0].ReqID != "r0" || recs[n-1].ReqID != fmt.Sprintf("r%d", n-1) {
		t.Fatalf("内容/顺序错误: first=%q last=%q", recs[0].ReqID, recs[n-1].ReqID)
	}

	// Stop 后内存 ring 仍可用（持久化只剩显式 Flush；fd 已由 Stop 关闭）
	l.Stop()
	l.Append(CallRecord{ReqID: "after-stop", Status: "ok"})
	recs = l.ReadAll()
	if len(recs) != 10 { // maxRecords=10 截断
		t.Fatalf("Stop 后 ring 应可继续更新: got %d 条", len(recs))
	}
	if recs[9].ReqID != "after-stop" {
		t.Fatalf("Stop 后最新记录缺失: %+v", recs)
	}
}

// TestEventLogSetMaxRecordsHotReload：热加载就地调上限——截断保序、
// 不丢新记录，且不整体替换对象（待写缓冲/写者生命周期保留）。
func TestEventLogSetMaxRecordsHotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	callLogWriteInterval.Store(int64(time.Hour))
	defer callLogWriteInterval.Store(oldInterval)

	l := NewEventLog(5)
	l.SetPath(path)
	defer l.Stop()

	for i := 0; i < 4; i++ {
		l.Append(CallRecord{ReqID: fmt.Sprintf("old-%d", i), Status: "ok"})
	}
	// 收缩：截断保序，只留最新 2 条
	l.SetMaxRecords(2)
	recs := l.ReadAll()
	if len(recs) != 2 || recs[0].ReqID != "old-2" || recs[1].ReqID != "old-3" {
		t.Fatalf("收缩后 ring = %+v, want [old-2 old-3]", recs)
	}
	// 收缩后新记录不被截断前的旧环干扰；扩容就地产生效
	l.Append(CallRecord{ReqID: "new-1", Status: "ok"})
	l.SetMaxRecords(10)
	l.Append(CallRecord{ReqID: "new-2", Status: "ok"})
	recs = l.ReadAll()
	if len(recs) != 3 || recs[0].ReqID != "old-3" || recs[2].ReqID != "new-2" {
		t.Fatalf("扩容后 ring = %+v, want [old-3 new-1 new-2]", recs)
	}

	// 落盘侧不丢：6 次 Append 的待写缓冲全部写出
	l.Flush()
	recs = readRecords(t, path)
	if len(recs) != 6 {
		t.Fatalf("文件行数 = %d, want 6（热加载不丢记录）", len(recs))
	}
}

// TestEventLogRotation：超过轮转阈值 → 关闭 fd、滚动 .1（覆盖旧 .1）、
// 新文件只含新记录。使用注入的小阈值。
func TestEventLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	oldRotate := callLogRotateBytes.Load()
	callLogWriteInterval.Store(int64(time.Hour))
	defer callLogWriteInterval.Store(oldInterval)
	defer callLogRotateBytes.Store(oldRotate)
	callLogRotateBytes.Store(100) // 小阈值：几次写即超限

	l := NewEventLog(10)
	l.SetPath(path)
	defer l.Stop()

	// 旧批次：单条 JSON 明显大于阈值的一半（约 110B），3 条 ≈ 330B
	big := strings.Repeat("x", 60)
	for i := 0; i < 3; i++ {
		l.Append(CallRecord{ReqID: fmt.Sprintf("old-%d", i), Status: "ok", ErrMsg: big})
	}
	l.Flush()

	// 新批次：写前文件超阈值 → 轮转，旧批次进入 .1，新文件只含新记录
	for i := 0; i < 2; i++ {
		l.Append(CallRecord{ReqID: fmt.Sprintf("new-%d", i), Status: "ok", ErrMsg: big})
	}
	l.Flush()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf(".1 滚动文件未生成: %v", err)
	}
	recs := readRecords(t, path)
	if len(recs) != 2 || recs[0].ReqID != "new-0" || recs[1].ReqID != "new-1" {
		t.Fatalf("新文件记录 = %+v, want [new-0 new-1]", recs)
	}
	oldRecs := readRecords(t, path+".1")
	if len(oldRecs) != 3 || oldRecs[0].ReqID != "old-0" {
		t.Fatalf(".1 记录 = %+v, want [old-0 old-1 old-2]", oldRecs)
	}
}

// TestEventLogConcurrentAppendFlush：并发 Append + Flush + ReadAll（-race），
// 最终文件行数与内容完整（单写者串行化，无交错/重复）。
func TestEventLogConcurrentAppendFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	callLogWriteInterval.Store(int64(2 * time.Millisecond)) // 后台写者高频参与
	defer callLogWriteInterval.Store(oldInterval)

	l := NewEventLog(5000)
	l.SetPath(path)
	defer l.Stop()

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := l.Append(CallRecord{ReqID: fmt.Sprintf("w%d-%d", w, i), Status: "ok"}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(2)
	go func() {
		defer aux.Done()
		for {
			select {
			case <-stop:
				return
			default:
				l.Flush()
				time.Sleep(time.Millisecond)
			}
		}
	}()
	go func() {
		defer aux.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = l.ReadAll()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	wg.Wait()
	close(stop)
	aux.Wait()
	l.Flush()

	recs := readRecords(t, path)
	if len(recs) != workers*perWorker {
		t.Fatalf("文件行数 = %d, want %d", len(recs), workers*perWorker)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		if seen[r.ReqID] {
			t.Fatalf("记录重复写入: %s", r.ReqID)
		}
		seen[r.ReqID] = true
	}
}