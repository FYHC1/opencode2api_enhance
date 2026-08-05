use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct Config {
    pub base_url: String,
    pub default_password: String,
    pub clash_external_url: String,
    pub clash_auth_token: String,
    /// 流内超时切换区间（毫秒；0 = 用默认值）
    pub timeout_ttft_min_ms: Option<i64>,
    pub timeout_ttft_max_ms: Option<i64>,
    pub timeout_silence_min_ms: Option<i64>,
    pub timeout_silence_max_ms: Option<i64>,
    pub failover_probe_min: Option<i64>,
    pub failover_probe_max: Option<i64>,
    /// 调用日志保留上限
    pub call_log_max: Option<i64>,
}

impl Config {
    pub fn config_dir() -> PathBuf {
        let dir = dirs::config_dir()
            .unwrap_or_else(|| PathBuf::from("."))
            .join("opencode2api-manager");
        fs::create_dir_all(&dir).ok();
        dir
    }

    pub fn config_path() -> PathBuf {
        Self::config_dir().join("config.json")
    }

    pub fn load() -> Result<Self> {
        let path = Self::config_path();
        if path.exists() {
            let data = fs::read_to_string(&path)
                .context("Failed to read config file")?;
            let config: Config = serde_json::from_str(&data)
                .context("Failed to parse config file")?;
            Ok(config)
        } else {
            Ok(Config::default())
        }
    }

    /// 生效的默认密码：config 未设置时回退 "123456"。
    /// 实例未单独设置密码时，实例 API 门禁与探测均使用该值。
    pub fn effective_default_password() -> String {
        let password = Self::load()
            .unwrap_or_default()
            .default_password;
        if password.is_empty() {
            "123456".to_string()
        } else {
            password
        }
    }

    pub fn save(&self) -> Result<()> {
        let path = Self::config_path();
        let data = serde_json::to_string_pretty(self)
            .context("Failed to serialize config")?;
        fs::write(&path, data)
            .context("Failed to write config file")?;
        Ok(())
    }

    pub fn get(&self, key: &str) -> Option<String> {
        match key {
            "base_url" => Some(self.base_url.clone()),
            "default_password" => Some(self.default_password.clone()),
            "clash_external_url" => Some(self.clash_external_url.clone()),
            "clash_auth_token" => Some(self.clash_auth_token.clone()),
            "timeout_ttft_min_ms" => Some(self.timeout_ttft_min_ms.unwrap_or(0).to_string()),
            "timeout_ttft_max_ms" => Some(self.timeout_ttft_max_ms.unwrap_or(0).to_string()),
            "timeout_silence_min_ms" => Some(self.timeout_silence_min_ms.unwrap_or(0).to_string()),
            "timeout_silence_max_ms" => Some(self.timeout_silence_max_ms.unwrap_or(0).to_string()),
            "failover_probe_min" => Some(self.failover_probe_min.unwrap_or(0).to_string()),
            "failover_probe_max" => Some(self.failover_probe_max.unwrap_or(0).to_string()),
            "call_log_max" => Some(self.call_log_max.unwrap_or(0).to_string()),
            _ => None,
        }
    }

    pub fn set(&mut self, key: &str, value: &str) -> Result<()> {
        let parse_i64 = |s: &str| -> Result<i64> {
            s.parse::<i64>().with_context(|| format!("invalid integer for {key}: {s}"))
        };
        match key {
            "base_url" => {
                self.base_url = value.to_string();
            }
            "default_password" => {
                self.default_password = value.to_string();
            }
            "clash_external_url" => {
                self.clash_external_url = value.to_string();
            }
            "clash_auth_token" => {
                self.clash_auth_token = value.to_string();
            }
            "timeout_ttft_min_ms" => self.timeout_ttft_min_ms = Some(parse_i64(value)?),
            "timeout_ttft_max_ms" => self.timeout_ttft_max_ms = Some(parse_i64(value)?),
            "timeout_silence_min_ms" => self.timeout_silence_min_ms = Some(parse_i64(value)?),
            "timeout_silence_max_ms" => self.timeout_silence_max_ms = Some(parse_i64(value)?),
            "failover_probe_min" => self.failover_probe_min = Some(parse_i64(value)?),
            "failover_probe_max" => self.failover_probe_max = Some(parse_i64(value)?),
            "call_log_max" => self.call_log_max = Some(parse_i64(value)?),
            _ => {
                anyhow::bail!("Unknown config key: {}", key);
            }
        }
        self.save()?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::env;

    fn temp_config_dir() -> PathBuf {
        let dir = env::temp_dir().join("opencode2api-manager-test");
        fs::create_dir_all(&dir).ok();
        dir
    }

    #[test]
    fn test_config_save_and_load() {
        let dir = temp_config_dir();
        let config_path = dir.join("config.json");

        let mut config = Config::default();
        config.base_url = "http://127.0.0.1:8088/v1".to_string();
        config.default_password = "test123".to_string();

        let data = serde_json::to_string_pretty(&config).unwrap();
        fs::write(&config_path, data).unwrap();

        let loaded: Config = serde_json::from_str(
            &fs::read_to_string(&config_path).unwrap()
        ).unwrap();

        assert_eq!(loaded.base_url, "http://127.0.0.1:8088/v1");
        assert_eq!(loaded.default_password, "test123");

        fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn test_config_get_set() {
        let mut config = Config::default();
        config.set("base_url", "http://localhost:9090").unwrap();
        assert_eq!(config.get("base_url"), Some("http://localhost:9090".to_string()));

        config.set("default_password", "secret").unwrap();
        assert_eq!(config.get("default_password"), Some("secret".to_string()));
    }

    #[test]
    fn test_config_unknown_key() {
        let mut config = Config::default();
        let result = config.set("unknown_key", "value");
        assert!(result.is_err());
    }
}