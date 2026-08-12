// 实例/网关 opencode2api.json 配置生成（Rust opencode_cfg.rs 移植）。
package manager

import (
	"encoding/json"
	"fmt"
	"sort"
)

// defaultFreeAliases 免费模型别名（-free 后缀隐藏）。
var defaultFreeAliases = []struct{ from, to string }{
	{"deepseek-v4-flash", "deepseek-v4-flash-free"},
	{"mimo-v2.5", "mimo-v2.5-free"},
	{"ling-3.0-flash", "ling-3.0-flash-free"},
	{"nemotron-3-ultra", "nemotron-3-ultra-free"},
	{"north-mini-code", "north-mini-code-free"},
	{"laguna-s-2.1", "laguna-s-2.1-free"},
}

func aliasMap() map[string]string {
	m := map[string]string{}
	for _, a := range defaultFreeAliases {
		m[a.from] = a.to
	}
	return m
}

func reasoningMap() map[string]string {
	return map[string]string{"minimal": "low", "medium": "medium", "high": "high"}
}

// buildOpenCodeCfg 单实例配置：本地 sing-box 作为唯一 SOCKS5 出口。
func (m *Manager) buildOpenCodeCfg(singboxPort uint16) ([]byte, error) {
	appCfg := m.loadConfig()
	cfg := map[string]any{
		"model_alias":            aliasMap(),
		"reasoning_effort_map":   reasoningMap(),
		"force_disable_thinking": false,
		"socks5_proxies": []any{map[string]any{
			"name": "singbox", "addr": fmt.Sprintf("127.0.0.1:%d", singboxPort),
		}},
		"active_socks5":    fmt.Sprintf("127.0.0.1:%d", singboxPort),
		"show_node_prefix": appCfg.ShowNodePrefix,
	}
	// 透传厂商注册表 + 路由：实例子进程与核心一致，能注册多厂商（如 windsurf）
	cfg = injectVendorConfig(cfg, appCfg)
	return json.MarshalIndent(cfg, "", "  ")
}

// BuildOpenCodeCfgFor 导出包装（main 装配实例接缝用）。
func (m *Manager) BuildOpenCodeCfgFor(port uint16) ([]byte, error) { return m.buildOpenCodeCfg(port) }

// buildRouterCfg 统一网关路由配置：多 sing-box 出口 + route_mode + 超时区间。
func (m *Manager) buildRouterCfg(singboxPorts []uint16, portNames map[uint16]string, routeMode string) ([]byte, error) {
	appCfg := m.loadConfig()
	proxies := []any{}
	sorted := append([]uint16(nil), singboxPorts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, p := range sorted {
		name := portNames[p]
		if name == "" {
			name = fmt.Sprintf("instance-%d", p)
		}
		proxies = append(proxies, map[string]any{
			"name": name, "addr": fmt.Sprintf("127.0.0.1:%d", p),
		})
	}
	if routeMode == "" {
		routeMode = "round_robin"
	}
	cfg := map[string]any{
		"model_alias":            aliasMap(),
		"reasoning_effort_map":   reasoningMap(),
		"force_disable_thinking": false,
		"socks5_proxies":         proxies,
		"active_socks5":          "__round_robin__",
		"route_mode":             routeMode,
		"show_node_prefix":       appCfg.ShowNodePrefix,
	}
	// 超时/日志区间（0 = 用 core 默认）
	applyIf := func(key string, v int64) {
		if v > 0 {
			cfg[key] = v
		}
	}
	applyIf("timeout_ttft_min_ms", appCfg.TimeoutTTFTMinMS)
	applyIf("timeout_ttft_max_ms", appCfg.TimeoutTTFTMaxMS)
	applyIf("timeout_silence_min_ms", appCfg.TimeoutSilenceMinMS)
	applyIf("timeout_silence_max_ms", appCfg.TimeoutSilenceMaxMS)
	applyIf("failover_probe_min", appCfg.FailoverProbeMin)
	applyIf("failover_probe_max", appCfg.FailoverProbeMax)
	applyIf("call_log_max", appCfg.CallLogMax)
	// P2 性能模式：熔断阈值 / 半开间隔 / 开关透传（>0 才写，未配置子进程用默认）。
	applyIf("pool_breaker_threshold", int64(appCfg.PoolBreakerThreshold))
	applyIf("pool_halfopen_interval_sec", int64(appCfg.PoolHalfOpenIntervalSec))
	if appCfg.PoolPerformanceMode != nil {
		cfg["pool_performance_mode"] = *appCfg.PoolPerformanceMode
	}
	// 透传厂商注册表 + 路由：网关子进程与核心一致，能注册多厂商（如 windsurf）
	cfg = injectVendorConfig(cfg, appCfg)
	return json.MarshalIndent(cfg, "", "  ")
}

// injectVendorConfig 把管理器配置里的厂商注册表与路由写入子进程配置。
// 子进程（实例子进程 / 网关子进程）与核心同二进制，读同一格式的 opencode2api.json，
// 不带 providers 则只认 opencode —— 多厂商（windsurf 等）在 exe 实例/网关形态下不可用。
func injectVendorConfig(cfg map[string]any, appCfg Config) map[string]any {
	if len(appCfg.Providers) > 0 {
		cfg["providers"] = appCfg.Providers
	}
	if len(appCfg.Routing) > 0 {
		cfg["routing"] = appCfg.Routing
	}
	return cfg
}
