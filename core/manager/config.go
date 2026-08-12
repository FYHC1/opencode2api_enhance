// 应用级配置（Rust config.rs 移植）：dataDir/config.json。
// 与实例级 gateway 配置（runtime/<name>/opencode2api.json，由 opencodecfg 生成）不同。
package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPassword 是未配置密码时的生效默认值。
const DefaultPassword = "123456"

// Config 应用级配置（字段与 Rust Config 一一对应，JSON 名一致）。
type Config struct {
	BaseURL             string `json:"base_url"`
	DefaultPassword     string `json:"default_password"`
	ClashExternalURL    string `json:"clash_external_url"`
	ClashAuthToken      string `json:"clash_auth_token"`
	TimeoutTTFTMinMS    int64  `json:"timeout_ttft_min_ms,omitempty"`
	TimeoutTTFTMaxMS    int64  `json:"timeout_ttft_max_ms,omitempty"`
	TimeoutSilenceMinMS int64  `json:"timeout_silence_min_ms,omitempty"`
	TimeoutSilenceMaxMS int64  `json:"timeout_silence_max_ms,omitempty"`
	FailoverProbeMin    int64  `json:"failover_probe_min,omitempty"`
	FailoverProbeMax    int64  `json:"failover_probe_max,omitempty"`
	CallLogMax          int64  `json:"call_log_max,omitempty"`
	ShowNodePrefix      bool   `json:"show_node_prefix,omitempty"`

	// SubscribeURL 订阅地址；SubscribeIntervalMin 自动拉取间隔（分钟，<=0 不自动拉）。
	SubscribeURL        string `json:"subscribe_url,omitempty"`
	SubscribeIntervalMin int    `json:"subscribe_interval_min,omitempty"`

	// 健康巡检：检查间隔（秒，<=0 不巡检）与自动重启连续失败阈值（<=0 不重启）。
	HealthCheckIntervalSec  int `json:"health_check_interval_sec,omitempty"`
	HealthRestartThreshold int `json:"health_restart_threshold,omitempty"`

	// 实例池链路探活（性能模式 P1）：间隔（秒）、单次超时（秒）、质量窗口（分钟），
	// <=0 时用默认值（45s / 3s / 10min）；PoolProbeEnabled 未设置（nil）默认开启。
	PoolProbeIntervalSec int   `json:"pool_probe_interval_sec,omitempty"`
	PoolProbeTimeoutSec  int   `json:"pool_probe_timeout_sec,omitempty"`
	PoolQualityWindowMin int   `json:"pool_quality_window_min,omitempty"`
	PoolProbeEnabled     *bool `json:"pool_probe_enabled,omitempty"`

	// GatewayKey 统一网关鉴权密钥（空 = 回退默认 sk-unified-local；main 功能 M6）。
	GatewayKey string `json:"gateway_key,omitempty"`

	// 端口配置（0 = 未设置，按 env > config > 编译默认 三源读取）：
	// 供 headless/Web 直跑与自定义部署使用；桌面壳经环境变量按槽位表注入（优先于 config）。
	GatewayPort      uint16 `json:"gateway_port,omitempty"`
	InstanceBasePort uint16 `json:"instance_base_port,omitempty"`
	ProbeAPIPort     uint16 `json:"probe_api_port,omitempty"`
	ProbeSocksPort   uint16 `json:"probe_socks_port,omitempty"`

	// Providers 厂商注册表（透传主程序 AppConfig 格式）：实例子进程/网关子进程
	// 生成的 opencode2api.json 需要带上，才能像核心一样注册多厂商（如 windsurf）。
	Providers []map[string]any `json:"providers,omitempty"`
	// Routing 模型→厂商路由（透传，供子进程按模型路由到正确厂商）。
	Routing map[string]any `json:"routing,omitempty"`
}

// configPath 返回配置文件路径。
func (m *Manager) configPath() string {
	return filepath.Join(m.paths.DataDir, "config.json")
}

