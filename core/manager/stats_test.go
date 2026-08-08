package manager

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAggregateStats(t *testing.T) {
	m := newTestManager(t)
	// 两个实例 + 统一网关
	writeStatsFile(t, m, "inst1", `{"total_requests":10,"models":{"a":{"request_count":6,"prompt_tokens":10,"completion_tokens":20,"total_tokens":30},"b":{"request_count":4,"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}}`)
	writeStatsFile(t, m, "inst2", `{"total_requests":2,"models":{"m":{"request_count":2,"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)
	_ = m.AddInstance(Instance{Name: "inst1", Port: 18100, SingboxPort: 28100})
	// 统一网关 + node_stats
	writeStatsFile(t, m, "_unified-gateway", `{"total_requests":99,"models":{"gw":{"request_count":99,"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}}`)
	nodeDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	_ = os.WriteFile(filepath.Join(nodeDir, "node_stats.json"), []byte(`{"total_requests":99,"nodes":{"127.0.0.1:28100":{"request_count":90,"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"127.0.0.1:29999":{"request_count":9,"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`), 0o644)

	sum := m.AggregateStats()
	if sum.TotalRequests != 111 {
		t.Fatalf("total_requests = %d, want 111", sum.TotalRequests)
	}
	if len(sum.Instances) != 3 {
		t.Fatalf("instances = %d, want 3", len(sum.Instances))
	}
	// inst1 剪枝？不——inst1 在列表 => exists
	// 统一网关：名"统一网关"，exists=true，node 反查名
	var gw *InstanceStat
	for i := range sum.Instances {
		if sum.Instances[i].Name == "统一网关" {
			gw = &sum.Instances[i]
		}
	}
	if gw == nil || !gw.Exists {
		t.Fatal("unified gateway entry missing / not exists")
	}
	if len(gw.Nodes) != 2 {
		t.Fatalf("gateway nodes = %+v", gw.Nodes)
	}
	if gw.Nodes[0].Name != "inst1" {
		t.Fatalf("node name resolution = %+v", gw.Nodes)
	}
	// 排序：gateway total=3, inst1 total=41, inst2 total=2 → 排序依次 inst1/gateway/inst2
	if sum.Instances[0].Name != "inst1" || sum.Instances[1].Name != "统一网关" || sum.Instances[2].Name != "inst2" {
		t.Fatalf("order = %+v", sum.Instances)
	}
}

func TestAggregateStatsMissingOnly(t *testing.T) {
	m := newTestManager(t)
	if sum := m.AggregateStats(); len(sum.Instances) != 0 {
		t.Fatalf("no runtime dir -> empty, got %+v", sum)
	}
}

func TestResetStatsDiskPaths(t *testing.T) {
	m := newTestManager(t)
	// stopped 实例 stats.json → 覆写为空
	writeStatsFile(t, m, "stopped1", `{"total_requests":5,"models":{"m":{"request_count":5}}}`)
	_ = m.AddInstance(Instance{Name: "stopped1", Port: 18100, SingboxPort: 28100})
	// 统一网关文件
	writeStatsFile(t, m, "_unified-gateway", `{"total_requests":7,"models":{"g":{"request_count":7}}}`)
	gwDir := filepath.Join(m.paths.RuntimeDir, "_unified-gateway")
	_ = os.WriteFile(filepath.Join(gwDir, "node_stats.json"), []byte(`{"total_requests":7,"nodes":{"127.0.0.1:28100":{"total_tokens":1}}}`), 0o644)
	// 已删除实例历史目录（清删应移除）
	writeStatsFile(t, m, "ghost", `{"total":1,"models":{}}`)

	res := m.ResetStats(true)
	// 未运行的实例走磁盘覆写（不计 HTTP）
	if res.ResetCount == 0 {
		t.Fatalf("reset_count = %d, want > 0", res.ResetCount)
	}
	// ghost 目录被删
	if _, err := os.Stat(filepath.Join(m.paths.RuntimeDir, "ghost")); err == nil {
		t.Fatal("ghost dir should be removed")
	}
	if res.DeletedCount != 1 {
		t.Fatalf("deleted_count = %d, want 1", res.DeletedCount)
	}
	// 覆写后的内容
	data, _ := os.ReadFile(filepath.Join(m.paths.RuntimeDir, "stopped1", "stats.json"))
	var v map[string]any
	if json.Unmarshal(data, &v) != nil || v["total_requests"].(float64) != 0 {
		t.Fatalf("stats.json not emptied: %s", string(data))
	}
	_ = data
}

// rawHTTPServer 一个直接说 HTTP/1.1 的 TCP 服务端（与探针/网关真实语义一致）。
func rawHTTPServer(t *testing.T) (uint16, func()) {
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
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				req := string(buf[:n])
				var statusLine, body string
				switch {
				case len(req) >= 4 && req[:4] == "POST":
					statusLine, body = "200 OK", `{"posted":true}`
				case len(req) >= 6 && req[:6] == "DELETE":
					statusLine, body = "200 OK", `{"deleted":true}`
				case strings.HasPrefix(req, "GET /x "):
					statusLine, body = "200 OK", `{"ok":true}`
				default:
					statusLine, body = "404 Not Found", `{"error":"nope"}`
				}
				resp := "HTTP/1.1 " + statusLine + "\r\nContent-Type: application/json\r\nContent-Length: " + intToStr(len(body)) + "\r\n\r\n" + body
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port), func() { ln.Close() }
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestHTTPRequestRaw(t *testing.T) {
	port, stop := rawHTTPServer(t)
	defer stop()

	status, body, err := httpGetJSON(port, "/other", 3, "")
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("404 case: %d %s %v", status, string(body), err)
	}
	status, body, err = httpGetJSON(port, "/x", 3, "tok")
	if err != nil || status != http.StatusOK || string(body) != `{"ok":true}` {
		t.Fatalf("200 case: %d %s %v", status, string(body), err)
	}
	status, body, err = httpPostJSON(port, "/x", 3, "tok", []byte(`{"a":1}`))
	if err != nil || status != http.StatusOK || string(body) != `{"posted":true}` {
		t.Fatalf("POST case: %d %s %v", status, string(body), err)
	}
	status, _, err = httpDeleteJSON(port, "/x", 3, "tok")
	if err != nil || status != http.StatusOK {
		t.Fatalf("DELETE case: %d %v", status, err)
	}
	if _, _, err := httpGetJSON(59999, "/x", 1, ""); err == nil {
		t.Fatal("unreachable port must error")
	}
}

func writeStatsFile(t *testing.T, m *Manager, name, content string) {
	t.Helper()
	dir := filepath.Join(m.paths.RuntimeDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
