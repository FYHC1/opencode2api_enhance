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
    /// 对话流是否展示「🤖 节点 · 模型」前缀（默认关闭）
    pub show_node_prefix: Option<bool>,
    /// 统一网关监听端口（None = 默认：debug 21080 / release 18080）
    pub gateway_port: Option<u16>,
    /// 统一网关鉴权密钥（None = 默认 "sk-unified-local"；空串=重置为默认）
    pub gateway_key: Option<String>,
    /// 管理 HTTP 服务端口（桌面/headless 共用；None = 默认 19090）
    pub http_port: Option<u16>,
    /// 订阅 URL（空 = 未配置）
    pub subscribe_url: Option<String>,
    /// 订阅自动拉取间隔分钟（0/None = 不自动拉取）
    pub subscribe_interval_min: Option<u32>,
    /// 健康巡检间隔秒（0/None = 关闭巡检）
    pub health_check_interval_sec: Option<u32>,
    /// 健康巡检失败 N 次自动重启（0/None = 不自动重启）
    pub health_restart_threshold: Option<u32>,
    /// 日志过滤关键词（逗号分隔）
    pub log_filter_keywords: Option<String>,
}

impl Config {
    pub fn config_dir() -> PathBuf {
        // 环境变量优先：OPCODE2API_DATA_DIR 可指向完全隔离的数据目录
        // （调试版与正式版共用 %APPDATA%\opencode2api-manager 会导致实例池/配置/runtime 互相干扰，
        //   调试版启动时由 lib.rs 自动设置独立目录实现隔离）
        if let Ok(dir) = std::env::var("OPCODE2API_DATA_DIR") {
            if !dir.trim().is_empty() {
                let p = PathBuf::from(dir);
                if let Err(e) = fs::create_dir_all(&p) {
                    eprintln!("警告: 创建数据目录失败 {}: {}", p.display(), e);
                }
                return p;
            }
        }
        let dir = dirs::config_dir()
            .unwrap_or_else(|| PathBuf::from("."))
            .join("opencode2api-manager");
        if let Err(e) = fs::create_dir_all(&dir) {
            eprintln!("警告: 创建数据目录失败 {}: {}", dir.display(), e);
        }
        dir
    }

    pub fn config_path() -> PathBuf {
        Self::config_dir().join("config.json")
    }

    pub fn load() -> Result<Self> {
        let path = Self::config_path();
        if path.exists() {
            let data = fs::read_to_string(&path).context("Failed to read config file")?;
            let config: Config =
                serde_json::from_str(&data).context("Failed to parse config file")?;
            Ok(config)
        } else {
            Ok(Config::default())
        }
    }

    /// 生效的默认密码：config 未设置时回退 "123456"。
    /// 实例未单独设置密码时，实例 API 门禁与探测均使用该值。
    pub fn effective_default_password() -> String {
        let password = Self::load().unwrap_or_default().default_password;
        if password.is_empty() {
            "123456".to_string()
        } else {
            password
        }
    }

    /// 生效的统一网关端口：config 未设置时回退默认（debug 21080 / release 18080）
    pub fn effective_gateway_port() -> u16 {
        Self::load()
            .unwrap_or_default()
            .gateway_port
            .unwrap_or_else(|| {
                #[cfg(debug_assertions)]
                {
                    21080
                }
                #[cfg(not(debug_assertions))]
                {
                    18080
                }
            })
    }

    /// 生效的统一网关密钥：config 未设置或为空时回退默认 "sk-unified-local"
    pub fn effective_gateway_key() -> String {
        Self::load()
            .unwrap_or_default()
            .gateway_key
            .filter(|k| !k.is_empty())
            .unwrap_or_else(|| "sk-unified-local".to_string())
    }

    /// 生效的管理 HTTP 端口：config 未设置时回退 19090
    pub fn effective_http_port() -> u16 {
        Self::load().unwrap_or_default().http_port.unwrap_or(19090)
    }

