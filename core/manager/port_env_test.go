package manager

import (
	"os"
	"testing"
)

// TestInstanceBasePortEnv 验证实例 base 端口环境变量覆盖。
func TestInstanceBasePortEnv(t *testing.T) {
	// 默认
	os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	if got := instanceBasePort(); got != 18100 {
		t.Errorf("default instanceBasePort = %d, want 18100", got)
	}
	// 环境变量覆盖
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "51000")
	if got := instanceBasePort(); got != 51000 {
		t.Errorf("env instanceBasePort = %d, want 51000", got)
	}
	// 非法值回退默认
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "abc")
	if got := instanceBasePort(); got != 18100 {
		t.Errorf("invalid instanceBasePort = %d, want 18100", got)
	}
	os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
}

// TestProbePortsEnv 验证探针端口环境变量覆盖。
func TestProbePortsEnv(t *testing.T) {
	os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")
	if got := probeAPIPort(); got != 19000 {
		t.Errorf("default probeAPIPort = %d, want 19000", got)
	}
	if got := probeSocksPort(); got != 29000 {
		t.Errorf("default probeSocksPort = %d, want 29000", got)
	}
	os.Setenv("OPCODE2API_PROBE_API_PORT", "52000")
	os.Setenv("OPCODE2API_PROBE_SOCKS_PORT", "52100")
	if got := probeAPIPort(); got != 52000 {
		t.Errorf("env probeAPIPort = %d, want 52000", got)
	}
	if got := probeSocksPort(); got != 52100 {
		t.Errorf("env probeSocksPort = %d, want 52100", got)
	}
	os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")
}
