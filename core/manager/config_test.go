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
