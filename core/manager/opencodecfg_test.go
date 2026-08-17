package manager

import (
	"encoding/json"
	"testing"
)

// TestUpstreamProxyAddr 覆盖 upstream_proxy 归一化：空值 / socks5:// 前缀 / http:// 前缀 /
// 非法（无端口）回退空串；Clash mixed-port 同时支持 socks5 与 http，统一存裸 host:port。
func TestUpstreamProxyAddr(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"  ":                          "",
		"socks5://127.0.0.1:7897":     "127.0.0.1:7897",
		"http://127.0.0.1:7897":       "127.0.0.1:7897",
		"127.0.0.1:7897":              "127.0.0.1:7897", // 裸 host:port 也接受
		"socks5://localhost:7897":     "localhost:7897",
		" socks5://127.0.0.1:7897   ": "127.0.0.1:7897", // 两侧空白应剥离
		// 保留合法用例：大写 scheme / IPv6（G24 不回归）。
		"SOCKS5://127.0.0.1:7897": "127.0.0.1:7897",
		"[::1]:7897":              "[::1]:7897",
		"socks5://[::1]:7897":     "[::1]:7897",
		// 非法：无端口 → 回退直连
		"socks5://127.0.0.1":           "",
		"127.0.0.1":                    "",
		"socks5://":                    "",
		"socks5://127.0.0.1:7897/path": "", // 带路径也属非法
		// G24 新增病态输入：// 前缀（归一化后剩 //host:port）、端口 0。
		"socks5:////127.0.0.1:5": "",
		"socks5://127.0.0.1:0":   "",
		"127.0.0.1:0":            "",
		"//127.0.0.1:5":          "",
	}
	for in, want := range cases {
		if got := upstreamProxyAddr(in); got != want {
			t.Fatalf("upstreamProxyAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildOpenCodeCfgUpstreamProxy 配置 upstream_proxy 后：active_socks5 指向代理
// （scheme 被剥离为裸 host:port），socks5_proxies 单元素；非法值回退现状。
func TestBuildOpenCodeCfgUpstreamProxy(t *testing.T) {
	const port = uint16(40001)

	t.Run("socks5 scheme", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.saveConfig(Config{UpstreamProxy: "socks5://127.0.0.1:7897"}); err != nil {
			t.Fatalf("saveConfig: %v", err)
		}
		cfg := mustBuildOpenCodeCfg(t, m, port)
		if got := cfg["active_socks5"]; got != "127.0.0.1:7897" {
			t.Fatalf("active_socks5 = %v, want 127.0.0.1:7897", got)
		}
		proxies := mustSocks5Proxies(t, cfg)
		if len(proxies) != 1 {
			t.Fatalf("socks5_proxies len = %d, want 1", len(proxies))
		}
		if addr := proxies[0]["addr"]; addr != "127.0.0.1:7897" {
			t.Fatalf("socks5_proxies[0].addr = %v, want 127.0.0.1:7897", addr)
		}
		if name := proxies[0]["name"]; name != "upstream" {
			t.Fatalf("socks5_proxies[0].name = %v, want upstream", name)
		}
	})

	t.Run("http scheme (Clash mixed-port)", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.saveConfig(Config{UpstreamProxy: "http://127.0.0.1:7897"}); err != nil {
			t.Fatalf("saveConfig: %v", err)
		}
		cfg := mustBuildOpenCodeCfg(t, m, port)
		if got := cfg["active_socks5"]; got != "127.0.0.1:7897" {
			t.Fatalf("active_socks5 = %v, want 127.0.0.1:7897", got)
		}
	})

	t.Run("invalid (no port) falls back to singbox", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.saveConfig(Config{UpstreamProxy: "socks5://127.0.0.1"}); err != nil {
			t.Fatalf("saveConfig: %v", err)
		}
		cfg := mustBuildOpenCodeCfg(t, m, port)
		if got := cfg["active_socks5"]; got != "127.0.0.1:40001" {
			t.Fatalf("active_socks5 = %v, want 127.0.0.1:40001 (回退 sing-box)", got)
		}
		proxies := mustSocks5Proxies(t, cfg)
		if len(proxies) != 1 || proxies[0]["name"] != "singbox" {
			t.Fatalf("socks5_proxies = %v, want 单元素 singbox", proxies)
		}
	})

	// 网关路由配置保持多出口轮询（实例各自经代理出口，网关不做单点代理）。
	t.Run("router config unaffected", func(t *testing.T) {
		m := newTestManager(t)
		if err := m.saveConfig(Config{UpstreamProxy: "socks5://127.0.0.1:7897"}); err != nil {
			t.Fatalf("saveConfig: %v", err)
		}
		gwCfg, err := m.buildRouterCfg([]uint16{40001, 40002}, map[uint16]string{40001: "a", 40002: "b"}, "smart")
		if err != nil {
			t.Fatalf("buildRouterCfg: %v", err)
		}
		var gw map[string]any
		if err := json.Unmarshal(gwCfg, &gw); err != nil {
			t.Fatalf("unmarshal gw cfg: %v", err)
		}
		if got := gw["active_socks5"]; got != "__round_robin__" {
			t.Fatalf("router active_socks5 = %v, want __round_robin__", got)
		}
		if proxies := mustSocks5Proxies(t, gw); len(proxies) != 2 {
			t.Fatalf("router socks5_proxies len = %d, want 2", len(proxies))
		}
	})
}