// loadConfig 读取应用配置；文件缺失/损坏回退默认值。
func (m *Manager) loadConfig() Config {
	cfg := Config{}
	data, err := os.ReadFile(m.configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

// saveConfig 写回应用配置。
func (m *Manager) saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.configPath(), data, 0o644)
}

// effectiveDefaultPassword 生效默认密码：未设置 → "123456"。
func (m *Manager) effectiveDefaultPassword() string {
	pw := m.loadConfig().DefaultPassword
	if pw == "" {
		return DefaultPassword
	}
	return pw
}

// ConfigGet 返回配置键值（字符串形态，与 Rust get 一致；未知键报错）。
func (m *Manager) ConfigGet(key string) (string, error) {
	cfg := m.loadConfig()
	switch key {
	case "base_url":
		return cfg.BaseURL, nil
	case "default_password":
		return cfg.DefaultPassword, nil
	case "clash_external_url":
		return cfg.ClashExternalURL, nil
	case "clash_auth_token":
		return cfg.ClashAuthToken, nil
	case "timeout_ttft_min_ms":
		return strconv.FormatInt(cfg.TimeoutTTFTMinMS, 10), nil
	case "timeout_ttft_max_ms":
		return strconv.FormatInt(cfg.TimeoutTTFTMaxMS, 10), nil
	case "timeout_silence_min_ms":
		return strconv.FormatInt(cfg.TimeoutSilenceMinMS, 10), nil
	case "timeout_silence_max_ms":
		return strconv.FormatInt(cfg.TimeoutSilenceMaxMS, 10), nil
	case "failover_probe_min":
		return strconv.FormatInt(cfg.FailoverProbeMin, 10), nil
	case "failover_probe_max":
		return strconv.FormatInt(cfg.FailoverProbeMax, 10), nil
	case "call_log_max":
		return strconv.FormatInt(cfg.CallLogMax, 10), nil
	case "show_node_prefix":
		return strconv.FormatBool(cfg.ShowNodePrefix), nil
	case "subscribe_url":
		return cfg.SubscribeURL, nil
	case "subscribe_interval_min":
		return strconv.Itoa(cfg.SubscribeIntervalMin), nil
	case "health_check_interval_sec":
		return strconv.Itoa(cfg.HealthCheckIntervalSec), nil
	case "health_restart_threshold":
		return strconv.Itoa(cfg.HealthRestartThreshold), nil
	case "pool_probe_interval_sec":
		return strconv.Itoa(cfg.PoolProbeIntervalSec), nil
	case "pool_probe_timeout_sec":
		return strconv.Itoa(cfg.PoolProbeTimeoutSec), nil
	case "pool_quality_window_min":
		return strconv.Itoa(cfg.PoolQualityWindowMin), nil
	case "pool_probe_enabled":
		return strconv.FormatBool(poolProbeEnabled(cfg)), nil
	case "gateway_key":
		// 密钥不回显：设置过返回掩码，未设置返回空（main 语义一致）。
		if cfg.GatewayKey == "" {
			return "", nil
		}
		return "******", nil
	case "gateway_port":
		return strconv.FormatUint(uint64(cfg.GatewayPort), 10), nil
	case "instance_base_port":
		return strconv.FormatUint(uint64(cfg.InstanceBasePort), 10), nil
	case "probe_api_port":
		return strconv.FormatUint(uint64(cfg.ProbeAPIPort), 10), nil
	case "probe_socks_port":
		return strconv.FormatUint(uint64(cfg.ProbeSocksPort), 10), nil
	default:
		return "", fmt.Errorf("Unknown config key: %s", key)
	}
}

