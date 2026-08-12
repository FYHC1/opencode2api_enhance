package manager

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// ---- 滑动窗口评分 ----

func TestComputeQualityEmptyWindow(t *testing.T) {
	var rec QualityRecord
	computeQuality(&rec, nil, 1000, 600)
	if rec.Score != 100 || rec.Level != qualityHealthy {
		t.Fatalf("empty window: score=%d level=%s, want 100/healthy", rec.Score, rec.Level)
	}
	if len(rec.Samples) != 0 {
		t.Fatalf("samples should be empty, got %d", len(rec.Samples))
	}
}

func TestComputeQualityAllOK(t *testing.T) {
	samples := []ProbeSample{
		{OK: true, LatencyMS: 100, TS: 1000},
		{OK: true, LatencyMS: 200, TS: 1001},
		{OK: true, LatencyMS: 150, TS: 1002},
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.Score != 100 || rec.Level != qualityHealthy {
		t.Fatalf("all ok: score=%d level=%s, want 100/healthy", rec.Score, rec.Level)
	}
	if rec.SuccessRate != 1.0 || rec.ConsecutiveFailures != 0 {
		t.Fatalf("rate=%v cf=%d, want 1.0/0", rec.SuccessRate, rec.ConsecutiveFailures)
	}
	if rec.AvgLatencyMS != 150 {
		t.Fatalf("avg latency=%d, want 150", rec.AvgLatencyMS)
	}
}

func TestComputeQualityAllFail(t *testing.T) {
	samples := []ProbeSample{
		{OK: false, LatencyMS: 3000, TS: 1000},
		{OK: false, LatencyMS: 3000, TS: 1001},
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.Score != 0 || rec.Level != qualityDown {
		t.Fatalf("all fail: score=%d level=%s, want 0/down", rec.Score, rec.Level)
	}
	if rec.ConsecutiveFailures != 2 {
		t.Fatalf("cf=%d, want 2", rec.ConsecutiveFailures)
	}
}

// 单次失败（窗口内唯一样本）不应判 down，应为 flaky（分数被打到 0 以下）。
func TestComputeQualitySingleFail(t *testing.T) {
	samples := []ProbeSample{{OK: false, LatencyMS: 3000, TS: 1000}}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.Level != qualityFlaky {
		t.Fatalf("single fail: level=%s, want flaky", rec.Level)
	}
}

// 窗口滑出：旧样本不参与评分，且不留在 Samples 中。
func TestComputeQualityWindowSlide(t *testing.T) {
	samples := []ProbeSample{
		{OK: false, LatencyMS: 3000, TS: 100}, // 旧失败（窗口外）
		{OK: true, LatencyMS: 200, TS: 900},   // 新成功（窗口内）
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600) // cutoff = 400
	if len(rec.Samples) != 1 {
		t.Fatalf("samples=%d, want 1 (old slid out)", len(rec.Samples))
	}
	if rec.Score != 100 || rec.Level != qualityHealthy {
		t.Fatalf("after slide: score=%d level=%s, want 100/healthy", rec.Score, rec.Level)
	}
}

// 延迟分档：高延迟（avg>8000）把满分压到 30。
func TestComputeQualityLatencyTier(t *testing.T) {
	samples := []ProbeSample{
		{OK: true, LatencyMS: 9000, TS: 1000},
		{OK: true, LatencyMS: 9000, TS: 1001},
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.Score != 30 {
		t.Fatalf("high latency score=%d, want 30", rec.Score)
	}
	if rec.Level != qualityFlaky {
		t.Fatalf("level=%s, want flaky", rec.Level)
	}

	// 中等延迟（avg 2000，>1000）压 0.8 倍 → 80 → degraded。
	samples2 := []ProbeSample{
		{OK: true, LatencyMS: 2000, TS: 1000},
		{OK: true, LatencyMS: 2000, TS: 1001},
	}
	var rec2 QualityRecord
	computeQuality(&rec2, samples2, 1000, 600)
	if rec2.Score != 80 || rec2.Level != qualityDegraded {
		t.Fatalf("mid latency: score=%d level=%s, want 80/degraded", rec2.Score, rec2.Level)
	}
}

// 连续失败计数：末尾 1 次失败 + 前面成功 → 成功率 0.5、扣 15 分 → flaky。
func TestComputeQualityConsecutiveFailures(t *testing.T) {
	samples := []ProbeSample{
		{OK: true, LatencyMS: 100, TS: 1000},
		{OK: false, LatencyMS: 3000, TS: 1001},
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.ConsecutiveFailures != 1 {
		t.Fatalf("cf=%d, want 1", rec.ConsecutiveFailures)
	}
	// rate=0.5 → 50；avg=1550 触发延迟分档 ×0.8 → 40；连续失败扣 15 → 25；score<50 → flaky。
	if rec.Score != 25 || rec.Level != qualityFlaky {
		t.Fatalf("score=%d level=%s, want 25/flaky", rec.Score, rec.Level)
	}

	// 连续 3 次失败 → down。
	three := []ProbeSample{
		{OK: true, LatencyMS: 100, TS: 1000},
		{OK: false, LatencyMS: 3000, TS: 1001},
		{OK: false, LatencyMS: 3000, TS: 1002},
		{OK: false, LatencyMS: 3000, TS: 1003},
	}
	var rec3 QualityRecord
	computeQuality(&rec3, three, 1000, 600)
	if rec3.ConsecutiveFailures != 3 || rec3.Level != qualityDown {
		t.Fatalf("three fails: cf=%d level=%s, want 3/down", rec3.ConsecutiveFailures, rec3.Level)
	}
}

// 成功率 < 0.9 → degraded（即便无连续失败）。
func TestComputeQualityLowRate(t *testing.T) {
	samples := []ProbeSample{
		{OK: false, LatencyMS: 100, TS: 1000},
		{OK: false, LatencyMS: 100, TS: 1001},
		{OK: true, LatencyMS: 100, TS: 1002},
		{OK: true, LatencyMS: 100, TS: 1003},
		{OK: true, LatencyMS: 100, TS: 1004},
		{OK: true, LatencyMS: 100, TS: 1005},
		{OK: true, LatencyMS: 100, TS: 1006},
		{OK: true, LatencyMS: 100, TS: 1007},
		{OK: true, LatencyMS: 100, TS: 1008},
		{OK: true, LatencyMS: 100, TS: 1009},
	}
	var rec QualityRecord
	computeQuality(&rec, samples, 1000, 600)
	if rec.SuccessRate != 0.8 {
		t.Fatalf("rate=%v, want 0.8", rec.SuccessRate)
	}
	if rec.Level != qualityDegraded {
		t.Fatalf("level=%s, want degraded", rec.Level)
	}
}

// ---- 探活目标 URL ----

func TestProbeTargetURL(t *testing.T) {
	if got := probeTargetURL(Config{}); got != defaultProbeTarget {
		t.Fatalf("default target=%s, want %s", got, defaultProbeTarget)
	}
	if got := probeTargetURL(Config{BaseURL: "http://127.0.0.1:8088/v1"}); got != "http://127.0.0.1:8088/v1/models" {
		t.Fatalf("base /v1 target=%s", got)
	}
	if got := probeTargetURL(Config{BaseURL: "http://127.0.0.1:8088/"}); got != "http://127.0.0.1:8088/v1/models" {
		t.Fatalf("base / target=%s", got)
	}
	if got := probeTargetURL(Config{BaseURL: "  https://x.example  "}); got != "https://x.example/v1/models" {
		t.Fatalf("trimmed target=%s", got)
	}
}

// ---- 配置生效值 ----

func TestPoolProbeConfigDefaults(t *testing.T) {
	cfg := Config{}
	if got := poolProbeInterval(cfg); got != 45 {
		t.Fatalf("interval=%d, want 45", got)
	}
	if got := poolProbeTimeout(cfg); got != 3*time.Second {
		t.Fatalf("timeout=%v, want 3s", got)
	}
	if got := poolQualityWindowSec(cfg); got != 600 {
		t.Fatalf("window=%d, want 600", got)
	}
	if !poolProbeEnabled(cfg) {
		t.Fatal("enabled default should be true")
	}

	cfg2 := Config{PoolProbeIntervalSec: 10, PoolProbeTimeoutSec: 5, PoolQualityWindowMin: 20}
	if got := poolProbeInterval(cfg2); got != 10 {
		t.Fatalf("interval=%d, want 10", got)
	}
	if got := poolProbeTimeout(cfg2); got != 5*time.Second {
		t.Fatalf("timeout=%v, want 5s", got)
	}
	if got := poolQualityWindowSec(cfg2); got != 1200 {
		t.Fatalf("window=%d, want 1200", got)
	}

	disabled := false
	cfg3 := Config{PoolProbeEnabled: &disabled}
	if poolProbeEnabled(cfg3) {
		t.Fatal("explicit false should disable")
	}
}

// ---- 配置解析（ConfigSet / ConfigGet / ConfigViewOf） ----

func TestPoolProbeConfigSetGet(t *testing.T) {
	m := New(t.TempDir())

	if err := m.ConfigSet("pool_probe_interval_sec", "60"); err != nil {
		t.Fatalf("set interval: %v", err)
	}
	if got, _ := m.ConfigGet("pool_probe_interval_sec"); got != "60" {
		t.Fatalf("get interval=%s, want 60", got)
	}
	if err := m.ConfigSet("pool_probe_timeout_sec", "7"); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
	if err := m.ConfigSet("pool_quality_window_min", "15"); err != nil {
		t.Fatalf("set window: %v", err)
	}
	if err := m.ConfigSet("pool_probe_enabled", "false"); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if got, _ := m.ConfigGet("pool_probe_enabled"); got != "false" {
		t.Fatalf("get enabled=%s, want false", got)
	}

	// 非法值回退（保持原值并报错）。
	if err := m.ConfigSet("pool_probe_interval_sec", "-1"); err == nil {
		t.Fatal("negative interval should error")
	}
	if got, _ := m.ConfigGet("pool_probe_interval_sec"); got != "60" {
		t.Fatalf("interval after invalid set=%s, want 60", got)
	}
	if err := m.ConfigSet("pool_probe_enabled", "notabool"); err == nil {
		t.Fatal("invalid bool should error")
	}

	// ConfigViewOf 反映生效值。
	v := m.ConfigViewOf()
	if v.PoolProbeIntervalSec != 60 || v.PoolProbeTimeoutSec != 7 || v.PoolQualityWindowMin != 15 {
		t.Fatalf("view=%d/%d/%d, want 60/7/15", v.PoolProbeIntervalSec, v.PoolProbeTimeoutSec, v.PoolQualityWindowMin)
	}
	if v.PoolProbeEnabled {
		t.Fatal("view enabled should be false")
	}
}

// ---- 持久化 ----

func TestPoolQualityFileRoundtrip(t *testing.T) {
	m := New(t.TempDir())
	recs := []QualityRecord{
		{Name: "n1", Port: 14400, SingboxPort: 16400, Score: 100, Level: qualityHealthy,
			Samples: []ProbeSample{{OK: true, LatencyMS: 100, TS: 1000}}},
		{Name: "n2", Port: 14401, SingboxPort: 16401, Score: 0, Level: qualityDown,
			ConsecutiveFailures: 3},
	}
	m.savePoolQuality(recs)
	loaded := m.loadPoolQuality()
	if len(loaded) != 2 || loaded[0].Name != "n1" || loaded[1].Level != qualityDown {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded[0].Samples[0].LatencyMS != 100 {
		t.Fatalf("sample lost: %+v", loaded[0].Samples)
	}
}

func TestPoolQualityFileCorrupt(t *testing.T) {
	m := New(t.TempDir())
	if err := os.MkdirAll(m.paths.RuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.paths.RuntimeDir, "pool_quality.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.loadPoolQuality(); got != nil {
		t.Fatalf("corrupt file should load nil, got %+v", got)
	}
}

// ---- 测试用最小 SOCKS5 代理 ----

type testSocksMode int

const (
	socksModeProxy  testSocksMode = iota // CONNECT 后双向转发到真实目标
	socksModeReject                      // CONNECT 一律拒绝
	socksModeHang                        // 握手后不响应（模拟卡死）
)

// startTestSocks5 起一个无鉴权的最小 SOCKS5 代理，返回监听地址。
func startTestSocks5(t *testing.T, mode testSocksMode) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleTestSocksConn(conn, mode)
		}
	}()
	return ln.Addr().(*net.TCPAddr).String()
}

func handleTestSocksConn(conn net.Conn, mode testSocksMode) {
	defer conn.Close()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil { // 无鉴权
		return
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[3] != 0x03 { // 仅支持域名类型
		return
	}
	lb := make([]byte, 1)
	if _, err := io.ReadFull(conn, lb); err != nil {
		return
	}
	host := make([]byte, lb[0])
	if _, err := io.ReadFull(conn, host); err != nil {
		return
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return
	}
	target := net.JoinHostPort(string(host), strconv.Itoa(int(binary.BigEndian.Uint16(portB))))

	switch mode {
	case socksModeReject:
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	case socksModeHang:
		return // 不响应 CONNECT
	}

	up, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(up, conn) }()
	go func() { defer wg.Done(); _, _ = io.Copy(conn, up) }()
	wg.Wait()
}

// socksPort 从 "127.0.0.1:port" 解析端口。
func socksPort(t *testing.T, addr string) uint16 {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return uint16(p)
}

// ---- 链路探测三态 ----

func TestHTTPGetViaSocksSuccess(t *testing.T) {
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path=%s, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer back.Close()

	socksAddr := startTestSocks5(t, socksModeProxy)
	ok, err := httpGetViaSocks(socksPort(t, socksAddr), back.URL+"/v1/models", 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want true/nil", ok, err)
	}
}

func TestHTTPGetViaSocksReject(t *testing.T) {
	socksAddr := startTestSocks5(t, socksModeReject)
	ok, err := httpGetViaSocks(socksPort(t, socksAddr), "http://127.0.0.1:1/v1/models", 2*time.Second)
	if err == nil || ok {
		t.Fatalf("reject: ok=%v err=%v, want false+err", ok, err)
	}
}

func TestHTTPGetViaSocksTimeout(t *testing.T) {
	socksAddr := startTestSocks5(t, socksModeHang)
	ok, err := httpGetViaSocks(socksPort(t, socksAddr), "http://127.0.0.1:1/v1/models", 200*time.Millisecond)
	if err == nil || ok {
		t.Fatalf("hang: ok=%v err=%v, want false+err", ok, err)
	}
}

// 5xx 视为链路失败。
func TestHTTPGetViaSocksServerError(t *testing.T) {
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer back.Close()
	socksAddr := startTestSocks5(t, socksModeProxy)
	ok, err := httpGetViaSocks(socksPort(t, socksAddr), back.URL+"/v1/models", 2*time.Second)
	if ok || err == nil {
		t.Fatalf("5xx: ok=%v err=%v, want false+err", ok, err)
	}
}

// ---- 探活调度（RunPoolQualityOnce） ----

func TestRunPoolQualityOnce(t *testing.T) {
	m := New(t.TempDir())

	// 后端目标（经 SOCKS 转发可达）。
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer back.Close()

	goodSocks := startTestSocks5(t, socksModeProxy)
	badSocks := startTestSocks5(t, socksModeReject)

	// 预置 3 实例：good（Running，链路通）、bad（Running，链路拒）、stopped（Stopped，不探测）。
	_ = m.AddInstance(Instance{Name: "good", Port: 14400, SingboxPort: socksPort(t, goodSocks), Node: "n1", Password: "sk"})
	_ = m.AddInstance(Instance{Name: "bad", Port: 14401, SingboxPort: socksPort(t, badSocks), Node: "n2", Password: "sk"})
	_ = m.AddInstance(Instance{Name: "stopped", Port: 14402, SingboxPort: socksPort(t, goodSocks), Node: "n3", Password: "sk"})
	for _, inst := range m.ListInstances() {
		inst.Status = StatusRunning()
		if inst.Name == "stopped" {
			inst.Status = StatusStopped()
		}
		_ = m.UpdateInstance(inst)
	}

	// 配置探测目标指向本地后端（base_url 生效）。
	_ = m.ConfigSet("base_url", back.URL+"/v1")
	_ = m.ConfigSet("pool_probe_timeout_sec", "2")

	// 第 1 轮：good 健康，bad 单次失败 → flaky（down 需连续 3 次失败）。
	summary := m.RunPoolQualityOnce(&fakeRunner{})
	if summary.Total != 2 {
		t.Fatalf("total=%d, want 2 (stopped excluded)", summary.Total)
	}
	if summary.Probed != 2 {
		t.Fatalf("probed=%d, want 2", summary.Probed)
	}
	if summary.Healthy != 1 || summary.Flaky != 1 {
		t.Fatalf("round1 healthy=%d flaky=%d, want 1/1", summary.Healthy, summary.Flaky)
	}
	for _, rec := range summary.Records {
		if rec.Name == "good" && rec.Level != qualityHealthy {
			t.Fatalf("good level=%s, want healthy", rec.Level)
		}
		if rec.Name == "bad" && rec.Level != qualityFlaky {
			t.Fatalf("bad level=%s, want flaky", rec.Level)
		}
	}

	// 连跑 3 轮：bad 连续失败累计到 3 → down；good 始终 healthy（恢复路径）。
	for i := 0; i < 2; i++ {
		m.RunPoolQualityOnce(&fakeRunner{})
	}
	round3 := m.RunPoolQualityOnce(&fakeRunner{})
	if round3.Down != 1 || round3.Healthy != 1 {
		t.Fatalf("round3 healthy=%d down=%d, want 1/1", round3.Healthy, round3.Down)
	}
	for _, rec := range round3.Records {
		if rec.Name == "bad" && rec.Level != qualityDown {
			t.Fatalf("bad round3 level=%s, want down", rec.Level)
		}
		if rec.Name == "good" && rec.Level != qualityHealthy {
			t.Fatalf("good round3 level=%s, want healthy", rec.Level)
		}
	}

	// 持久化已落盘且含 2 条。
	loaded := m.loadPoolQuality()
	if len(loaded) != 2 {
		t.Fatalf("persisted %d, want 2", len(loaded))
	}
	for _, rec := range loaded {
		if rec.Name == "bad" && rec.ConsecutiveFailures < 3 {
			t.Fatalf("bad cf=%d, want >=3", rec.ConsecutiveFailures)
		}
	}

	// GET 视图：非 Running 的陈旧记录被过滤。
	_ = m.UpdateInstance(Instance{Name: "good", Port: 14400, SingboxPort: socksPort(t, goodSocks), Node: "n1", Password: "sk", Status: StatusStopped()})
	view := m.poolQualityView()
	if view.Total != 1 {
		t.Fatalf("view total=%d, want 1", view.Total)
	}
}
