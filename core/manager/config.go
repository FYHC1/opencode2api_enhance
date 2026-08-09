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
	}
}

// maskSecret 非空秘密展示为 ***。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return strings.Repeat("*", len(s))
}
