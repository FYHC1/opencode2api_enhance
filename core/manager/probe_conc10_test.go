package manager

// CONC-10 L7/L8 测试：probe 单节点预算共享 deadline + run() 收尾保留错误态。
// 全部单测（fake Runner / 裸 TCP fake 服务器），不 spawn 真实进程、不碰生产端口。

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slowProbeHTTPServer 带响应延迟的探针目标：GET /v1/models 延迟 getDelay、
// POST /v1/chat/completions 延迟 postDelay。
func slowProbeHTTPServer(t *testing.T, getDelay, postDelay time.Duration) (uint16, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				var reqB []byte
				tmp := make([]byte, 2048)
				for !bytesContains(reqB, []byte("\r\n\r\n")) && len(reqB) < 8192 {
					n, err := c.Read(tmp)
					if n > 0 {
						reqB = append(reqB, tmp[:n]...)
					}
					if err != nil {
						break
					}
				}
				req := string(reqB)
				var delay time.Duration
				var body string
				switch {
				case strings.Contains(req, "/v1/models"):
					delay = getDelay
					body = `{"object":"list","data":[{"id":"deepseek-v4-flash"},{"id":"pro-x"}]}`
				case strings.Contains(req, "chat/completions"):
					delay = postDelay
					body = `{"choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}]}`
				default:
					body = `{"error":"no"}`
				}
				if delay > 0 {
					time.Sleep(delay)
				}
				resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port), func() { _ = ln.Close() }
}

// L7：GET/POST 共享同一 deadline——两请求总耗时 ≤ 预算（±余量）。
func TestFreeCompletionSharesBudget(t *testing.T) {
	t.Run("both_fit_shared_deadline", func(t *testing.T) {
		// GET 300ms + POST 300ms = 600ms < 800ms 预算：两请求都在共享预算内成功。
		port, stop := slowProbeHTTPServer(t, 300*time.Millisecond, 300*time.Millisecond)
		defer stop()
		status, body, count, _, err := freeCompletion(port, "pw", 800*time.Millisecond)
		if err != nil || status != 200 {
			t.Fatalf("status=%d err=%v, want 200 OK", status, err)
		}
		if !strings.Contains(string(body), "OK") {
			t.Fatalf("body = %s", string(body))
		}
		if count != 2 {
			t.Fatalf("model count = %d, want 2", count)
		}
	})

	t.Run("post_uses_remaining_budget", func(t *testing.T) {
		// GET 快（100ms），POST 永不完成（2s）。共享 deadline 下 POST 按剩余预算
		// 超时，总耗时 ≤ 预算；旧实现 POST 拿整预算 → 总耗时 GET+预算 超支。
		const budget = 500 * time.Millisecond
		port, stop := slowProbeHTTPServer(t, 100*time.Millisecond, 2*time.Second)
		defer stop()
		start := time.Now()
		status, _, count, _, err := freeCompletion(port, "pw", budget)
		elapsed := time.Since(start)
		if elapsed > budget+200*time.Millisecond {
			t.Fatalf("total elapsed = %v, want ≤ %v (+200ms margin)", elapsed, budget)
		}
		// GET 成功（models 解析出 2 个模型）但 POST 超时。
		if count != 2 {
			t.Fatalf("model count = %d, want 2 (GET must succeed before POST timeout)", count)
		}
		if err == nil && status == 200 {
			t.Fatal("POST should time out within remaining budget")
		}
	})
}

// L8：run() 收尾 defer 保留错误态——allocatePorts 失败置 ScanError 后不得被覆盖成 done。
func TestScanRunAllocatePortsFailureKeepsScanError(t *testing.T) {
	m := newTestManager(t)
	ctrl := NewScanController(m, &fakeRunner{})
	ctrl.mu.Lock()
	ctrl.progress.Status = ScanRunning
	ctrl.mu.Unlock()
	nodes := []ClashNode{{Name: "n1", NodeType: "trojan", Server: "1.2.3.4", Port: 443, Password: "p"}}
	// API/SOCKS 起点端口相同 → 每对 api==socks 被跳过 → allocatePorts 必失败
	//（全部端口检查短路在绑真实端口之前，测试不触碰系统端口）。
	ctrl.run(ScanOptions{APIPort: 25100, SocksPort: 25100, TimeoutSec: 3, Concurrency: 1}, nodes)
	snap := ctrl.Snapshot()
	if snap.Status != ScanError {
		t.Fatalf("status = %s, want ScanError（defer 不得覆盖错误态）", snap.Status)
	}
	if snap.Error == "" {
		t.Fatal("error message empty")
	}
	if snap.FinishedMS == 0 {
		t.Fatal("FinishedMS not written by finalize")
	}
}

// L8：正常收尾路径仍然置 ScanDone（错误态保留改动不回归正常流程）。
func TestScanRunNormalCompletionSetsDone(t *testing.T) {
	m := newTestManager(t)
	ctrl := NewScanController(m, &fakeRunner{})
	ctrl.mu.Lock()
	ctrl.progress.Status = ScanRunning
	ctrl.mu.Unlock()
	// relay 类型在 buildSingboxConfig 阶段失败 → worker 快速返回，不 spawn 进程、不触网。
	nodes := []ClashNode{
		{Name: "n1", NodeType: "relay", Server: "1.2.3.4", Port: 443, Password: "p"},
		{Name: "n2", NodeType: "relay", Server: "1.2.3.4", Port: 443, Password: "p"},
	}
	ctrl.run(ScanOptions{APIPort: 25100, SocksPort: 26100, TimeoutSec: 3, Concurrency: 2}, nodes)
	snap := ctrl.Snapshot()
	if snap.Status != ScanDone {
		t.Fatalf("status = %s, want ScanDone", snap.Status)
	}
	if snap.FinishedMS == 0 {
		t.Fatal("FinishedMS not written")
	}
}