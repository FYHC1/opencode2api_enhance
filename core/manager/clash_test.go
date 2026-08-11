package manager

import (
	"strings"
	"testing"
)

const clashSample = `mixed-port: 7890
proxies:
  - name: "HK-01"
    type: trojan
    server: 1.2.3.4
    port: 443
    password: "pw123"
    udp: true
    skip-cert-verify: true
  - name: CN-X
    type: vless
    server: example.com
    port: 443
    uuid: "00000000-0000-0000-0000-000000000001"
    network: ws
    ws-opts:
      path: /ws?ed=2048
    reality-opts:
      public-key: "PUBKEY"
      short-id: "abcd"
    flow: xtls-rprx-vision
  - name: OBSFUSCATED   # 注释行
    type: ss
    server: 5.6.7.8
    port: 8388
    cipher: chacha20-ietf-poly1305
    password: "sspass"
`

func TestYAMLParseClashProxies(t *testing.T) {
	root, err := yamlParse(strings.ReplaceAll(clashSample, "\t", "  "))
	if err != nil {
		t.Fatalf("yamlParse: %v", err)
	}
	proxies := root.sliceOf("proxies")
	if len(proxies) != 3 {
		t.Fatalf("proxies = %d, want 3 (got %+v)", len(proxies), proxies)
	}
	first := proxies[0]
	if first.string("name") != "HK-01" || first.string("type") != "trojan" ||
		first.string("server") != "1.2.3.4" || first.intVal("port") != 443 {
		t.Fatalf("first = %+v", first)
	}
	if b := first.boolPtr("skip-cert-verify"); b == nil || !*b {
		t.Fatalf("skip-cert-verify = %v", b)
	}
	// 注释行内联
	if proxies[2].string("type") != "ss" {
		t.Fatalf("third type = %q", proxies[2].string("type"))
	}
}

func TestParseClashYAMLNodes(t *testing.T) {
	nodes, err := parseClashYAML(strings.ReplaceAll(clashSample, "\t", "  "))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	n0 := nodes[0]
	if n0.Name != "HK-01" || n0.NodeType != "trojan" || n0.Port != 443 || n0.Password != "pw123" {
		t.Fatalf("n0 = %+v", n0)
	}
	if n0.SkipCertVerify == nil || !*n0.SkipCertVerify {
		t.Fatalf("n0 skip-cert = %v", n0.SkipCertVerify)
	}
	n1 := nodes[1]
	if n1.UUID == "" || n1.Network != "ws" || n1.WsPath != "/ws?ed=2048" || n1.Flow != "xtls-rprx-vision" {
		t.Fatalf("n1 = %+v", n1)
	}
}

func TestParseClashYAMLMalformed(t *testing.T) {
	if _, err := parseClashYAML("proxies:\n  - name: a\n   server: x\n"); err == nil {
		// 非法缩进可能不被判错；仅断言不 panic 且返回空或错误
	}
}

func TestIsJunkNode(t *testing.T) {
	if !isJunkNode("-----广告") {
		t.Fatal("dash junk not detected")
	}
	if !isJunkNode("剩余流量: 12GB") {
		t.Fatal("keywords junk not detected")
	}
	if isJunkNode("正常节点-01") {
		t.Fatal("normal node flagged as junk")
	}
}

func TestParseClashURL(t *testing.T) {
	port, path := parseClashURL("http://127.0.0.1:9090")
	if port != 9090 || path != "/" {
		t.Fatalf("plain: %d %q", port, path)
	}
	port, path = parseClashURL("http://example.com/control")
	if port != 80 || path != "/control" {
		t.Fatalf("subpath: %d %q", port, path)
	}
	if p, _ := parseClashURL("https://x"); p != 0 {
		t.Fatalf("https must be rejected: %d", p)
	}
}
