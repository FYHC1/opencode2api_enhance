package manager

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------- sing-box 增强（远程 main 42fa058 迁移） ----------

func singboxOutboundOf(t *testing.T, yaml string) map[string]any {
	t.Helper()
	nodes, err := parseClashYAML(yaml)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("parse clash: %v", err)
	}
	cfg, err := buildSingboxConfig(nodes[0], 7899)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var doc map[string]any
	if json.Unmarshal(cfg, &doc) != nil {
		t.Fatalf("json: %s", cfg)
	}
	return doc["outbounds"].([]any)[0].(map[string]any)
}

// anytls 标 tls:false（Clash 常见错误写法）也必须生成 tls.enabled: true。
func TestSingboxAnyTLSForcedTLS(t *testing.T) {
	ob := singboxOutboundOf(t, `
proxies:
  - {name: anytls-hk, server: hklumen.094180.xyz, port: 9999, type: anytls, password: p1, tls: false, skip-cert-verify: true}
`)
	if ob["type"] != "anytls" {
		t.Fatalf("type = %v", ob["type"])
	}
	tls := ob["tls"].(map[string]any)
	if tls["enabled"] != true {
		t.Fatalf("anytls tls.enabled must be true, got %v", tls["enabled"])
	}
	if tls["insecure"] != true {
		t.Fatalf("insecure should follow skip-cert-verify")
	}
}

// hysteria2 同样强制 TLS。
func TestSingboxHysteria2ForcedTLS(t *testing.T) {
	ob := singboxOutboundOf(t, `
proxies:
  - {name: hy2, server: jp.example.com, port: 8443, type: hysteria2, password: p1, tls: false}
`)
	if ob["type"] != "hysteria2" {
		t.Fatalf("type = %v", ob["type"])
	}
	if ob["tls"].(map[string]any)["enabled"] != true {
		t.Fatalf("hysteria2 tls must be forced true")
	}
}

// tuic outbound。
func TestSingboxTuic(t *testing.T) {
	ob := singboxOutboundOf(t, `
proxies:
  - {name: tuic1, server: tuic.example.com, port: 7788, type: tuic, uuid: u-123, password: pw, sni: cdn.tuic.com}
`)
	if ob["type"] != "tuic" {
		t.Fatalf("type = %v", ob["type"])
	}
	if ob["uuid"] != "u-123" || ob["password"] != "pw" {
		t.Fatalf("tuic fields = %+v", ob)
	}
	if ob["tls"].(map[string]any)["enabled"] != true {
		t.Fatalf("tuic tls must be true")
	}
	if ob["tls"].(map[string]any)["server_name"] != "cdn.tuic.com" {
		t.Fatalf("tuic sni not honored")
	}
}

// wireguard outbound（private-key/public-key）。
func TestSingboxWireguard(t *testing.T) {
	ob := singboxOutboundOf(t, `
proxies:
  - {name: wg1, server: wg.example.com, port: 51820, type: wireguard, private-key: priv1, public-key: pub1}
`)
	if ob["type"] != "wireguard" {
		t.Fatalf("type = %v", ob["type"])
	}
	if ob["private_key"] != "priv1" {
		t.Fatalf("private_key = %v", ob["private_key"])
	}
	peers := ob["peers"].([]any)[0].(map[string]any)
	if peers["public_key"] != "pub1" {
		t.Fatalf("peer public_key = %v", peers["public_key"])
	}
}

// hysteria v1 outbound（auth-str）。
func TestSingboxHysteria1(t *testing.T) {
	ob := singboxOutboundOf(t, `
proxies:
  - {name: hy1, server: hy.example.com, port: 8443, type: hysteria, auth-str: token1}
`)
	if ob["type"] != "hysteria" {
		t.Fatalf("type = %v", ob["type"])
	}
	if ob["auth_str"] != "token1" {
		t.Fatalf("auth_str = %v", ob["auth_str"])
	}
	if ob["tls"].(map[string]any)["enabled"] != true {
		t.Fatalf("hy1 tls must be true")
	}
}

// DoH DNS + default_domain_resolver。
func TestSingboxDoHDNS(t *testing.T) {
	nodes, err := parseClashYAML("proxies:\n  - {name: t, server: 1.2.3.4, port: 443, type: trojan, password: p}\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildSingboxConfig(nodes[0], 7899)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, `"ali-doh"`) || !strings.Contains(s, `223.5.5.5`) {
		t.Fatalf("missing DoH server: %s", s)
	}
	if !strings.Contains(s, `"default_domain_resolver"`) {
		t.Fatalf("missing default_domain_resolver: %s", s)
	}
}
