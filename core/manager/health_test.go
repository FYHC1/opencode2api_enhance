package manager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// TestHealthRoundsMutuallyExclusive 后台轮与手动轮互斥（healthMu）：
// 慢探测轮持锁期间，第二轮不得并发执行。
func TestHealthRoundsMutuallyExclusive(t *testing.T) {
	m := New(t.TempDir())
	_ = m.AddInstance(Instance{Name: "i1", Port: 59997, Node: "n1", Password: "sk"})
	inst, _ := m.FindInstance("i1")
	inst.Status = StatusRunning()
	_ = m.UpdateInstance(inst)

	origProbe := probePort
	var gateMu sync.Mutex
	firstBlocked := false
	entered := make(chan struct{})
	release := make(chan struct{})
	probePort = func(_ uint16, _ time.Duration) bool {
		gateMu.Lock()
		first := !firstBlocked
		firstBlocked = true
		gateMu.Unlock()
		if first {
			close(entered)
			<-release // 阻塞首轮探测，模拟慢探测窗口
			return false
		}
		return true
	}
	defer func() { probePort = origProbe }()

	runner := &fakeRunner{}
	round1 := make(chan struct{})
	go func() {
		m.RunHealthCheckOnce(runner)
		close(round1)
	}()
	<-entered // 后台轮已进入探测（持 healthMu）

	round2 := make(chan struct{})
	go func() {
		m.RunHealthCheckOnce(runner)
		close(round2)
	}()
	// 手动轮被 healthMu 挡在门外：后台轮未完成前不得执行。
	select {
	case <-round2:
		t.Fatal("手动巡检与后台巡检并发执行（healthMu 未生效）")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	<-round1
	<-round2
}

// TestHealthRoundDoesNotRestartJustStopped 用户刚停的实例不被健康轮拉起：
// 轮开始快照 Running，探测期间被用户 Stop → 重启决策复核应跳过。
func TestHealthRoundDoesNotRestartJustStopped(t *testing.T) {
	m := New(t.TempDir())
	_ = m.ConfigSet("health_restart_threshold", "2")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	openPort := uint16(ln.Addr().(*net.TCPAddr).Port)

	_ = m.AddInstance(Instance{Name: "ok", Port: openPort, Node: "n1", Password: "sk"})
	_ = m.AddInstance(Instance{Name: "victim", Port: 59997, Node: "n2", Password: "sk"})
	list := m.ListInstances()
	for i := range list {
		list[i].Status = StatusRunning()
		_ = m.UpdateInstance(list[i])
	}
	// 预置 victim 失败计数 = 阈值-1：本轮探测失败后恰好触发重启判定。
	m.saveHealthRecords([]HealthRecord{
		{Name: "ok", Healthy: true, ConsecutiveFailures: 0},
		{Name: "victim", Healthy: false, ConsecutiveFailures: 1, LastError: "旧失败"},
	})

	origProbe := probePort
	entered := make(chan struct{})
	release := make(chan struct{})
	probePort = func(port uint16, _ time.Duration) bool {
		if port == 59997 { // victim 探测阻塞，等测试先停掉实例
			close(entered)
			<-release
			return false
		}
		return true // ok 实例立即健康
	}
	defer func() { probePort = origProbe }()

	runner := &fakeRunner{}
	done := make(chan struct{})
	go func() {
		m.RunHealthCheckOnce(runner)
		close(done)
	}()
	<-entered // victim 探测中（此刻仍 Running）
	// 用户此刻手动停掉 victim
	if err := m.StopInstance(runner, "victim"); err != nil {
		t.Fatalf("stop victim: %v", err)
	}
	close(release)
	<-done

	// 复核应跳过 victim：不得自动重启。
	if len(runner.starts) != 0 {
		t.Fatalf("不应发生自动重启，starts=%+v", runner.starts)
	}
	inst, _ := m.FindInstance("victim")
	if inst.Status.State != "Stopped" {
		t.Fatalf("victim 应保持用户停止状态，got %s", inst.Status.State)
	}
	// victim 失败计数应被清零：避免下次手动启动后立刻被旧计数误重启。
	for _, rec := range m.loadHealthRecords() {
		if rec.Name == "victim" && rec.ConsecutiveFailures != 0 {
			t.Fatalf("victim 失败计数应清零，got %d", rec.ConsecutiveFailures)
		}
	}
}

// TestHealthProbeParallelism 并行探测：多个实例的探测同时进行（barrier 式 seam）。
func TestHealthProbeParallelism(t *testing.T) {
	m := New(t.TempDir())
	const n = 4
	for i := 0; i < n; i++ {
		_ = m.AddInstance(Instance{Name: "i" + itoa(uint16(i)), Port: uint16(59990 + i), Node: "n", Password: "sk"})
	}
	list := m.ListInstances()
	for i := range list {
		list[i].Status = StatusRunning()
		_ = m.UpdateInstance(list[i])
	}

	origProbe := probePort
	var active, maxActive int32
	entered := make(chan struct{}, n)
	release := make(chan struct{})
	probePort = func(_ uint16, _ time.Duration) bool {
		cur := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if cur <= old || atomic.CompareAndSwapInt32(&maxActive, old, cur) {
				break
			}
		}
		entered <- struct{}{}
		<-release // 全部进入后再一起放行
		atomic.AddInt32(&active, -1)
		return true
	}
	defer func() { probePort = origProbe }()

	done := make(chan struct{})
	go func() {
		m.RunHealthCheckOnce(&fakeRunner{})
		close(done)
	}()
	// 等全部 n 个探测同时进入（串行实现同一时刻只会有一个）。
	for i := 0; i < n; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("第 %d 个探测未进入（串行？）", i)
		}
	}
	close(release)
	<-done
	if got := atomic.LoadInt32(&maxActive); got != n {
		t.Fatalf("最大并发探测数 = %d, want %d", got, n)
	}
}
