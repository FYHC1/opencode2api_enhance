use crate::clash_yaml::ClashNode;
use anyhow::{bail, Context, Result};
use serde_json::json;

/// 从 Clash 节点生成 sing-box outbound 配置
fn build_outbound(node: &ClashNode) -> Result<serde_json::Value> {
    match node.node_type.as_str() {
        "trojan" => {
            let password = node.password.as_deref().unwrap_or_default();
            if password.is_empty() {
                bail!("节点 '{}' 缺少 password", node.name);
            }
            Ok(json!({
                "type": "trojan",
                "tag": "proxy",
                "server": node.server,
                "server_port": node.port,
                "password": password,
                "tls": {
                    "enabled": node.tls.unwrap_or(true),
                    "server_name": node.sni.as_deref().or(node.servername.as_deref()).unwrap_or(&node.server),
                    "insecure": node.skip_cert_verify.unwrap_or(false)
                }
            }))
        }
        "vless" => {
            let uuid = node.uuid.as_deref().unwrap_or_default();
            if uuid.is_empty() {
                bail!("节点 '{}' 缺少 uuid", node.name);
            }
            let server_name = node
                .servername
                .as_deref()
                .or(node.sni.as_deref())
                .unwrap_or(&node.server)
                .to_string();

            let mut tls = json!({
                "enabled": node.tls.unwrap_or(true),
                "server_name": server_name,
                "insecure": node.skip_cert_verify.unwrap_or(false)
            });
            if let Some(fp) = node.client_fingerprint.as_deref() {
                tls["utls"] = json!({"enabled": true, "fingerprint": fp});
            }

            let transport = build_transport(node);

            Ok(json!({
                "type": "vless",
                "tag": "proxy",
                "server": node.server,
                "server_port": node.port,
                "uuid": uuid,
                "tls": tls,
                "transport": transport
            }))
        }
        "vmess" => {
            let uuid = node.uuid.as_deref().unwrap_or_default();
            if uuid.is_empty() {
                bail!("节点 '{}' 缺少 uuid", node.name);
            }
            let transport = build_transport(node);
            Ok(json!({
                "type": "vmess",
                "tag": "proxy",
                "server": node.server,
                "server_port": node.port,
                "uuid": uuid,
                "security": "auto",
                "alter_id": 0,
                "tls": {
                    "enabled": node.tls.unwrap_or(false),
                    "server_name": node.servername.as_deref().or(node.sni.as_deref()).unwrap_or(&node.server),
                    "insecure": node.skip_cert_verify.unwrap_or(false)
                },
                "transport": transport
            }))
        }
        "ss" | "shadowsocks" => {
            let password = node.password.as_deref().unwrap_or_default();
            if password.is_empty() {
                bail!("节点 '{}' 缺少 password", node.name);
            }
            let method = node.cipher.as_deref().unwrap_or("aes-256-gcm");
            Ok(json!({
                "type": "shadowsocks",
                "tag": "proxy",
                "server": node.server,
                "server_port": node.port,
                "method": method,
                "password": password
            }))
        }
        other => bail!("暂不支持的节点类型: {}", other),
    }
}

/// 构建传输层配置（ws / http / tcp）
fn build_transport(node: &ClashNode) -> serde_json::Value {
    match node.network.as_deref() {
        Some("ws") => {
            let mut headers = json!({});
            let path = node
                .ws_opts
                .as_ref()
                .and_then(|o| o.path.clone())
                .unwrap_or_else(|| "/".to_string());
            if let Some(h) = node.ws_opts.as_ref().and_then(|o| o.headers.as_ref())
                && let Ok(j) = serde_json::to_value(h) {
                    headers = j;
                }
            json!({
                "type": "ws",
                "path": path,
                "headers": headers
            })
        }
        Some("http") => json!({
            "type": "http",
            "host": null,
            "path": null
        }),
        _ => json!({
            "type": "tcp"
        }),
    }
}

/// 生成完整 sing-box 配置文件
pub fn build_singbox_config(node: &ClashNode, listen_port: u16) -> Result<String> {
    let outbound = build_outbound(node)?;
    let config = json!({
        "log": {
            "level": "warn",
            "timestamp": true
        },
        "inbounds": [
            {
                "type": "socks",
                "listen": "127.0.0.1",
                "listen_port": listen_port
            }
        ],
        "outbounds": [
            outbound,
            {
                "type": "direct",
                "tag": "direct"
            }
        ],
        "route": {
            "final": "proxy"
        }
    });

    serde_json::to_string_pretty(&config).context("Failed to serialize sing-box config")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::clash_yaml::parse_clash_yaml;

    #[test]
    fn test_build_trojan_config() {
        let yaml = r#"
proxies:
  - {name: "🇸🇬 新加坡 G1", server: 139.177.187.106, port: 26150, type: trojan, password: JbdAgz2NJF, sni: v.qq.com, skip-cert-verify: true}
"#;
        let nodes = parse_clash_yaml(yaml).unwrap();
        let config = build_singbox_config(&nodes[0], 7890).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        let ob = &v["outbounds"][0];
        assert_eq!(ob["type"], "trojan");
        assert_eq!(ob["server"], "139.177.187.106");
        assert_eq!(ob["server_port"], 26150);
        assert_eq!(ob["password"], "JbdAgz2NJF");
        assert_eq!(ob["tls"]["server_name"], "v.qq.com");
        assert_eq!(ob["tls"]["insecure"], true);
        assert_eq!(v["inbounds"][0]["listen_port"], 7890);
        assert_eq!(v["route"]["final"], "proxy");
    }

    #[test]
    fn test_build_vless_config() {
        let yaml = r#"
proxies:
  - {name: CF移动优选1, server: 91.193.59.158, port: 2096, type: vless, uuid: 7a3bac2b-b3ae-4bf6-845a-31fa95bfde26, tls: true, skip-cert-verify: false, servername: edt0099.cuterzhuzhu.eu.org, client-fingerprint: chrome, network: ws, ws-opts: {path: /, headers: {Host: edt0099.cuterzhuzhu.eu.org}}}
"#;
        let nodes = parse_clash_yaml(yaml).unwrap();
        let config = build_singbox_config(&nodes[0], 7891).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        let ob = &v["outbounds"][0];
        assert_eq!(ob["type"], "vless");
        assert_eq!(ob["uuid"], "7a3bac2b-b3ae-4bf6-845a-31fa95bfde26");
        assert_eq!(ob["tls"]["server_name"], "edt0099.cuterzhuzhu.eu.org");
        assert_eq!(ob["tls"]["utls"]["fingerprint"], "chrome");
        assert_eq!(ob["transport"]["type"], "ws");
        assert_eq!(ob["transport"]["path"], "/");
    }

    #[test]
    fn test_unsupported_type() {
        let yaml = r#"
proxies:
  - {name: test, server: 1.2.3.4, port: 80, type: hysteria2}
"#;
        let nodes = parse_clash_yaml(yaml).unwrap();
        let result = build_singbox_config(&nodes[0], 7892);
        assert!(result.is_err());
    }
}