    pub fn save(&self) -> Result<()> {
        let path = Self::config_path();
        let data = serde_json::to_string_pretty(self).context("Failed to serialize config")?;
        fs::write(&path, data).context("Failed to write config file")?;
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
            "show_node_prefix" => Some(self.show_node_prefix.unwrap_or(false).to_string()),
            "gateway_port" => Some(self.gateway_port.map(|p| p.to_string()).unwrap_or_default()),
            "gateway_key" => Some(self.gateway_key.clone().unwrap_or_default()),
            "http_port" => Some(self.http_port.map(|p| p.to_string()).unwrap_or_default()),
            "subscribe_url" => Some(self.subscribe_url.clone().unwrap_or_default()),
            "subscribe_interval_min" => Some(self.subscribe_interval_min.unwrap_or(0).to_string()),
            "health_check_interval_sec" => {
                Some(self.health_check_interval_sec.unwrap_or(0).to_string())
            }
            "health_restart_threshold" => {
                Some(self.health_restart_threshold.unwrap_or(0).to_string())
            }
            "log_filter_keywords" => Some(self.log_filter_keywords.clone().unwrap_or_default()),
            _ => None,
        }
    }

    pub fn set(&mut self, key: &str, value: &str) -> Result<()> {
        let parse_i64 = |s: &str| -> Result<i64> {
            s.parse::<i64>()
                .with_context(|| format!("invalid integer for {key}: {s}"))
        };
        // u32 字段（订阅间隔/巡检间隔/重启阈值）拒绝负值——负值经 i64→u32 cast
        // 会 wrap 成 4294967295，导致后台循环休眠数年（实测 -1 → 4294967295）。
        let parse_u32 = |s: &str, key: &str| -> Result<u32> {
            s.parse::<u32>()
                .with_context(|| format!("invalid non-negative integer for {key}: {s}"))
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
            "show_node_prefix" => {
                let b = value
                    .parse::<bool>()
                    .with_context(|| format!("invalid boolean for show_node_prefix: {value}"))?;
                self.show_node_prefix = Some(b);
            }
            "gateway_port" => {
                if value.is_empty() {
                    self.gateway_port = None;
                } else {
                    let p = value
                        .parse::<u16>()
                        .with_context(|| format!("invalid port for gateway_port: {value}"))?;
                    self.gateway_port = Some(p);
                }
            }
            "gateway_key" => {
                // 空串 = 重置为默认；非空需至少 8 字符
                if value.is_empty() {
                    self.gateway_key = None;
                } else if value.len() < 8 {
                    anyhow::bail!("网关密钥至少 8 个字符");
                } else {
                    self.gateway_key = Some(value.to_string());
                }
            }
            "http_port" => {
                if value.is_empty() {
                    self.http_port = None;
                } else {
                    let p = value
                        .parse::<u16>()
                        .with_context(|| format!("invalid port for http_port: {value}"))?;
                    self.http_port = Some(p);
                }
            }
            "subscribe_url" => {
                self.subscribe_url = if value.is_empty() {
                    None
                } else {
                    Some(value.to_string())
                };
            }
            "subscribe_interval_min" => {
                self.subscribe_interval_min = Some(parse_u32(value, key)?);
            }
            "health_check_interval_sec" => {
                self.health_check_interval_sec = Some(parse_u32(value, key)?);
            }
            "health_restart_threshold" => {
                self.health_restart_threshold = Some(parse_u32(value, key)?);
            }
            "log_filter_keywords" => {
                self.log_filter_keywords = if value.is_empty() {
                    None
                } else {
                    Some(value.to_string())
                };
            }
            _ => {
                anyhow::bail!("Unknown config key: {}", key);
            }
        }
        self.save()?;
        Ok(())
    }
}

// Config::set() 内部会 save() 到全局 config_path()，而 config_dir() 读取进程级
// OPCODE2API_DATA_DIR env 变量；并行测试互相覆盖 env/配置文件会导致偶发失败，
// 故所有触碰 env 或配置文件的测试（config 与 opencode_cfg 模块）需持同一把串行锁执行。
#[cfg(test)]
pub(crate) static CONFIG_TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

#[cfg(test)]
mod tests {
    use super::*;
    use std::env;

