package manager

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// probeHTTPServer 模拟探针目标：GET /v1/models + POST /v1/chat/completions。
func probeHTTPServer(t *testing.T) (uint16, func()) {
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
				var body string
				switch {
				case strings.Contains(req, "/v1/models"):
					body = `{"object":"list","data":[{"id":"deepseek-v4-flash"},{"id":"pro-x"}]}`
				case strings.Contains(req, "chat/completions"):
					body = `{"choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}]}`
				default:
					body = `{"error":"no"}`
				}
				resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port), func() { _ = ln.Close() }
}

func bytesContains(b, sub []byte) bool {
	if len(b) < len(sub) {
		return false
	}
	return strings.Contains(string(b), string(sub))
}

func TestProbeNodeOK(t *testing.T) {
	m := newTestManager(t)
	apiPort, stop := probeHTTPServer(t)
	defer stop()
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	socksPort := uint16(socksLn.Addr().(*net.TCPAddr).Port)

	run := &fakeRunner{}
	ctrl := NewScanController(m, run)
	pair := portPair{api: apiPort, socks: socksPort}
	node := ClashNode{Name: "ok-node", NodeType: "trojan", Server: "1.2.3.4", Port: 443, Password: "p"}
	res := ctrl.probeNode(0, ScanOptions{TimeoutSec: 8}, node, pair, t.TempDir())

	if !res.OK || res.Category != "ok" {
		t.Fatalf("res = %+v", res)
	}
	if res.ModelCount == nil || *res.ModelCount != 2 {
		t.Fatalf("model_count = %v", res.ModelCount)
	}
	// 防泄漏：成功路径也必须清理两个探针进程（sing-box pid=101、opencode pid=102）。
	// 曾出现每次扫描每个节点残留一对进程，任务管理器堆积数十个 opencode2api/sing-box。
	if len(run.starts) != 2 {
		t.Fatalf("starts = %d, want 2 (sing-box + opencode)", len(run.starts))
	}
	if len(run.killed) != 2 {
		t.Fatalf("killed = %v, want both probe pids cleaned after success", run.killed)
	}
	for _, pid := range []int{101, 102} {
		found := false
		for _, k := range run.killed {
			if k == pid {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("pid %d not killed: %v", pid, run.killed)
		}
	}
}

func TestProbeNodeConfigFail(t *testing.T) {
	m := newTestManager(t)
	ctrl := NewScanController(m, &fakeRunner{})
	node := ClashNode{Name: "bad", NodeType: "relay", Server: "x", Port: 1}
	res := ctrl.probeNode(0, ScanOptions{TimeoutSec: 5}, node, portPair{api: 19900, socks: 29900}, t.TempDir())
	if res.Category != "config" {
		t.Fatalf("category = %q, want config", res.Category)
	}
}

func TestAllocatePorts(t *testing.T) {
	m := newTestManager(t)
	ctrl := NewScanController(m, &fakeRunner{})
	// 25100/26100 避开默认实例段（18200-20200）与 sing-box 段（20200-22200）
	pairs, err := ctrl.allocatePorts(ScanOptions{APIPort: 25100, SocksPort: 26100}, 3)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if len(pairs) != 3 {
		t.Fatalf("pairs = %d", len(pairs))
	}
	if pairs[0].api != 25100 || pairs[0].socks != 26100 {
		t.Fatalf("first pair = %+v", pairs[0])
	}
	if pairs[1].api == pairs[0].socks || pairs[1].socks == pairs[0].api {
		t.Fatalf("port collision: %+v", pairs)
	}
}

func TestFreeCompletionAgainstServer(t *testing.T) {
	port, stop := probeHTTPServer(t)
	defer stop()
	status, body, count, err := freeCompletion(port, "pw", 5*time.Second)
	if err != nil || status != 200 {
		t.Fatalf("status %d err %v", status, err)
	}
	if !strings.Contains(string(body), "OK") {
		t.Fatalf("body = %s", string(body))
	}
	if count != 2 {
		t.Fatalf("model count = %d, want 2", count)
	}
}