// TestBuildOpenCodeCfgUnconfiguredSnapshot 回归快照：未配置 upstream_proxy 时
// 生成的实例子进程配置与现状完全一致（E1 改造只影响配置了代理的路径）。
func TestBuildOpenCodeCfgUnconfiguredSnapshot(t *testing.T) {
	m := newTestManager(t)
	got, err := m.buildOpenCodeCfg(40001, false)
	if err != nil {
		t.Fatalf("buildOpenCodeCfg: %v", err)
	}
	if string(got) != goldenInstanceCfg {
		t.Fatalf("unconfigured instance cfg changed by E1:\n--- got ---\n%s\n--- want (现状) ---\n%s", got, goldenInstanceCfg)
	}
}

func mustBuildOpenCodeCfg(t *testing.T, m *Manager, port uint16) map[string]any {
	t.Helper()
	data, err := m.buildOpenCodeCfg(port, false)
	if err != nil {
		t.Fatalf("buildOpenCodeCfg: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	return cfg
}

func mustSocks5Proxies(t *testing.T, cfg map[string]any) []map[string]any {
	t.Helper()
	raw, ok := cfg["socks5_proxies"].([]any)
	if !ok {
		t.Fatalf("socks5_proxies missing: %v", cfg)
	}
	proxies := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		pm, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("socks5_proxies element not object: %v", p)
		}
		proxies = append(proxies, pm)
	}
	return proxies
}

// goldenInstanceCfg 未配置 upstream_proxy 时 buildOpenCodeCfg(40001) 的完整输出
// （json.MarshalIndent 键序固定：本文件写死为 E1 改造前的基线）。
const goldenInstanceCfg = `{
  "active_socks5": "127.0.0.1:40001",
  "force_disable_thinking": false,
  "model_alias": {
    "deepseek-v4-flash": "deepseek-v4-flash-free",
    "laguna-s-2.1": "laguna-s-2.1-free",
    "ling-3.0-flash": "ling-3.0-flash-free",
    "mimo-v2.5": "mimo-v2.5-free",
    "nemotron-3-ultra": "nemotron-3-ultra-free",
    "north-mini-code": "north-mini-code-free"
  },
  "reasoning_effort_map": {
    "high": "high",
    "medium": "medium",
    "minimal": "low"
  },
  "show_node_prefix": false,
  "socks5_proxies": [
    {
      "addr": "127.0.0.1:40001",
      "name": "singbox"
    }
  ]
}`
