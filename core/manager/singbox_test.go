package manager

import (
	"encoding/json"
	"testing"
)

func singleOutbound(t *testing.T, node ClashNode) map[string]any {
	t.Helper()
	b, err := buildSingboxConfig(node, 18100)
	if err != nil {
		t.Fatalf("singbox: %v", err)
	}
	var cfg map[string]any
	if json.Unmarshal(b, &cfg) != nil {
		t.Fatalf("bad json: %s", string(b))
	}
	return cfg["outbounds"].([]any)[0].(map[string]any)
}

func TestSingboxTrojan(t *testing.T) {
	out := singleOutbound(t, ClashNode{NodeType: "trojan", Server: "a.example", Port: 443, Password: "p1"})
	if out["type"] != "trojan" || out["password"] != "p1" {
		t.Fatalf("out = %+v", out)
	}
	tls := out["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != "a.example" {
		t.Fatalf("tls = %+v", tls)
	}
}

func TestSingboxVlessReality(t *testing.T) {
	node := ClashNode{NodeType: "vless", Server: "r.example", Port: 443, UUID: "u1", Network: "ws",
		RealityPublicKey: "pub", RealityShortID: "abcd", ClientFingerprint: "firefox", Flow: "xtls-rprx-vision"}
	out := singleOutbound(t, node)
	if out["type"] != "vless" || out["flow"] != "xtls-rprx-vision" {
		t.Fatalf("out = %+v", out)
	}
	tls := out["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "pub" || reality["short_id"] != "abcd" {
		t.Fatalf("reality = %+v", reality)
	}
	if out["utls"].(map[string]any)["fingerprint"] != "firefox" {
		t.Fatalf("utls = %+v", out["utls"])
	}
	if out["transport"].(map[string]any)["type"] != "ws" {
		t.Fatalf("transport = %+v", out["transport"])
	}
}

func TestSingboxShadowsocks(t *testing.T) {
	out := singleOutbound(t, ClashNode{NodeType: "ss", Server: "s.example", Port: 8388, Password: "sp"})
	if out["method"] != "aes-256-gcm" || out["password"] != "sp" {
		t.Fatalf("ss = %+v", out)
	}
}

func TestSingboxHysteria2(t *testing.T) {
	node := ClashNode{NodeType: "hysteria2", Server: "h.example", Port: 8448, Password: "hp", Obfs: "salamander"}
	out := singleOutbound(t, node)
	if out["type"] != "hysteria2" || out["obfs"].(map[string]any)["type"] != "salamander" {
		t.Fatalf("hy2 = %+v", out)
	}
}

func TestSingboxUnsupported(t *testing.T) {
	if _, err := buildSingboxConfig(ClashNode{NodeType: "relay", Server: "x", Port: 1}, 1); err == nil {
		t.Fatal("relay should error")
	}
	if _, err := buildSingboxConfig(ClashNode{NodeType: "trojan", Server: "x", Port: 443}, 1); err == nil {
		t.Fatal("trojan no password should error")
	}
	if _, err := buildSingboxConfig(ClashNode{NodeType: "vless", Server: "x", Port: 443}, 1); err == nil {
		t.Fatal("vless no uuid should error")
	}
}

func TestPickFreeModel(t *testing.T) {
	got := pickFreeModel([]map[string]any{{"id": "pro-x"}, {"id": "deepseek-v4-flash"}})
	if got != "deepseek-v4-flash" {
		t.Fatalf("got %q", got)
	}
	got = pickFreeModel([]map[string]any{{"id": "a"}, {"id": "b-free"}})
	if got != "b-free" {
		t.Fatalf("prefer -free, got %q", got)
	}
	if !isFreeModelID("big-pickle") || !isFreeModelID("X-free") {
		t.Fatal("free detection failed")
	}
}

func TestProbeHelpers(t *testing.T) {
	if !probeCompletionSuccess(200, []byte(`{"choices":[{}]}`)) {
		t.Fatal("choices should pass")
	}
	if probeCompletionSuccess(200, []byte(`{"choices":[]}`)) {
		t.Fatal("empty choices fail")
	}
	if probeCompletionSuccess(503, []byte(`{"choices":[{}]}`)) {
		t.Fatal("503 fail")
	}
	if n, ok := modelsCount([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`)); !ok || n != 2 {
		t.Fatalf("modelsCount = %d %v", n, ok)
	}
}
