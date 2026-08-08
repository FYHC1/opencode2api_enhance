use anyhow::Result;
use serde_json::json;

/// 生成 opencode2api 的 config.json
/// active_socks5 指向 sing-box 的 SOCKS5 端口
pub fn build_opencode_config(singbox_port: u16) -> Result<String> {
    let proxy_addr = format!("127.0.0.1:{}", singbox_port);
    let cfg = crate::config::Config::load().unwrap_or_default();
    let show_node_prefix = cfg.show_node_prefix.unwrap_or(false);
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
        "active_socks5": proxy_addr,
        "show_node_prefix": show_node_prefix
    });

    Ok(serde_json::to_string_pretty(&config)?)
}

/// 生成统一网关配置：池内实例作为 SOCKS5 代理列表，路由模式默认 failover
/// （成功不动游标、失败/限流/额度耗尽才切下一个健康实例）。
/// port_names 提供 singbox 端口 → 实例名映射，写入 socks5_proxies[].name，
/// 供 Go 侧流式输出显示「🤖 实例名 · 模型」（而非 SOCKS5 地址）。
pub fn build_opencode_router_config(
    singbox_ports: &[u16],
    port_names: &[(u16, String)],
    route_mode: &str,
) -> Result<String> {
    let name_for_port = |port: u16| -> String {
        port_names
            .iter()
            .find(|(p, _)| *p == port)
            .map(|(_, n)| n.clone())
            .unwrap_or_else(|| format!("instance-{}", port))
    };
    let proxies: Vec<serde_json::Value> = singbox_ports
        .iter()
        .map(|port| {
            json!({
                "name": name_for_port(*port),
                "addr": format!("127.0.0.1:{}", port),
                "username": "",
                "password": ""
            })
        })
        .collect();
    // 流内超时切换区间配置：从 manager 配置读取，未设置用默认值（毫秒）
    let cfg = crate::config::Config::load().unwrap_or_default();
    let ttft_min = cfg.timeout_ttft_min_ms.unwrap_or(10000);
    let ttft_max = cfg.timeout_ttft_max_ms.unwrap_or(10000);
    let silence_min = cfg.timeout_silence_min_ms.unwrap_or(5000);
    let silence_max = cfg.timeout_silence_max_ms.unwrap_or(5000);
    let probe_min = cfg.failover_probe_min.unwrap_or(2);
    let probe_max = cfg.failover_probe_max.unwrap_or(3);
    let call_log_max = cfg.call_log_max.unwrap_or(5000);
    let show_node_prefix = cfg.show_node_prefix.unwrap_or(false);
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
        "call_log_max": call_log_max,
        "show_node_prefix": show_node_prefix
    });

    Ok(serde_json::to_string_pretty(&config)?)
}

#[cfg(test)]
mod tests {
    use super::*;

    // 与 config 模块共用串行锁：本模块测试会 set OPCODE2API_DATA_DIR，
    // 而 Config::set()/load() 依赖该进程级 env 变量，需串行避免互相覆盖。
    fn lock() -> std::sync::MutexGuard<'static, ()> {
        crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner())
    }

    #[test]
    fn test_build_opencode_config() {
        let _guard = lock();
        // 隔离数据目录：build_opencode_config 会读 Config::load()，
        // 避免受开发者本机 config.json 的 show_node_prefix 设置影响
        let test_dir = std::env::temp_dir().join("opencode2api-cfg-test");
        let _ = std::fs::remove_dir_all(&test_dir);
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let config = build_opencode_config(7890).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "127.0.0.1:7890");
        assert_eq!(v["socks5_proxies"][0]["addr"], "127.0.0.1:7890");
        assert!(v["model_alias"]["deepseek-v4-flash"].is_string());
        assert_eq!(v["force_disable_thinking"], false);
        // show_node_prefix 默认 false（默认关闭）
        assert_eq!(v["show_node_prefix"], false);
        unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") };
    }

    #[test]
    fn test_build_opencode_config_different_port() {
        let _guard = lock();
        let config = build_opencode_config(7892).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "127.0.0.1:7892");
    }

    #[test]
    fn test_build_opencode_router_config() {
        let _guard = lock();
        // 隔离数据目录（同 test_build_opencode_config）
        let test_dir = std::env::temp_dir().join("opencode2api-cfg-test");
        let _ = std::fs::remove_dir_all(&test_dir);
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let names = vec![
            (18001u16, "日本1".to_string()),
            (18002u16, "美国2".to_string()),
        ];
        let config = build_opencode_router_config(&[18001, 18002], &names, "failover").unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["active_socks5"], "__round_robin__");
        assert_eq!(v["route_mode"], "failover");
        assert_eq!(v["socks5_proxies"].as_array().unwrap().len(), 2);
        // name 应取真实实例名（而非占位 instance-N）
        assert_eq!(v["socks5_proxies"][0]["name"], "日本1");
        assert_eq!(v["socks5_proxies"][0]["addr"], "127.0.0.1:18001");
        assert_eq!(v["socks5_proxies"][1]["name"], "美国2");
        // show_node_prefix 默认 false
        assert_eq!(v["show_node_prefix"], false);
        unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") };
    }

    #[test]
    fn test_build_opencode_router_config_round_robin() {
        let _guard = lock();
        let names = vec![
            (18001u16, "a".to_string()),
            (18002u16, "b".to_string()),
            (18003u16, "c".to_string()),
        ];
        let config =
            build_opencode_router_config(&[18001, 18002, 18003], &names, "round_robin").unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        assert_eq!(v["route_mode"], "round_robin");
        assert_eq!(v["socks5_proxies"].as_array().unwrap().len(), 3);
        assert_eq!(v["socks5_proxies"][2]["name"], "c");
    }
}
