// EventLog.Clear 与 /api/clear-call-log 清空契约测试：
// 清空 = 内存环形 + 待写缓冲 + 磁盘文件（含 .1 旧段）一并清空；
// 代际计数保证清空前的在途待写批次不会在清空后仍写入文件（日志复活）。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEventLogClearRemovesFilesAndRestartsFresh：清空后磁盘文件（含 .1）消失，
// 之后新追加只写新记录（代际生效）。
func TestEventLogClearRemovesFilesAndRestartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	callLogWriteInterval.Store(int64(time.Hour)) // 停用后台周期，全走显式 Flush
	t.Cleanup(func() { callLogWriteInterval.Store(oldInterval) })

	l := NewEventLog(100)
	l.SetPath(path)
	defer l.Stop()
	l.Append(CallRecord{ReqID: "old-1", Status: "ok"})
	l.Append(CallRecord{ReqID: "old-2", Status: "ok"})
	l.Flush()

	if err := l.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("清空后主文件应被删除")
	}
	if recs := l.ReadAll(); len(recs) != 0 {
		t.Fatalf("清空后内存环形应为空, got %d", len(recs))
	}

	// 清空后新追加只落新记录
	l.Append(CallRecord{ReqID: "fresh", Status: "ok"})
	l.Flush()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("新文件未重建: %v", err)
	}
	if strings.Contains(string(data), "old-") {
		t.Fatalf("清空前的记录复活: %s", string(data))
	}
	if !strings.Contains(string(data), "fresh") {
		t.Fatalf("新记录缺失: %s", string(data))
	}
}

// TestEventLogClearRemovesRotatedSegment：清空同时删除轮转旧段 .1。
func TestEventLogClearRemovesRotatedSegment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval, oldRotate := callLogWriteInterval.Load(), callLogRotateBytes.Load()
	callLogWriteInterval.Store(int64(time.Hour))
	callLogRotateBytes.Store(1) // 极小阈值：任何一条都触发轮转
	t.Cleanup(func() {
		callLogWriteInterval.Store(oldInterval)
		callLogRotateBytes.Store(oldRotate)
	})

	l := NewEventLog(100)
	l.SetPath(path)
	defer l.Stop()
	for i := 0; i < 3; i++ {
		l.Append(CallRecord{ReqID: fmt.Sprintf("r%d", i), Status: "ok"})
	}
	l.Flush() // 首批写入主文件（轮转检查在下次写前触发）
	l.Append(CallRecord{ReqID: "r3", Status: "ok"})
	l.Flush() // 二次写：主文件超阈值 → 滚动为 .1，新批写新主文件
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf(".1 旧段应已生成: %v", err)
	}
	if err := l.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("清空后 .1 旧段应被删除")
	}
}

// TestEventLogClearStaleBatchDiscarded：清空期间在途待写批次（代际不符）被丢弃，
// 清空后只保留新记录（并发 + -race 验证锁序 writeMu→mu 无死锁）。
func TestEventLogClearStaleBatchDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	oldInterval := callLogWriteInterval.Load()
	callLogWriteInterval.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { callLogWriteInterval.Store(oldInterval) })

	l := NewEventLog(100)
	l.SetPath(path)
	defer l.Stop()
	for i := 0; i < 5; i++ {
		l.Append(CallRecord{ReqID: fmt.Sprintf("old-%d", i), Status: "ok"})
	}
	l.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			l.Append(CallRecord{ReqID: fmt.Sprintf("race-%d", i), Status: "ok"})
			time.Sleep(2 * time.Millisecond)
		}
	}()
	for i := 0; i < 10; i++ {
		_ = l.Clear()
		time.Sleep(5 * time.Millisecond)
	}
	<-done

	if err := l.Clear(); err != nil {
		t.Fatalf("final clear: %v", err)
	}
	l.Append(CallRecord{ReqID: "final", Status: "ok"})
	l.Flush()
	l.Flush() // 排空后台写者可能再取的批次

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "final") {
		t.Fatalf("清空后文件应为仅 final 一条, got %d 行: %s", len(lines), string(data))
	}
}

// TestClearCallLogHandler：/api/clear-call-log DELETE 清空本进程日志（200）。
func TestClearCallLogHandler(t *testing.T) {
	dir := setupCallLogDir(t)
	callLog.Append(CallRecord{ReqID: "h1", Status: "ok"})
	callLog.Flush()
	if _, err := os.Stat(filepath.Join(dir, "call_log.jsonl")); err != nil {
		t.Fatalf("日志文件应已落盘: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/clear-call-log", nil)
	clearCallLogHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "call_log.jsonl")); err == nil {
		t.Fatal("清空后日志文件应被删除")
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp["status"] != "ok" {
		t.Fatalf("响应 = %s", rr.Body.String())
	}
}

// TestClearCallLogHandlerMethodNotAllowed：非 DELETE 返回 405。
func TestClearCallLogHandlerMethodNotAllowed(t *testing.T) {
	dir := setupCallLogDir(t)
	_ = dir
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/clear-call-log", nil)
	clearCallLogHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}
