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
            _ => None,
        }
    }

    pub fn set(&mut self, key: &str, value: &str) -> Result<()> {
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