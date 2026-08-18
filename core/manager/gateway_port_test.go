package manager

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigGatewayPortSetAndValidate(t *testing.T) {
	m := New(t.TempDir())
	// 默认回退
	if p := m.managerGatewayPort(); p != unifiedGatewayPort {
		t.Fatalf("default port = %d", p)
	}
	// 非法端口报错
	if err := m.ConfigSet("gateway_port", "70000"); err == nil {
		t.Fatal("port > 65535 should error")
	}
	if err := m.ConfigSet("gateway_port", "abc"); err == nil {
		t.Fatal("non-numeric port should error")
	}
	// 合法设置
	if err := m.ConfigSet("gateway_port", "50123"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cfg := m.loadConfig(); cfg.GatewayPort != 50123 {
		t.Fatalf("persisted port = %d", cfg.GatewayPort)
	}
	v, err := m.ConfigGet("gateway_port")
	if err != nil || v != "50123" {
		t.Fatalf("ConfigGet port = %q err=%v", v, err)
	}
	// 空串重置默认
	if err := m.ConfigSet("gateway_port", ""); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if cfg := m.loadConfig(); cfg.GatewayPort != 0 {
		t.Fatalf("after reset = %d", cfg.GatewayPort)
	}
}

func TestNewGatewayUsesConfiguredPort(t *testing.T) {
	m := New(t.TempDir())
	_ = m.ConfigSet("gateway_port", "50123")
	gw := NewGateway(m, 0)
	if gw.Port() != 50123 {
		t.Fatalf("gateway port = %d", gw.Port())
	}
}

// T5: 设置端口后网关内存端口立即更新（未运行时静默成功；下次启动用新端口）。
func TestApplyPortUpdatesMemoryPort(t *testing.T) {
	m := New(t.TempDir())
	run := &fakeRunner{}
	gw := NewGateway(m, 0)
	if err := gw.ApplyPort(50123, run); err != nil {
		t.Fatalf("apply when not running: %v", err)
	}
	if gw.Port() != 50123 {
		t.Fatalf("port = %d, want 50123", gw.Port())
	}
	if len(run.starts) != 0 {
		t.Fatalf("must not start when not running, got %+v", run.starts)
	}
	// 经 ConfigSet 保存后，再次构造网关应使用新端口（落盘生效）
	_ = m.ConfigSet("gateway_port", "50123")
	if cfg := m.loadConfig(); cfg.GatewayPort != 50123 {
		t.Fatalf("persisted port = %d", cfg.GatewayPort)
	}
}

func TestGatewayPortHandlerHTTP(t *testing.T) {
	m := New(t.TempDir())
	h := m.ConfigSetHandler()
	// 非法端口 → 400
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/config/set", strings.NewReader(`{"key":"gateway_port","value":"99999"}`))
	h(rec, req)
	if rec.Code != 400 {
		t.Fatalf("invalid port code=%d body=%s", rec.Code, rec.Body.String())
	}
	// 合法 → 200
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/admin/config/set", strings.NewReader(`{"key":"gateway_port","value":"50123"}`))
	h(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("set code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	// ConfigView：gateway_port 回显数值
	gh := m.ConfigGetHandler()
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/admin/config", nil)
	gh(rec3, req3)
	body := rec3.Body.String()
	if !strings.Contains(body, `"gateway_port":50123`) {
		t.Fatalf("view body = %s", body)
	}
}
