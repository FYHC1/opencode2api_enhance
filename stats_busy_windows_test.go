//go:build windows

// 统计落盘对「文件被占用」的重试与复位失败上报测试（Windows 专属：独占共享
// 句柄模拟管理器聚合读取/直写窗口；非 Windows 无强制占用语义，无需等价测试）。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// lockFileExclusive 以独占共享模式（share=0）打开文件，模拟 Windows 下其它进程
// 持句柄导致的文件占用（与跨进程占用同一机制）。
func lockFileExclusive(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
}

// TestFlushTokenStatsRetriesBusy：stats.json 被占用（模拟管理器聚合读取窗口）期间
// flushTokenStatsNow 短暂重试后成功——重置统计不再因「文件被占用」失败。
func TestFlushTokenStatsRetriesBusy(t *testing.T) {
	dir := t.TempDir()
	snapshotStatsGlobals(t)
	path := filepath.Join(dir, "stats.json")
	tokenStatsMu.Lock()
	tokenStatsPath = path
	statsFlushInterval.Store(int64(time.Hour)) // 停用后台周期，测试全走显式 flush
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsDirty = false
	tokenStatsFlushCnt = 0
	tokenStatsMu.Unlock()
	if err := os.WriteFile(path, []byte(`{"total_requests":5,"models":{"m":{"request_count":5}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	recordTokenUsage("m1", 1, 2, 3, "")
	h, err := lockFileExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		syscall.CloseHandle(h)
	}()
	if err := flushTokenStatsNow(); err != nil {
		t.Fatalf("占用窗口内重试后仍失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st TokenStatsData
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("stats.json 损坏: %v", err)
	}
	if st.TotalRequests != 1 {
		t.Fatalf("total_requests = %d, want 1", st.TotalRequests)
	}
}

// TestResetStatsHandler500OnFlushBusy：占用持续超过重试窗口 → resetStatsHandler
// 返回 500 而非静默 200（管理端据此上报失败，不再出现「提示成功但没清零」）。
func TestResetStatsHandler500OnFlushBusy(t *testing.T) {
	dir := t.TempDir()
	snapshotStatsGlobals(t)
	path := filepath.Join(dir, "stats.json")
	tokenStatsMu.Lock()
	tokenStatsPath = path
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsDirty = false
	tokenStatsMu.Unlock()
	if err := os.WriteFile(path, []byte(`{"total_requests":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := lockFileExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/reset-stats", nil)
	resetStatsHandler(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}
