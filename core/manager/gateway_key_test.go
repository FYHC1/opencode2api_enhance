package manager

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigGatewayKeySetAndValidate(t *testing.T) {
	m := New(t.TempDir())
	// 默认回退
	if k := effectiveGatewayKey(m.loadConfig()); k != "sk-unified-local" {
		t.Fatalf("default key = %q", k)
	}
	// 短密钥报错
	if err := m.ConfigSet("gateway_key", "short"); err == nil {
		t.Fatal("short key should error")
	}
	// 合法设置
	if err := m.ConfigSet("gateway_key", "my-secret-key"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if k := effectiveGatewayKey(m.loadConfig()); k != "my-secret-key" {
		t.Fatalf("effective = %q", k)
	}
	// 空串重置默认
	if err := m.ConfigSet("gateway_key", ""); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if k := effectiveGatewayKey(m.loadConfig()); k != "sk-unified-local" {
		t.Fatalf("after reset = %q", k)
	}
	// ConfigGet 不回显明文
	if err := m.ConfigSet("gateway_key", "my-secret-key"); err != nil {
		t.Fatal(err)
	}
	v, err := m.ConfigGet("gateway_key")
	if err != nil || v == "my-secret-key" {
		t.Fatalf("ConfigGet leak: %q err=%v", v, err)
	}
}

func TestNewGatewayUsesConfiguredKey(t *testing.T) {
	m := New(t.TempDir())
	_ = m.ConfigSet("gateway_key", "custom-gateway-99")
	gw := NewGateway(m, 0)
	if gw.password != "custom-gateway-99" {
		t.Fatalf("gateway password = %q", gw.password)
	}
}

func TestGatewayKeyHandlerHTTP(t *testing.T) {
	m := New(t.TempDir())
	h := m.ConfigSetHandler()
	// 短密钥 → 400
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/config/set", strings.NewReader(`{"key":"gateway_key","value":"short"}`))
	h(rec, req)
	if rec.Code != 400 {
		t.Fatalf("short key code=%d body=%s", rec.Code, rec.Body.String())
	}
	// 合法 → 200
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/admin/config/set", strings.NewReader(`{"key":"gateway_key","value":"my-secret-key"}`))
	h(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("set code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	// ConfigView：has_gateway_key=true 且 gateway_key 掩码（不含明文）
	gh := m.ConfigGetHandler()
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/admin/config", nil)
	gh(rec3, req3)
	body := rec3.Body.String()
	if !strings.Contains(body, `"has_gateway_key":true`) {
		t.Fatalf("view body = %s", body)
	}
	if strings.Contains(body, "my-secret-key") {
		t.Fatalf("view leaks plaintext: %s", body)
	}
}