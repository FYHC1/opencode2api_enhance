package manager

import (
	"strings"
	"testing"
)

// ---------- 订阅：sing-box JSON ----------

func TestParseSubscriptionSingboxJSON(t *testing.T) {
	body := `{
  "outbounds": [
    {"type":"vless","tag":"HK-01","server":"hk.example.com","server_port":443,"uuid":"u1",
     "tls":{"enabled":true,"server_name":"cdn.example.com","reality":{"public_key":"pbk1","short_id":"abcd"},
           "utls":{"fingerprint":"chrome"}},"flow":"xtls-rprx-vision"},
    {"type":"shadowsocks","tag":"SS1","server":"ss.example.com","server_port":8388,"method":"aes-256-gcm","password":"pw1"},
    {"type":"wireguard","tag":"WG-1","server":"wg.example.com","server_port":51820,"private_key":"priv1"}
  ]
}`
	nodes, err := parseSubscription(body)
	if err != nil {
		t.Fatalf("parseSubscription: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len = %d, want 3", len(nodes))
	}
	if nodes[0].Name != "HK-01" || nodes[0].NodeType != "vless" || nodes[0].RealityPbk != "pbk1" || nodes[0].RealitySid != "abcd" {
		t.Fatalf("vless = %+v", nodes[0])
	}
	if !nodes[0].TLS {
		t.Fatalf("vless reality should be TLS")
	}
	if nodes[1].Cipher != "aes-256-gcm" || nodes[1].Password != "pw1" {
		t.Fatalf("ss = %+v", nodes[1])
	}
	if nodes[2].NodeType != "wireguard" || nodes[2].Password != "priv1" {
		t.Fatalf("wg = %+v", nodes[2])
	}
}

// ---------- 新协议 URI ----------

func TestParseTuicURI(t *testing.T) {
	n, err := parseSubscription("tuic://uuid-1:pass@tuic.example.com:7788?sni=cdn.example.com#Tuic1")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].NodeType != "tuic" || n[0].UUID != "uuid-1" || n[0].Password != "pass" || n[0].SNI != "cdn.example.com" || !n[0].TLS {
		t.Fatalf("tuic = %+v", n[0])
	}
}

func TestParseWireguardURI(t *testing.T) {
	n, err := parseSubscription("wg://pubkey-abc@wg.example.com:51820?private_key=priv-xyz#WG-A")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].NodeType != "wireguard" || n[0].Cipher != "pubkey-abc" || n[0].Password != "priv-xyz" {
		t.Fatalf("wg = %+v", n[0])
	}
}

func TestParseSocksURI(t *testing.T) {
	n, err := parseSubscription("socks://user1:pass1@so.example.com:1080#S1")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].NodeType != "socks" || n[0].Cipher != "user1" || n[0].Password != "pass1" {
		t.Fatalf("socks = %+v", n[0])
	}
}

func TestParseHysteria1URI(t *testing.T) {
	n, err := parseSubscription("hysteria://hy.example.com:8443?auth=tokenA&peer=hy.example.com#Hy1")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].NodeType != "hysteria" || n[0].Password != "tokenA" || n[0].SNI != "hy.example.com" {
		t.Fatalf("hy1 = %+v", n[0])
	}
}

func TestParseAnyTLSURI(t *testing.T) {
	n, err := parseSubscription("anytls://pwd1@an.example.com:9999?insecure=1#A1")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].NodeType != "anytls" || n[0].Password != "pwd1" || !n[0].SkipCertVerify {
		t.Fatalf("anytls = %+v", n[0])
	}
}

// ---------- 容错 base64 / IPv6 / percent-decode ----------

func TestDecodeBase64LooseVariants(t *testing.T) {
	// 带空白 + URL-safe 变体 + 无 padding
	plain := "vless://uuid@a.example.com:443?security=tls#A"
	enc := base64URLNoPadOf(t, plain)
	decoded, ok := decodeBase64Loose(enc)
	if !ok || !strings.Contains(decoded, "vless://") {
		t.Fatalf("decode loose = %q ok=%v", decoded, ok)
	}
}

func TestSplitHostPortIPv6(t *testing.T) {
	host, port, err := splitHostPort("[2001:db8::1]:443")
	if err != nil || host != "2001:db8::1" || port != 443 {
		t.Fatalf("ipv6 = %s %d %v", host, port, err)
	}
	// IPv4 + query
	h2, p2, _ := splitHostPort("example.com:443?sni=x")
	if h2 != "example.com" || p2 != 443 {
		t.Fatalf("ipv4 = %s %d", h2, p2)
	}
}