// ConfigSet 设置配置键（int/bool 自动解析；未知键报错）并落盘。
func (m *Manager) ConfigSet(key, value string) error {
	cfg := m.loadConfig()
	parseInt := func() (int64, error) {
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid integer for %s: %s", key, value)
		}
		return v, nil
	}
	// parsePort 解析端口（空/"0"=未设置=0；合法 1-65535）。
	parsePort := func() (uint16, error) {
		if value == "" || value == "0" {
			return 0, nil
		}
		v, err := strconv.ParseUint(value, 10, 16)
		if err != nil || v == 0 || v > 65535 {
			return 0, fmt.Errorf("invalid port for %s: %s", key, value)
		}
		return uint16(v), nil
	}
	switch key {
	case "base_url":
		cfg.BaseURL = value
	case "default_password":
		cfg.DefaultPassword = value
	case "clash_external_url":
		cfg.ClashExternalURL = value
	case "clash_auth_token":
		cfg.ClashAuthToken = value
	case "timeout_ttft_min_ms":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.TimeoutTTFTMinMS = v
	case "timeout_ttft_max_ms":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.TimeoutTTFTMaxMS = v
	case "timeout_silence_min_ms":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.TimeoutSilenceMinMS = v
	case "timeout_silence_max_ms":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.TimeoutSilenceMaxMS = v
	case "failover_probe_min":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.FailoverProbeMin = v
	case "failover_probe_max":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.FailoverProbeMax = v
	case "call_log_max":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.CallLogMax = v
	case "show_node_prefix":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean for show_node_prefix: %s", value)
		}
		cfg.ShowNodePrefix = b
	case "subscribe_url":
		cfg.SubscribeURL = strings.TrimSpace(value)
	case "subscribe_interval_min":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.SubscribeIntervalMin = int(v)
	case "health_check_interval_sec":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.HealthCheckIntervalSec = int(v)
	case "health_restart_threshold":
		v, err := parseInt()
		if err != nil {
			return err
		}
		cfg.HealthRestartThreshold = int(v)
	case "pool_probe_interval_sec":
		v, err := parseInt()
		if err != nil {
			return err
		}
		if v < 0 {
			return errors.New("pool_probe_interval_sec 需 >= 0")
		}
		cfg.PoolProbeIntervalSec = int(v)
	case "pool_probe_timeout_sec":
		v, err := parseInt()
		if err != nil {
			return err
		}
		if v < 0 {
			return errors.New("pool_probe_timeout_sec 需 >= 0")
		}
		cfg.PoolProbeTimeoutSec = int(v)
	case "pool_quality_window_min":
		v, err := parseInt()
		if err != nil {
			return err
		}
		if v < 0 {
			return errors.New("pool_quality_window_min 需 >= 0")
		}
		cfg.PoolQualityWindowMin = int(v)
	case "pool_probe_enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean for pool_probe_enabled: %s", value)
		}
		cfg.PoolProbeEnabled = &b
	case "gateway_key":
		// 空串 = 重置为默认（gatewayKey() 回退 sk-unified-local）；非空需至少 8 字符（main 校验一致）。
		if value == "" {
			cfg.GatewayKey = ""
		} else if len(value) < 8 {
			return errors.New("网关密钥至少 8 个字符")
		} else {
			cfg.GatewayKey = value
		}
	case "gateway_port":
		port, err := parsePort()
		if err != nil {
			return err
		}
		cfg.GatewayPort = port
	case "instance_base_port":
		port, err := parsePort()
		if err != nil {
			return err
		}
		cfg.InstanceBasePort = port
	case "probe_api_port":
		port, err := parsePort()
		if err != nil {
			return err
		}
		cfg.ProbeAPIPort = port
	case "probe_socks_port":
		port, err := parsePort()
		if err != nil {
			return err
		}
		cfg.ProbeSocksPort = port
	default:
		return errors.New("Unknown config key: " + key)
	}
	return m.saveConfig(cfg)
}

