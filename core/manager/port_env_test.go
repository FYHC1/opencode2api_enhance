package manager

import (
	"os"
	"testing"
)

// TestInstanceBasePortEnv 验证实例 base 端口环境变量覆盖（env > config > 默认）。
func TestInstanceBasePortEnv(t *testing.T) {
	m := New(t.TempDir())
	// 默认
	os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	if got := m.instanceBasePort(); got != 18100 {
		t.Errorf("default instanceBasePort = %d, want 18100", got)
	}
	// 环境变量覆盖
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "51000")
	if got := m.instanceBasePort(); got != 51000 {
		t.Errorf("env instanceBasePort = %d, want 51000", got)
	}
	// config 次之（env 未设时）
	os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	_ = m.ConfigSet("instance_base_port", "44100")
	if got := m.instanceBasePort(); got != 44100 {
		t.Errorf("config instanceBasePort = %d, want 44100", got)
	}
	// 非法值回退：env 非法 → config 层 → 默认（先清 config 端口项）
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "abc")
	if got := m.instanceBasePort(); got != 44100 {
		t.Errorf("invalid env falls back to config: instanceBasePort = %d, want 44100", got)
	}
	_ = m.ConfigSet("instance_base_port", "0")
	if got := m.instanceBasePort(); got != 18100 {
		t.Errorf("invalid env with no config: instanceBasePort = %d, want 18100", got)
	}
	os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	_ = m.ConfigSet("instance_base_port", "0")
}

// TestProbePortsEnv 验证探针端口环境变量覆盖（env > config > 默认）。
func TestProbePortsEnv(t *testing.T) {
	m := New(t.TempDir())
	os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")
	if got := m.probeAPIPort(); got != 19000 {
		t.Errorf("default probeAPIPort = %d, want 19000", got)
	}
	if got := m.probeSocksPort(); got != 29000 {
		t.Errorf("default probeSocksPort = %d, want 29000", got)
	}
	os.Setenv("OPCODE2API_PROBE_API_PORT", "52000")
	os.Setenv("OPCODE2API_PROBE_SOCKS_PORT", "52100")
	if got := m.probeAPIPort(); got != 52000 {
		t.Errorf("env probeAPIPort = %d, want 52000", got)
	}
	if got := m.probeSocksPort(); got != 52100 {
		t.Errorf("env probeSocksPort = %d, want 52100", got)
	}
	// config 次之
	os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")
	_ = m.ConfigSet("probe_api_port", "44190")
	_ = m.ConfigSet("probe_socks_port", "46190")
	if got := m.probeAPIPort(); got != 44190 {
		t.Errorf("config probeAPIPort = %d, want 44190", got)
	}
	if got := m.probeSocksPort(); got != 46190 {
		t.Errorf("config probeSocksPort = %d, want 46190", got)
	}
	os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")
	_ = m.ConfigSet("probe_api_port", "0")
	_ = m.ConfigSet("probe_socks_port", "0")
}