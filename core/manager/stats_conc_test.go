// CONC-8（L9）测试：统计聚合并发读多目录、统计重置并发 DELETE（fake HTTP 计数
// 并发峰值）与错误按实例名有序收集。
package manager

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// deleteTracker 跨多个 fake 服务器共享的 DELETE 计数（并发峰值跨服务器统计）。
type deleteTracker struct {
	mu    sync.Mutex
	cur   int
	count int
	max   int
}

func (t *deleteTracker) enter() {
	t.mu.Lock()
	t.cur++
	t.count++
	if t.cur > t.max {
		t.max = t.cur
	}
	t.mu.Unlock()
}

func (t *deleteTracker) exit() {
	t.mu.Lock()
	t.cur--
	t.mu.Unlock()
}

func (t *deleteTracker) total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

func (t *deleteTracker) peak() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

// concurrentDeleteServer 统计重置用的 fake HTTP 服务：收到 DELETE 后登记到共享
// tracker（用于统计并发峰值）、短暂驻留模拟真实处理并应答指定状态码。
// 返回端口与停止函数。
func concurrentDeleteServer(t *testing.T, hold time.Duration, status int, tr *deleteTracker) (uint16, func()) {
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
				var req []byte
				tmp := make([]byte, 1024)
				for !bytes.Contains(req, []byte("\r\n\r\n")) && len(req) < 16*1024 {
					n, err := c.Read(tmp)
					if n > 0 {
						req = append(req, tmp[:n]...)
					}
					if err != nil {
						return
					}
				}
				tr.enter()
				defer tr.exit()
				if hold > 0 {
					time.Sleep(hold)
				}
				statusLine := "200 OK"
				if status != 200 {
					statusLine = "500 Internal Server Error"
				}
				body := `{"deleted":true}`
				resp := "HTTP/1.1 " + statusLine + "\r\nContent-Type: application/json\r\nContent-Length: " + intToStr(len(body)) + "\r\n\r\n" + body
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port), func() { ln.Close() }
}

// TestAggregateStatsParallelManyDirs：多目录并发读聚合——总数、实例数、
// 排序与逐个值正确；无 stats.json 目录与普通文件被跳过。
func TestAggregateStatsParallelManyDirs(t *testing.T) {
	m := newTestManager(t)
	const n = 12
	var wantReq int64 = 0
	var wantToken int64 = 0
	for k := 0; k < n; k++ {
		name := fmt.Sprintf("inst%02d", k)
		req := int64(k + 1)
		writeStatsFile(t, m, name, fmt.Sprintf(
			`{"total_requests":%d,"models":{"m":{"request_count":%d,"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}}`,
			req, req, 10*req, 20*req, 30*req))
		_ = m.AddInstance(Instance{Name: name, Port: uint16(20000 + k), SingboxPort: uint16(28000 + k)})
		wantReq += req
		wantToken += 30 * req
	}
	// 统一网关 + node_stats（节点反查实例名）。
	writeStatsFile(t, m, "_unified-gateway", `{"total_requests":99,"models":{"gw":{"request_count":99,"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}}`)
	_ = os.WriteFile(filepath.Join(m.paths.RuntimeDir, "_unified-gateway", "node_stats.json"),
		[]byte(`{"total_requests":99,"nodes":{"127.0.0.1:28005":{"request_count":9,"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}}`), 0o644)
	// 幽灵目录（已从实例列表删除，有 stats.json → 计入但 exists=false）。
	writeStatsFile(t, m, "ghost", `{"total_requests":5,"models":{"g":{"request_count":5,"prompt_tokens":5,"completion_tokens":5,"total_tokens":15}}}`)
	// 无 stats.json 目录 + 普通文件 → 跳过。
	_ = os.MkdirAll(filepath.Join(m.paths.RuntimeDir, "no-stats"), 0o755)
	_ = os.WriteFile(filepath.Join(m.paths.RuntimeDir, "stray.txt"), []byte("x"), 0o644)

	sum := m.AggregateStats()
	if sum.TotalRequests != wantReq+99+5 {
		t.Fatalf("total_requests = %d, want %d", sum.TotalRequests, wantReq+99+5)
	}
	if sum.TotalTokens != wantToken+3+15 {
		t.Fatalf("total_tokens = %d, want %d", sum.TotalTokens, wantToken+3+15)
	}
	// 12 实例 + 网关 + ghost；no-stats/stray 不进。
	if len(sum.Instances) != n+2 {
		t.Fatalf("instances = %d, want %d", len(sum.Instances), n+2)
	}
	// 降序排列：inst11（token 360）在最前，网关（3）在 ghost（15）之后。
	if sum.Instances[0].Name != "inst11" || sum.Instances[0].TotalTokens != 360 {
		t.Fatalf("first = %+v, want inst11/360", sum.Instances[0])
	}
	var gw *InstanceStat
	for i := range sum.Instances {
		s := &sum.Instances[i]
		if s.Name == "统一网关" {
			gw = s
		}
	}
	if gw == nil || !gw.Exists || len(gw.Nodes) != 1 || gw.Nodes[0].Name != "inst05" {
		t.Fatalf("gateway = %+v", gw)
	}
	ghostFound := false
	for i := range sum.Instances {
		s := &sum.Instances[i]
		if s.Name == "ghost" {
			ghostFound = true
			if s.Exists || s.TotalTokens != 15 {
				t.Fatalf("ghost = %+v, want exists=false/token=15", s)
			}
		}
		if s.Name == "no-stats" {
			t.Fatal("no-stats dir must be skipped")
		}
	}
	if !ghostFound {
		t.Fatal("ghost dir missing from result")
	}
}

