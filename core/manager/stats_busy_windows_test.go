//go:build windows

// 统计文件直写对「文件被占用」的重试测试（Windows 专属：独占共享句柄模拟
// 子进程原子写窗口；非 Windows 无强制占用语义，无需等价测试）。
package manager

import (
	"os"
	"path/filepath"
	"strings"
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

// TestWriteEmptyStatsFileRetriesBusy：stats.json 被占用期间直写短暂重试后成功，
// 不再把「文件被占用」抛给重置统计。
func TestWriteEmptyStatsFileRetriesBusy(t *testing.T) {
	m := newTestManager(t)
	path := filepath.Join(m.paths.RuntimeDir, "inst1", "stats.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"total_requests":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := lockFileExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		syscall.CloseHandle(h)
	}()
	if err := writeEmptyStatsFile(path, false); err != nil {
		t.Fatalf("占用窗口内重试后仍失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"total_requests": 0`) {
		t.Fatalf("未覆写为空: %s", string(data))
	}
}