// ConfigView 是前端 /api/admin/config 的响应形态（密码脱敏）。
type ConfigView struct {
	BaseURL             string `json:"base_url"`
	DefaultPassword     string `json:"default_password"`
	HasPassword         bool   `json:"has_password"`
	ClashExternalURL    string `json:"clash_external_url"`
	HasClashToken       bool   `json:"has_clash_token"`
	TimeoutTTFTMinMS    int64  `json:"timeout_ttft_min_ms"`
	TimeoutTTFTMaxMS    int64  `json:"timeout_ttft_max_ms"`
	TimeoutSilenceMinMS int64  `json:"timeout_silence_min_ms"`
	TimeoutSilenceMaxMS int64  `json:"timeout_silence_max_ms"`
	FailoverProbeMin    int64  `json:"failover_probe_min"`
	FailoverProbeMax    int64  `json:"failover_probe_max"`
	CallLogMax          int64  `json:"call_log_max"`
	ShowNodePrefix      bool   `json:"show_node_prefix"`
	SubscribeURL         string `json:"subscribe_url"`
	SubscribeIntervalMin int    `json:"subscribe_interval_min"`
	HealthCheckIntervalSec  int `json:"health_check_interval_sec"`
	HealthRestartThreshold int `json:"health_restart_threshold"`
	PoolProbeIntervalSec   int  `json:"pool_probe_interval_sec"`
	PoolProbeTimeoutSec    int  `json:"pool_probe_timeout_sec"`
	PoolQualityWindowMin   int  `json:"pool_quality_window_min"`
	PoolProbeEnabled       bool `json:"pool_probe_enabled"`
	HasGatewayKey           bool `json:"has_gateway_key"`
	GatewayKey              string `json:"gateway_key"`
}

// ConfigViewOf 生成前端视图（密码与 clash token 脱敏为掩码）。
// 默认值与 Rust config_get 一致：未设置时返回默认超时区间/探测数/日志上限，
// 避免前端拿到 0 导致输入框空白、校验拦截（min ≤ 0 非法）而"按钮不可用"。
func (m *Manager) ConfigViewOf() ConfigView {
	cfg := m.loadConfig()
	def := func(v, d int64) int64 {
		if v <= 0 {
			return d
		}
		return v
	}
	return ConfigView{
		BaseURL:             cfg.BaseURL,
		DefaultPassword:     maskSecret(cfg.DefaultPassword),
		HasPassword:         cfg.DefaultPassword != "",
		ClashExternalURL:    cfg.ClashExternalURL,
		HasClashToken:       cfg.ClashAuthToken != "",
		TimeoutTTFTMinMS:    def(cfg.TimeoutTTFTMinMS, 10000),
		TimeoutTTFTMaxMS:    def(cfg.TimeoutTTFTMaxMS, 10000),
		TimeoutSilenceMinMS: def(cfg.TimeoutSilenceMinMS, 5000),
		TimeoutSilenceMaxMS: def(cfg.TimeoutSilenceMaxMS, 5000),
		FailoverProbeMin:    def(cfg.FailoverProbeMin, 2),
		FailoverProbeMax:    def(cfg.FailoverProbeMax, 3),
		CallLogMax:          def(cfg.CallLogMax, 5000),
		ShowNodePrefix:      cfg.ShowNodePrefix,
		SubscribeURL:        cfg.SubscribeURL,
		SubscribeIntervalMin: cfg.SubscribeIntervalMin,
		HealthCheckIntervalSec:  cfg.HealthCheckIntervalSec,
		HealthRestartThreshold: cfg.HealthRestartThreshold,
		PoolProbeIntervalSec:   poolProbeInterval(cfg),
		PoolProbeTimeoutSec:    int(poolProbeTimeout(cfg).Seconds()),
		PoolQualityWindowMin:   int(poolQualityWindowSec(cfg) / 60),
		PoolProbeEnabled:       poolProbeEnabled(cfg),
		HasGatewayKey:           cfg.GatewayKey != "",
		GatewayKey:              maskSecret(cfg.GatewayKey),
	}
}

// effectiveGatewayKey 生效的统一网关密钥：配置未设置/为空时回退默认 "sk-unified-local"。
func effectiveGatewayKey(cfg Config) string {
	if cfg.GatewayKey != "" {
		return cfg.GatewayKey
	}
	return unifiedGatewayKey
}

// maskSecret 非空秘密展示为 ***。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return strings.Repeat("*", len(s))
}
