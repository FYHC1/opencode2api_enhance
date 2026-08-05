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
    // 流内超时切换区间配置：从 manager 配置读取，未设置用默认值（毫秒）
    let cfg = crate::config::Config::load().unwrap_or_default();
    let ttft_min = cfg.timeout_ttft_min_ms.unwrap_or(15000);
    let ttft_max = cfg.timeout_ttft_max_ms.unwrap_or(25000);
    let silence_min = cfg.timeout_silence_min_ms.unwrap_or(30000);
    let silence_max = cfg.timeout_silence_max_ms.unwrap_or(60000);
    let probe_min = cfg.failover_probe_min.unwrap_or(2);
    let probe_max = cfg.failover_probe_max.unwrap_or(3);
    let call_log_max = cfg.call_log_max.unwrap_or(5000);
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
        "route_mode": route_mode,
        "timeout_ttft_min_ms": ttft_min,
        "timeout_ttft_max_ms": ttft_max,
        "timeout_silence_min_ms": silence_min,
        "timeout_silence_max_ms": silence_max,
        "failover_probe_min": probe_min,
        "failover_probe_max": probe_max,
        "call_log_max": call_log_max
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
