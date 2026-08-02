use anyhow::Result;
use serde_json::json;

/// 生成 opencode2api 的 config.json
/// active_socks5 指向 sing-box 的 SOCKS5 端口
pub fn build_opencode_config(singbox_port: u16) -> Result<String> {
    let proxy_addr = format!("127.0.0.1:{}", singbox_port);
    let config = json!({
        "model_alias": {
            "deepseek-v4-flash": "deepseek-v4-flash-free",
            "mimo-v2.5": "mimo-v2.5-free",
            "ling-3.0-flash": "ling-3.0-flash-free",
            "nemotron-3-ultra": "nemotron-3-ultra-free",
            "north-mini-code": "north-mini-code-free",
            "laguna-s-2.1": "laguna-s-2.1-free"
        },
        "reasoning_effort_map": {
            "minimal": "low",
            "medium": "medium",
            "high": "high"
        },
        "force_disable_thinking": false,
        "socks5_proxies": [
            {
                "name": "singbox",
                "addr": proxy_addr,
                "username": "",
                "password": ""
            }
        ],
        "active_socks5": proxy_addr
    });

    Ok(serde_json::to_string_pretty(&config)?)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_build_opencode_config() {
        let config = build_opencode_config(7890).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "127.0.0.1:7890");
        assert_eq!(v["socks5_proxies"][0]["addr"], "127.0.0.1:7890");
        assert!(v["model_alias"]["deepseek-v4-flash"].is_string());
        assert_eq!(v["force_disable_thinking"], false);
    }

    #[test]
    fn test_build_opencode_config_different_port() {
        let config = build_opencode_config(7892).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "127.0.0.1:7892");
    }
}