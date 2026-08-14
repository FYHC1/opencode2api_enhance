package manager

import (
	"encoding/base64"
	"testing"
)

// ws Host 头保留测试：Clash YAML / vless:// 订阅节点解析出的 WSHeaders
// 必须写入生成的 sing-box 配置（CDN 前置节点缺 Host 头会被 Cloudflare 403 拒绝）。

func TestNodeFromYAMLWsHeadersFlow(t *testing.T) {
	nodes, err := parseClashYAML(`proxies:
  - {name: CDN-01, server: 1.2.3.4, port: 443, type: vless, uuid: "u1", network: ws, ws-opts: {path: /, headers: {Host: cdn.example.com}}}
`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parseClashYAML: %v nodes=%d", err, len(nodes))
	}
	n := nodes[0]
	if n.WSHeaders == nil || n.WSHeaders["Host"] != "cdn.example.com" {
		t.Fatalf("WSHeaders = %+v, want Host=cdn.example.com", n.WSHeaders)
	}
	if n.WsPath != "/" {
		t.Fatalf("WsPath = %q", n.WsPath)
	}
}

func TestNodeFromYAMLWsHeadersBlock(t *testing.T) {
	nodes, err := parseClashYAML(`proxies:
  - name: CDN-02
    server: 5.6.7.8
    port: 443
    type: vless
    uuid: "u2"
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: cdn2.example.com
`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parseClashYAML: %v nodes=%d", err, len(nodes))
	}
	n := nodes[0]
	if n.WSHeaders == nil || n.WSHeaders["Host"] != "cdn2.example.com" {
		t.Fatalf("WSHeaders = %+v, want Host=cdn2.example.com", n.WSHeaders)
	}
	if n.WsPath != "/ws" {
		t.Fatalf("WsPath = %q", n.WsPath)
	}
}

func TestNodeFromYAMLFlatWsHeaders(t *testing.T) {
	nodes, err := parseClashYAML(`proxies:
  - {name: CDN-03, server: 9.9.9.9, port: 443, type: vless, uuid: "u3", network: ws, ws-headers: {Host: cdn3.example.com}}
`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parseClashYAML: %v nodes=%d", err, len(nodes))
	}
	if n := nodes[0]; n.WSHeaders == nil || n.WSHeaders["Host"] != "cdn3.example.com" {
		t.Fatalf("WSHeaders = %+v, want Host=cdn3.example.com", n.WSHeaders)
	}
}

func TestSingboxVlessWsHeaders(t *testing.T) {
	node := ClashNode{NodeType: "vless", Server: "1.2.3.4", Port: 443, UUID: "u1", Network: "ws",
		WsPath: "/", WSHeaders: map[string]string{"Host": "cdn.example.com"}}
	out := singleOutbound(t, node)
	tr := out["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/" {
		t.Fatalf("transport = %+v", tr)
	}
	hdrs, ok := tr["headers"].(map[string]any)
	if !ok || hdrs["Host"] != "cdn.example.com" {
		t.Fatalf("headers = %+v, want Host=cdn.example.com", tr["headers"])
	}
}

func TestParseVmessHostHeader(t *testing.T) {
	// vmess://base64(json) 的 host 字段是 ws/http 传输的 Host 头，必须带出。
	json := `{"v":"2","ps":"CDN-VMESS","add":"1.2.3.4","port":443,"id":"u4","net":"ws","host":"cdn-v.example.com","path":"/","tls":"tls"}`
	n, err := parseVmess(base64.StdEncoding.EncodeToString([]byte(json)))
	if err != nil {
		t.Fatalf("parseVmess: %v", err)
	}
	if n.WsHeaders == nil || n.WsHeaders["Host"] != "cdn-v.example.com" {
		t.Fatalf("WsHeaders = %+v, want Host=cdn-v.example.com", n.WsHeaders)
	}
	cn := toClashNode(n)
	if cn.WSHeaders == nil || cn.WSHeaders["Host"] != "cdn-v.example.com" {
		t.Fatalf("toClashNode WSHeaders = %+v", cn.WSHeaders)
	}
}

func TestParseTrojanHostHeader(t *testing.T) {
	n, err := parseTrojan("pw@1.2.3.4:443?type=ws&host=cdn-t.example.com&path=%2F#CDN-T")
	if err != nil {
		t.Fatalf("parseTrojan: %v", err)
	}
	if n.WsHeaders == nil || n.WsHeaders["Host"] != "cdn-t.example.com" {
		t.Fatalf("WsHeaders = %+v, want Host=cdn-t.example.com", n.WsHeaders)
	}
	// host 参数不应再被当作 path 兜底
	if n.WsPath == "cdn-t.example.com" {
		t.Fatalf("host 被错误当作 path: %q", n.WsPath)
	}
}

func TestParseSingboxJSONWsHeaders(t *testing.T) {
	body := `{"outbounds":[{"type":"vless","tag":"CDN-JSON","server":"1.2.3.4","server_port":443,"uuid":"u5","tls":{"enabled":true,"server_name":"cdn.example.com"},"transport":{"type":"ws","path":"/","headers":{"Host":"cdn-j.example.com"}}}]}`
	nodes, err := parseSingboxJSON(body)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parseSingboxJSON: %v nodes=%d", err, len(nodes))
	}
	if n := nodes[0]; n.WsHeaders == nil || n.WsHeaders["Host"] != "cdn-j.example.com" {
		t.Fatalf("WsHeaders = %+v, want Host=cdn-j.example.com", n.WsHeaders)
	}
}
