// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

func loadConfig(path string) AppConfig {
	var cfg AppConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		slog.Warn("config parse failed", "error", err)
	}
	return cfg
}

func saveConfig(path string, cfg AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// rateLimitCooldownSec / rateLimitBackoffBaseMS / rateLimitBackoffCapMS 429 感知（S2）：
// 冷却秒 / 指数退避起点与上限毫秒（默认 30 / 1000 / 30000；<=0 回退默认）。
// 声明于 config.go：socks_perf.go 由 S3 并行维护，不做 S2 改动。
var (
	rateLimitCooldownSec    = 30
	rateLimitBackoffBaseMS  = 1000
	rateLimitBackoffCapMS   = 30000
)

func applyConfig(cfg AppConfig) {
	configMu.Lock()
	defer configMu.Unlock()
	if cfg.ModelAlias != nil {
		modelAlias = cfg.ModelAlias
	}
	if cfg.ReasoningEffortMap != nil {
		reasoningEffortMap = cfg.ReasoningEffortMap
	}
	forceDisableThinking = cfg.ForceDisableThinking
	if cfg.ShowNodePrefix != nil {
		showNodePrefix = *cfg.ShowNodePrefix
	}
	if cfg.Providers != nil {
		providersCfg = append([]ProviderCfg(nil), cfg.Providers...)
	}
	routingCfg = cfg.Routing

	if cfg.RouteMode == "round_robin" || cfg.RouteMode == "failover" || cfg.RouteMode == "smart" {
		routeMode = cfg.RouteMode
	}
	setTimeoutConfigFromApp(cfg)
	applyBadStatusConfig(cfg)

	// P2 性能模式：质量加权路由 + 熔断/半开（未设置保持当前值；关闭时路由行为与基线一致）。
	if cfg.PoolPerformanceMode != nil {
		poolPerfMode = *cfg.PoolPerformanceMode
	}
	if cfg.PoolBreakerThreshold > 0 {
		poolBreakerThreshold = cfg.PoolBreakerThreshold
	}
	if cfg.PoolHalfOpenIntervalSec > 0 {
		poolHalfOpenIntervalSec = cfg.PoolHalfOpenIntervalSec
	}
	// S3 链路类坏池自动恢复间隔（>0 才覆盖，未配置保持当前值/默认 300）。
	if cfg.BadPoolResetSec > 0 {
		badPoolResetSec = cfg.BadPoolResetSec
	}
	if cfg.PoolRaceCopies > 0 {
		poolRaceCopies = cfg.PoolRaceCopies
	}
	if cfg.RaceBudgetMS > 0 {
		raceBudgetMS = cfg.RaceBudgetMS
	}
	// S5 压力系数分段阈值（>0 才覆盖，未配置保持当前值/默认）。
	if cfg.PoolRacePressureLow > 0 {
		poolRacePressureLow = cfg.PoolRacePressureLow
	}
	if cfg.PoolRacePressureHigh > 0 {
		poolRacePressureHigh = cfg.PoolRacePressureHigh
	}
	// S2 429 感知（>0 才覆盖，未配置保持当前值/默认）。
	if cfg.RateLimitCooldownSec > 0 {
		rateLimitCooldownSec = cfg.RateLimitCooldownSec
	}
	if cfg.RateLimitBackoffBaseMS > 0 {
		rateLimitBackoffBaseMS = cfg.RateLimitBackoffBaseMS
	}
	if cfg.RateLimitBackoffCapMS > 0 {
		rateLimitBackoffCapMS = cfg.RateLimitBackoffCapMS
	}

	socks5Mu.Lock()
	proxiesChanged := false
	if cfg.Socks5Proxies != nil {
		proxiesChanged = !sameSocks5Proxies(socks5Proxies, cfg.Socks5Proxies)
		socks5Proxies = append([]Socks5Proxy(nil), cfg.Socks5Proxies...)
	}
	if activeSocks5 != cfg.ActiveSocks5 || proxiesChanged {
		activeSocks5 = cfg.ActiveSocks5
		socks5Client = nil
		socks5ClientAddr = ""
		atomic.StoreUint32(&socks5RRIndex, 0)
	}
	socks5Mu.Unlock()
	if proxiesChanged {
		socks5HealthMu.Lock()
		socks5Health = map[string]socks5HealthState{}
		socks5HealthMu.Unlock()
	}

}

func sameSocks5Proxies(a, b []Socks5Proxy) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func getSocks5ProxyCount() int {
	socks5Mu.RLock()
	defer socks5Mu.RUnlock()
	return len(socks5Proxies)
}

// maxRouteRetries 返回同模型路由重试上限：多代理时按代理数扩展，否则沿用上游重试上限。
func maxRouteRetries() int {
	proxyCount := getSocks5ProxyCount()
	if proxyCount > maxUpstreamRetries {
		return proxyCount
	}
	return maxUpstreamRetries
}

// startConfigWatcher applies config file changes without restarting the
// process, because restarting a live HTTP server drops active SSE streams.
func startConfigWatcher(path string) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		lastData, _ := os.ReadFile(path)
		for range ticker.C {
			data, err := os.ReadFile(path)
			if err != nil || bytes.Equal(data, lastData) {
				continue
			}
			var cfg AppConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				slog.Warn("config reload skipped", "path", path, "error", err)
				continue
			}
			applyConfig(cfg)
			lastData = append(lastData[:0], data...)
			slog.Info("config hot-reloaded", "path", path)
		}
	}()
}