func TestPercentDecodeName(t *testing.T) {
	if got := percentDecode("%E9%A6%99%E6%B8%AF"); got != "香港" {
		t.Fatalf("percent decode = %q", got)
	}
	// fragment 里的 URL 编码节点名
	n, err := parseSubscription("vless://uuid@a.example.com:443?security=tls#%E6%97%A5%E6%9C%AC")
	if err != nil {
		t.Fatal(err)
	}
	if n[0].Name != "日本" {
		t.Fatalf("fragment name = %q", n[0].Name)
	}
}

// ---------- 分组与合并 ----------

func TestGroupNameFor(t *testing.T) {
	m := New(t.TempDir())
	// URL 末段作分组名
	if g := m.groupNameFor("https://example.com/sub/speedtest.yaml", SubscriptionMeta{}); g != "speedtest" {
		t.Fatalf("url segment group = %q", g)
	}
	// 响应头订阅名优先于 URL 末段
	if g := m.groupNameFor("https://example.com/x", SubscriptionMeta{Name: "My Sub"}); g != "My Sub" {
		t.Fatalf("meta name group = %q", g)
	}
	// 内容配置名优先于响应头订阅名
	if g := m.groupNameFor("https://example.com/x", SubscriptionMeta{Name: "My Sub", Profile: "内容配置名"}); g != "内容配置名" {
		t.Fatalf("profile group = %q", g)
	}
}

func TestExtractProfileName(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"注释Profile", "# Profile: 我的机场\nproxies:\n- name: a\n  type: trojan", "我的机场"},
		{"##变体", "## Profile: 二级注释\nproxies:\n- name: a", "二级注释"},
		{"大小写", "# PROFILE: UPPER\nproxies:\n- name: a", "UPPER"},
		{"值带尾注", "# Profile: 名字 # 尾注\nproxies:\n- name: a", "名字"},
		{"顶层块", "profile:\n  name: 顶层配置名\nproxies:\n- name: a", "顶层配置名"},
		{"顶层块缩进", "profile:\n    name: 缩进配置名\nproxies:\n- name: a", "缩进配置名"},
		{"单行变体", "profile-name: 单行名\nproxies:\n- name: a", "单行名"},
		{"URL式变体", "profile_name=等号名\nproxies:\n- name: a", "等号名"},
		{"无配置名", "proxies:\n- name: a\n  type: trojan\n  server: 1.2.3.4", ""},
		{"注释但那不是Profile", "# subscribe: x\nproxies:\n- name: a", ""},
	}
	for _, c := range cases {
		if got := extractProfileName(c.body); got != c.want {
			t.Errorf("%s: extractProfileName = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSaveSubscriptionCacheGrouped(t *testing.T) {
	m := New(t.TempDir())
	// 订阅 A
	if err := m.saveSubscriptionCacheGrouped([]SubscribeNode{
		{Name: "A1", Server: "1.2.3.4", Port: 443, NodeType: "trojan", Group: "订阅A"},
	}); err != nil {
		t.Fatal(err)
	}
	// 订阅 B：合并保留 A
	if err := m.saveSubscriptionCacheGrouped([]SubscribeNode{
		{Name: "B1", Server: "5.6.7.8", Port: 443, NodeType: "vless", Group: "订阅B"},
	}); err != nil {
		t.Fatal(err)
	}
	cache := m.loadSubscriptionCache()
	if len(cache) != 2 {
		t.Fatalf("cache = %+v", cache)
	}
	// 重新导入订阅 A（同组替换，不重复）
	if err := m.saveSubscriptionCacheGrouped([]SubscribeNode{
		{Name: "A2", Server: "1.2.3.4", Port: 8443, NodeType: "trojan", Group: "订阅A"},
	}); err != nil {
		t.Fatal(err)
	}
	cache = m.loadSubscriptionCache()
	if len(cache) != 2 {
		t.Fatalf("after A reimport cache = %+v", cache)
	}
	// 且订阅 A 组的旧节点被替换（A1 不在，A2 在）
	names := map[string]bool{}
	for _, n := range cache {
		names[n.Name] = true
	}
	if names["A1"] || !names["A2"] || !names["B1"] {
		t.Fatalf("replace semantics broken: %+v", names)
	}
}

func base64URLNoPadOf(t *testing.T, s string) string {
	t.Helper()
	// 构造 URL-safe 无 padding 的 base64
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	bytes := []byte(s)
	var out strings.Builder
	for i := 0; i < len(bytes); i += 3 {
		var b [3]byte
		n := copy(b[:], bytes[i:])
		_ = n
		o1 := b[0] >> 2
		o2 := (b[0]&0x03)<<4 | b[1]>>4
		out.WriteByte(chars[o1])
		out.WriteByte(chars[o2])
		if i+1 < len(bytes) {
			o3 := (b[1]&0x0f)<<2 | b[2]>>6
			out.WriteByte(chars[o3])
		}
		if i+2 < len(bytes) {
			o4 := b[2] & 0x3f
			out.WriteByte(chars[o4])
		}
	}
	return out.String()
}
