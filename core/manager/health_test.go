package manager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProbePortClosed 未监听端口应判定不可达。
func TestProbePortClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind test port")
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if probePort(uint16(port), 200*time.Millisecond) {
		t.Error("closed port should be unreachable")
	}
}

// TestProbePortOpen 监听端口应判定可达。
func TestProbePortOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !probePort(uint16(port), time.Second) {
		t.Error("open port should be reachable")
	}
}

// TestHealthFileRoundtrip 记录持久化 roundtrip。
func TestHealthFileRoundtrip(t *testing.T) {
	m := New(t.TempDir())
	recs := []HealthRecord{
		{Name: "n1", Healthy: true, LastCheckTS: 123, ConsecutiveFailures: 0},
		{Name: "n2", Healthy: false, LastCheckTS: 456, ConsecutiveFailures: 2, LastError: "不可达"},
	}
	m.saveHealthRecords(recs)
	loaded := m.loadHealthRecords()
	if len(loaded) != 2 || loaded[0].Name != "n1" || loaded[1].ConsecutiveFailures != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
}

// TestRunHealthCheckOnce 单轮巡检：Running 统计、探测结果、陈旧记录过滤。
func TestRunHealthCheckOnce(t *testing.T) {
	m := New(t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	openPort := ln.Addr().(*net.TCPAddr).Port

	// 预置实例：2 Running + 1 Stopped（bad 用 59999 大端口，确保未监听且通过 AddInstance 校验）
	_ = m.AddInstance(Instance{Name: "ok", Port: uint16(openPort), Node: "n1", Password: "sk"})
	_ = m.AddInstance(Instance{Name: "bad", Port: 59999, Node: "n2", Password: "sk"})
	_ = m.AddInstance(Instance{Name: "stopped", Port: 59998, Node: "n3", Password: "sk"})
	list := m.ListInstances()
	for i := range list {
		if list[i].Name == "stopped" {
			list[i].Status = StatusStopped()
		} else {
			list[i].Status = StatusRunning()
		}
		_ = m.UpdateInstance(list[i])
	}
	// 预置陈旧记录（stopped 实例的历史健康记录，应被过滤）
	m.saveHealthRecords([]HealthRecord{{Name: "stopped", Healthy: true, LastCheckTS: 1}})

	summary := m.RunHealthCheckOnce(&fakeRunner{})
	if summary.Total != 2 {
		t.Fatalf("total = %d, want 2", summary.Total)
	}
	if summary.Healthy != 1 || summary.Unhealthy != 1 {
		t.Fatalf("healthy=%d unhealthy=%d, want 1/1", summary.Healthy, summary.Unhealthy)
	}
	for _, rec := range summary.Records {
		if rec.Name == "stopped" {
			t.Fatalf("stopped instance stale record leaked: %+v", summary.Records)
		}
	}
	// bad 实例应累计 1 次失败
	for _, rec := range summary.Records {
		if rec.Name == "bad" && rec.ConsecutiveFailures != 1 {
			t.Fatalf("bad consecutive = %d, want 1", rec.ConsecutiveFailures)
		}
	}
}

// TestHealthHandlersHTTP HTTP 冒烟。
func TestHealthHandlersHTTP(t *testing.T) {
	m := New(t.TempDir())
	h := m.HealthCheckHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/health/check", strings.NewReader(`{}`))
	h(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("check code=%d body=%s", rec.Code, rec.Body.String())
	}

	h2 := m.HealthSummaryHandler()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/admin/health/summary", nil)
	h2(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("summary code=%d", rec2.Code)
	}
}

// fakeRunner 复用 instance_test.go 的既有实现（starts/pids/killed 记录）。
var _ = http.MethodPost
