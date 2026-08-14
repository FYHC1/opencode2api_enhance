package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newTestManager 用临时目录构造管理器。
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := New(dir)
	return m
}

func TestConfigSetGetAllKeys(t *testing.T) {
	m := newTestManager(t)
	cases := map[string]string{
		"base_url":               "http://127.0.0.1:8088/v1",
		"default_password":       "secret",
		"clash_external_url":     "http://127.0.0.1:9090",
		"clash_auth_token":       "tok123",
		"timeout_ttft_min_ms":    "8000",
		"timeout_ttft_max_ms":    "12000",
		"timeout_silence_min_ms": "3000",
		"timeout_silence_max_ms": "6000",
		"failover_probe_min":     "2",
		"failover_probe_max":     "4",
		"call_log_max":           "3333",
		"show_node_prefix":       "true",
		"upstream_proxy":         "socks5://127.0.0.1:7897",
	}
	for k, v := range cases {
		if err := m.ConfigSet(k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
		got, err := m.ConfigGet(k)
		if err != nil {
			t.Fatalf("get %s: %v", k, err)
		}
		if got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	// 落盘：新管理器能读回
	m2 := New(m.paths.DataDir)
	if got, _ := m2.ConfigGet("show_node_prefix"); got != "true" {
		t.Fatalf("persisted show_node_prefix = %q", got)
	}
	if got, _ := m2.ConfigGet("upstream_proxy"); got != "socks5://127.0.0.1:7897" {
		t.Fatalf("persisted upstream_proxy = %q", got)
	}
}

func TestConfigSetInvalidValues(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.ConfigGet("unknown"); err == nil {
		t.Fatal("unknown key must error on get")
	}
	if err := m.ConfigSet("timeout_ttft_min_ms", "abc"); err == nil {
		t.Fatal("invalid int must error")
	}
	if err := m.ConfigSet("show_node_prefix", "maybe"); err == nil {
		t.Fatal("invalid bool must error")
	}
}

// S3: bad_pool_reset_sec 默认 300，可 Get/Set/View，负值拒绝。
func TestConfigBadPoolResetSec(t *testing.T) {
	m := newTestManager(t)
	if v := m.ConfigViewOf(); v.BadPoolResetSec != 300 {
		t.Fatalf("default BadPoolResetSec = %d, want 300", v.BadPoolResetSec)
	}
	if err := m.ConfigSet("bad_pool_reset_sec", "120"); err != nil {
		t.Fatalf("set bad_pool_reset_sec: %v", err)
	}
	if got, _ := m.ConfigGet("bad_pool_reset_sec"); got != "120" {
		t.Fatalf("get = %q, want 120", got)
	}
	if v := m.ConfigViewOf(); v.BadPoolResetSec != 120 {
		t.Fatalf("view = %d, want 120", v.BadPoolResetSec)
	}
	if err := m.ConfigSet("bad_pool_reset_sec", "-5"); err == nil {
		t.Fatal("negative bad_pool_reset_sec must error")
	}
	// 落盘读回。
	m2 := New(m.paths.DataDir)
	if got, _ := m2.ConfigGet("bad_pool_reset_sec"); got != "120" {
		t.Fatalf("persisted = %q, want 120", got)
	}
}

// N2: stop_scan_concurrency 默认 4、1~8 校验（非法回退 4）、落盘读回。
func TestConfigStopScanConcurrency(t *testing.T) {
	m := newTestManager(t)
	// 未设置 → ConfigGet 返回默认 4，ConfigView 生效 4。
	if got, _ := m.ConfigGet("stop_scan_concurrency"); got != "4" {
		t.Fatalf("default get = %q, want 4", got)
	}
	if v := m.ConfigViewOf().StopScanConcurrency; v != 4 {
		t.Fatalf("default view = %d, want 4", v)
	}
	// 正常 6：Get/View 原样 + 落盘读回。
	if err := m.ConfigSet("stop_scan_concurrency", "6"); err != nil {
		t.Fatalf("set 6: %v", err)
	}
	if got, _ := m.ConfigGet("stop_scan_concurrency"); got != "6" {
		t.Fatalf("get = %q, want 6", got)
	}
	if v := m.ConfigViewOf().StopScanConcurrency; v != 6 {
		t.Fatalf("view = %d, want 6", v)
	}
	m2 := New(m.paths.DataDir)
	if got, _ := m2.ConfigGet("stop_scan_concurrency"); got != "6" {
		t.Fatalf("persisted = %q, want 6", got)
	}
	// 非法值（越界 1~8）回退默认 4（落盘为未设置）。
	if err := m.ConfigSet("stop_scan_concurrency", "99"); err != nil {
		t.Fatalf("set 99 should fall back, got err: %v", err)
	}
	if got, _ := m.ConfigGet("stop_scan_concurrency"); got != "4" {
		t.Fatalf("get after 99 = %q, want 4", got)
	}
	if v := m.ConfigViewOf().StopScanConcurrency; v != 4 {
		t.Fatalf("view after 99 = %d, want 4", v)
	}
	// 非整数拒绝。
	if err := m.ConfigSet("stop_scan_concurrency", "abc"); err == nil {
		t.Fatal("invalid int must error")
	}
}

// U3: ui_poll_interval_sec 默认 5、0 = 关闭轮询（持久生效）、非法值回退默认 5。
func TestConfigUiPollIntervalSec(t *testing.T) {
	m := newTestManager(t)
	// 未设置（nil）→ ConfigGet 返回默认 5，ConfigView 生效 5。
	if got, _ := m.ConfigGet("ui_poll_interval_sec"); got != "5" {
		t.Fatalf("default get = %q, want 5", got)
	}
	if v := m.ConfigViewOf().UiPollIntervalSec; v != 5 {
		t.Fatalf("default UiPollIntervalSec = %d, want 5", v)
	}
	// 0 = 关闭轮询：Set 接受、ConfigGet 原样读回、ConfigView 生效 0
	// （关键回归：之前 ConfigView 把 0 归一为 5，观察不到关闭轮询）。
	if err := m.ConfigSet("ui_poll_interval_sec", "0"); err != nil {
		t.Fatalf("set 0: %v", err)
	}
	if got, _ := m.ConfigGet("ui_poll_interval_sec"); got != "0" {
		t.Fatalf("get after set 0 = %q, want 0", got)
	}
	if v := m.ConfigViewOf().UiPollIntervalSec; v != 0 {
		t.Fatalf("view after set 0 = %d, want 0", v)
	}
	// 落盘读回：新管理器（同一目录）重载后 0 仍持久生效。
	m2 := New(m.paths.DataDir)
	if got, _ := m2.ConfigGet("ui_poll_interval_sec"); got != "0" {
		t.Fatalf("persisted get = %q, want 0", got)
	}
	if v := m2.ConfigViewOf().UiPollIntervalSec; v != 0 {
		t.Fatalf("persisted view = %d, want 0", v)
	}
	// 1~60 正常值原样读回。
	if err := m.ConfigSet("ui_poll_interval_sec", "10"); err != nil {
		t.Fatalf("set 10: %v", err)
	}
	if got, _ := m.ConfigGet("ui_poll_interval_sec"); got != "10" {
		t.Fatalf("get after set 10 = %q, want 10", got)
	}
	if v := m.ConfigViewOf().UiPollIntervalSec; v != 10 {
		t.Fatalf("view = %d, want 10", v)
	}
	// 非法值（负数/超界）→ nil（未设置）回退默认 5。
	if err := m.ConfigSet("ui_poll_interval_sec", "-3"); err != nil {
		t.Fatalf("set -3: %v", err)
	}
	if got, _ := m.ConfigGet("ui_poll_interval_sec"); got != "5" {
		t.Fatalf("get after -3 = %q, want 5", got)
	}
	if v := m.ConfigViewOf().UiPollIntervalSec; v != 5 {
		t.Fatalf("view after -3 = %d, want 5", v)
	}
	if err := m.ConfigSet("ui_poll_interval_sec", "99"); err != nil {
		t.Fatalf("set 99: %v", err)
	}
	if got, _ := m.ConfigGet("ui_poll_interval_sec"); got != "5" {
		t.Fatalf("get after 99 = %q, want 5", got)
	}
	// 非整数拒绝。
	if err := m.ConfigSet("ui_poll_interval_sec", "abc"); err == nil {
		t.Fatal("invalid int must error")
	}
}

func TestConfigViewMasking(t *testing.T) {
	m := newTestManager(t)
	_ = m.ConfigSet("default_password", "hunter2")
	_ = m.ConfigSet("clash_auth_token", "tok")
	v := m.ConfigViewOf()
	if v.DefaultPassword != "*******" {
		t.Fatalf("password masked = %q, want *******", v.DefaultPassword)
	}
	if !v.HasPassword || !v.HasClashToken {
		t.Fatalf("has_password=%v has_clash_token=%v", v.HasPassword, v.HasClashToken)
	}
	// 未设置时
	m2 := newTestManager(t)
	v2 := m2.ConfigViewOf()
	if v2.HasPassword {
		t.Fatal("fresh config should have no password")
	}
}

func TestEffectiveDefaultPassword(t *testing.T) {
	m := newTestManager(t)
	if got := m.effectiveDefaultPassword(); got != DefaultPassword {
		t.Fatalf("default = %q, want %q", got, DefaultPassword)
	}
	_ = m.ConfigSet("default_password", "x")
	if got := m.effectiveDefaultPassword(); got != "x" {
		t.Fatalf("effective = %q, want x", got)
	}
}

func TestInstanceRegistryPersistAndStatus(t *testing.T) {
	m := newTestManager(t)
	inst := Instance{Name: "a1", Port: 18100, Node: "n1", SingboxPort: 28100, Status: StatusStopped()}
	if err := m.AddInstance(inst); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := m.AddInstance(Instance{Name: "a1", Port: 18101}); err == nil {
		t.Fatal("dup name must error")
	}
	if err := m.AddInstance(Instance{Name: "a2", Port: 18100}); err == nil {
		t.Fatal("dup port must error")
	}
	found, ok := m.FindInstance("a1")
	if !ok || found.Port != 18100 {
		t.Fatalf("find = %+v, %v", found, ok)
	}
	// 状态外部标签形态
	got := Instance{Name: "a3", Port: 18102, Status: StatusError("boom")}
	if err := m.AddInstance(got); err != nil {
		t.Fatalf("add a3: %v", err)
	}
	_ = m.UpdateInstance(Instance{Name: "a3", Port: 18102, Status: StatusStopped()})
	// JSON 形态
	data, _ := json.Marshal(StatusError("boom"))
	if string(data) != `{"Error":["boom"]}` {
		t.Fatalf("marshal = %s", string(data))
	}
	var st InstanceStatus
	if err := json.Unmarshal([]byte(`{"Error":["x","y"]}`), &st); err != nil || st.State != "Error" || len(st.Error) != 2 {
		t.Fatalf("unmarshal error form = %+v, %v", st, err)
	}
	if err := json.Unmarshal([]byte(`"Running"`), &st); err != nil || st.State != "Running" {
		t.Fatalf("unmarshal string form = %+v, %v", st, err)
	}
	// 持久化读取
	data, err := os.ReadFile(filepath.Join(m.paths.DataDir, "instances.json"))
	if err != nil {
		t.Fatalf("instances.json: %v", err)
	}
	var list []Instance
	if json.Unmarshal(data, &list) != nil || len(list) < 2 {
		t.Fatalf("persisted list: %s", string(data))
	}
}