    fn lock() -> std::sync::MutexGuard<'static, ()> {
        CONFIG_TEST_LOCK.lock().unwrap_or_else(|e| e.into_inner())
    }

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

        let loaded: Config =
            serde_json::from_str(&fs::read_to_string(&config_path).unwrap()).unwrap();

        assert_eq!(loaded.base_url, "http://127.0.0.1:8088/v1");
        assert_eq!(loaded.default_password, "test123");

        fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn test_config_get_set() {
        let _guard = lock();
        let mut config = Config::default();
        config.set("base_url", "http://localhost:9090").unwrap();
        assert_eq!(
            config.get("base_url"),
            Some("http://localhost:9090".to_string())
        );

        config.set("default_password", "secret").unwrap();
        assert_eq!(config.get("default_password"), Some("secret".to_string()));
    }

    #[test]
    fn test_config_show_node_prefix_get_set() {
        let _guard = lock();
        let mut config = Config::default();
        // 默认关闭
        assert_eq!(config.get("show_node_prefix"), Some("false".to_string()));
        config.set("show_node_prefix", "true").unwrap();
        assert_eq!(config.get("show_node_prefix"), Some("true".to_string()));
        assert_eq!(config.show_node_prefix, Some(true));
        // 非法布尔应报错
        assert!(config.set("show_node_prefix", "maybe").is_err());
    }

    #[test]
    fn test_config_unknown_key() {
        let _guard = lock();
        let mut config = Config::default();
        let result = config.set("unknown_key", "value");
        assert!(result.is_err());
    }

    #[test]
    fn test_config_dir_env_override() {
        let _guard = lock();
        // 保存原环境变量
        let orig = env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = env::temp_dir().join("opencode2api-manager-env-test");
        // 设置环境变量，验证 config_dir 指向它
        unsafe { env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let dir = Config::config_dir();
        assert_eq!(dir, test_dir);
        assert!(dir.exists(), "隔离目录应被创建");
        // 清理
        unsafe { env::remove_var("OPCODE2API_DATA_DIR") };
        match orig {
            Some(v) => unsafe { env::set_var("OPCODE2API_DATA_DIR", v) },
            None => {}
        }
        fs::remove_dir_all(&test_dir).ok();
    }

    #[test]
    fn test_config_new_fields_get_set() {
        let _guard = lock();
        let mut config = Config::default();
        config.set("gateway_port", "18080").unwrap();
        assert_eq!(config.gateway_port, Some(18080));
        assert_eq!(config.get("gateway_port"), Some("18080".to_string()));
        // 空值回退默认
        config.set("gateway_port", "").unwrap();
        assert_eq!(config.gateway_port, None);

        config.set("gateway_key", "my-secret-key").unwrap();
        assert_eq!(config.gateway_key.as_deref(), Some("my-secret-key"));
        // 短密钥应报错
        assert!(config.set("gateway_key", "short").is_err());
        // 空值重置为默认
        config.set("gateway_key", "").unwrap();
        assert_eq!(config.gateway_key, None);

        config.set("http_port", "19100").unwrap();
        assert_eq!(config.http_port, Some(19100));

        config.set("subscribe_interval_min", "30").unwrap();
        assert_eq!(config.subscribe_interval_min, Some(30));

        config.set("health_check_interval_sec", "60").unwrap();
        assert_eq!(config.health_check_interval_sec, Some(60));

        config.set("health_restart_threshold", "3").unwrap();
        assert_eq!(config.health_restart_threshold, Some(3));

        config.set("log_filter_keywords", "error,timeout").unwrap();
        assert_eq!(config.log_filter_keywords.as_deref(), Some("error,timeout"));
    }

    #[test]
    fn test_config_gateway_key_persisted() {
        let _guard = lock();
        let orig = env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = env::temp_dir().join(format!("oc2api-cfg-key-{}", std::process::id()));
        unsafe { env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let mut config = Config::default();
        config.set("gateway_key", "my-secret-key").unwrap();
        let on_disk = fs::read_to_string(Config::config_path()).unwrap();
        assert!(on_disk.contains("\"gateway_key\": \"my-secret-key\""));
        unsafe { env::remove_var("OPCODE2API_DATA_DIR") };
        match orig {
            Some(v) => unsafe { env::set_var("OPCODE2API_DATA_DIR", v) },
            None => {}
        }
        fs::remove_dir_all(&test_dir).ok();
    }
}