// TestResetStatsParallelConcurrencyLimited：并发 DELETE 峰值 ≤ statsResetConcurrency
// 且 >1（证明并行确实发生且有上限）。
func TestResetStatsParallelConcurrencyLimited(t *testing.T) {
	m := newTestManager(t)
	const n = 10
	tr := &deleteTracker{}
	ports := make([]uint16, 0, n)
	stops := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		port, stop := concurrentDeleteServer(t, 150*time.Millisecond, 200, tr)
		ports = append(ports, port)
		stops = append(stops, stop)
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("inst%02d", i)
		_ = m.AddInstance(Instance{Name: name, Port: ports[i], Node: "n", Password: "sk"})
		markRunning(t, m, name)
	}
	res := m.ResetStats(false)
	if res.ResetCount != n {
		t.Fatalf("reset_count = %d, want %d; failed=%v", res.ResetCount, n, res.Failed)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed = %v", res.Failed)
	}
	if tr.total() != n {
		t.Fatalf("deletes = %d, want %d", tr.total(), n)
	}
	if mx := tr.peak(); mx > statsResetConcurrency {
		t.Fatalf("concurrent deletes = %d, want ≤ %d", mx, statsResetConcurrency)
	} else if mx < 2 {
		t.Fatalf("concurrent deletes = %d, want > 1（应并行发送）", mx)
	}
}

// TestResetStatsParallelFailedOrdered：并行发送下失败按实例名（列表）顺序收集。
func TestResetStatsParallelFailedOrdered(t *testing.T) {
	m := newTestManager(t)
	codes := []int{200, 200, 500, 200, 500, 200}
	tr := &deleteTracker{}
	ports := make([]uint16, 0, len(codes))
	stops := make([]func(), 0, len(codes))
	for _, code := range codes {
		port, stop := concurrentDeleteServer(t, 30*time.Millisecond, code, tr)
		ports = append(ports, port)
		stops = append(stops, stop)
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()
	for i := range codes {
		name := fmt.Sprintf("inst%02d", i)
		_ = m.AddInstance(Instance{Name: name, Port: ports[i], Node: "n", Password: "sk"})
		markRunning(t, m, name)
	}
	res := m.ResetStats(false)
	if res.ResetCount != 4 {
		t.Fatalf("reset_count = %d, want 4", res.ResetCount)
	}
	want := []string{"inst02: HTTP 500", "inst04: HTTP 500"}
	if len(res.Failed) != len(want) {
		t.Fatalf("failed = %v, want %v", res.Failed, want)
	}
	for i := range want {
		if res.Failed[i] != want[i] {
			t.Fatalf("failed = %v, want %v", res.Failed, want)
		}
	}
	if tr.total() != len(codes) {
		t.Fatalf("deletes = %d, want %d", tr.total(), len(codes))
	}
}