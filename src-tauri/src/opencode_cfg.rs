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

/// 生成统一网关配置：池内实例作为 SOCKS5 代理列表，路由模式默认 failover
/// （成功不动游标、失败/限流/额度耗尽才切下一个健康实例）。
pub fn build_opencode_router_config(singbox_ports: &[u16], route_mode: &str) -> Result<String> {
    let proxies: Vec<serde_json::Value> = singbox_ports
        .iter()
        .enumerate()
        .map(|(index, port)| {
            json!({
                "name": format!("instance-{}", index + 1),
                "addr": format!("127.0.0.1:{}", port),
                "username": "",
                "password": ""
            })
        })
        .collect();
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
        "socks5_proxies": proxies,
        "active_socks5": "__round_robin__",
        "route_mode": route_mode
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

    #[test]
    fn test_build_opencode_router_config() {
        let config = build_opencode_router_config(&[18001, 18002], "failover").unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "__round_robin__");
        assert_eq!(v["route_mode"], "failover");
        assert_eq!(v["socks5_proxies"].as_array().unwrap().len(), 2);
        assert_eq!(v["socks5_proxies"][0]["name"], "instance-1");
        assert_eq!(v["socks5_proxies"][0]["addr"], "127.0.0.1:18001");
    }

    #[test]
    fn test_build_opencode_router_config_round_robin() {
        let config = build_opencode_router_config(&[18001, 18002, 18003], "round_robin").unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["route_mode"], "round_robin");
        assert_eq!(v["socks5_proxies"].as_array().unwrap().len(), 3);
    }
}
