//! 订阅拉取与解析：Clash YAML / V2Ray base64 / 明文链接
//!
//! 拉取的订阅节点会持久化到本地缓存（`config_dir/subscription.json`），
//! 由 `clash_yaml::list_nodes_with_group()` 一并读取，保证：
//! - 实例启动时（`start_instance_inner`）能按节点名找到完整配置生成 sing-box
//! - 节点池页面可展示订阅节点
//! - `node_ip` 能解析订阅节点地址

use crate::clash_yaml::ClashNode;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use std::time::Duration;

/// 订阅节点（轻量结构，可落为实例；raw 保留原始链接）
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SubscribeNode {
    pub name: String,
    pub server: String,
    pub port: u16,
    pub node_type: String,
    pub password: Option<String>,
    pub uuid: Option<String>,
    pub cipher: Option<String>,
    pub sni: Option<String>,
    pub network: Option<String>,
    pub ws_path: Option<String>,
    pub flow: Option<String>,
    pub tls: bool,
    pub raw: String,
}

/// 拉取并解析订阅，返回节点列表
pub fn fetch_subscription(url: &str) -> Result<Vec<SubscribeNode>, String> {
    let resp = reqwest::blocking::get(url).map_err(|e| format!("订阅拉取失败: {}", e))?;
    if !resp.status().is_success() {
        return Err(format!("订阅拉取失败: HTTP {}", resp.status()));
    }
    let body = resp
        .text()
        .map_err(|e| format!("读取订阅内容失败: {}", e))?;
    parse_subscription(&body)
}

/// 解析订阅内容（自动识别 Clash YAML / base64 / 明文链接）
pub fn parse_subscription(body: &str) -> Result<Vec<SubscribeNode>, String> {
    let trimmed = body.trim().trim_start_matches('\u{feff}');
    if trimmed.is_empty() {
        return Err("订阅内容为空".to_string());
    }
    if trimmed.starts_with("proxies:")
        || (trimmed.contains("proxies:") && trimmed.contains("type:"))
    {
        let nodes = crate::clash_yaml::parse_clash_yaml(trimmed)
            .map_err(|e| format!("解析 Clash YAML 失败: {}", e))?;
        Ok(nodes.iter().map(subscribe_from_clash).collect())
    } else if is_base64_like(trimmed) {
        match BASE64.decode(trimmed) {
            Ok(bytes) => match String::from_utf8(bytes) {
                Ok(text) => parse_plain_links(&text),
                Err(_) => Err("订阅内容不是有效 UTF-8".to_string()),
            },
            Err(_) => parse_plain_links(trimmed),
        }
    } else {
        parse_plain_links(trimmed)
    }
}

fn subscribe_from_clash(n: &ClashNode) -> SubscribeNode {
    SubscribeNode {
        name: n.name.clone(),
        server: n.server.clone(),
        port: n.port,
        node_type: n.node_type.clone(),
        password: n.password.clone(),
        uuid: n.uuid.clone(),
        cipher: n.cipher.clone(),
        sni: n.sni.clone().or_else(|| n.servername.clone()),
        network: n.network.clone(),
        ws_path: n.ws_opts.as_ref().and_then(|w| w.path.clone()),
        flow: n.flow.clone(),
        tls: n.tls.unwrap_or(true),
        raw: format!("{}@{}:{}", n.node_type, n.server, n.port),
    }
}

fn is_base64_like(s: &str) -> bool {
    s.len() > 40
        && s.chars()
            .take(60)
            .all(|c| c.is_ascii_alphanumeric() || c == '+' || c == '/' || c == '=' || c == '-')
}

/// 逐行解析 vmess:// vless:// trojan:// ss:// hysteria2:// 链接
fn parse_plain_links(s: &str) -> Result<Vec<SubscribeNode>, String> {
    let mut nodes = Vec::new();
    for line in s.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        match parse_uri_link(line) {
            Ok(Some(node)) => nodes.push(node),
            Ok(None) => continue,
            Err(e) => eprintln!("跳过无法解析的订阅行: {}", e),
        }
    }
    if nodes.is_empty() {
        Err("订阅内容中未解析到任何可用节点".to_string())
    } else {
        Ok(nodes)
    }
}

fn parse_uri_link(line: &str) -> Result<Option<SubscribeNode>, String> {
    if let Some(rest) = line.strip_prefix("vmess://") {
        return parse_vmess(rest);
    }
    if let Some(rest) = line.strip_prefix("vless://") {
        return Ok(Some(parse_vless(rest)?));
    }
    if let Some(rest) = line.strip_prefix("trojan://") {
        return Ok(Some(parse_trojan(rest)?));
    }
    if let Some(rest) = line.strip_prefix("ss://") {
        return Ok(Some(parse_ss(rest)?));
    }
    if let Some(rest) = line
        .strip_prefix("hysteria2://")
        .or_else(|| line.strip_prefix("hy2://"))
    {
        return Ok(Some(parse_hysteria2(rest)?));
    }
    Ok(None)
}

fn parse_vmess(rest: &str) -> Result<Option<SubscribeNode>, String> {
    let decoded = BASE64
        .decode(rest)
        .map_err(|_| "vmess:// 非 base64 编码".to_string())?;
    let text = String::from_utf8(decoded).map_err(|_| "vmess 内容非 UTF-8".to_string())?;
    let v: serde_json::Value =
        serde_json::from_str(&text).map_err(|_| "vmess JSON 解析失败".to_string())?;
    let server = v["add"].as_str().unwrap_or_default().to_string();
    let port = v["port"]
        .as_str()
        .and_then(|p| p.parse::<u16>().ok())
        .unwrap_or(0);
    let name = v["ps"].as_str().unwrap_or(&server).to_string();
    if server.is_empty() || port == 0 {
        return Ok(None);
    }
    let tls = v["tls"].as_str() == Some("tls");
    Ok(Some(SubscribeNode {
        name,
        server,
        port,
        node_type: "vmess".to_string(),
        uuid: Some(v["id"].as_str().unwrap_or_default().to_string()),
        password: None,
        cipher: Some(v["scy"].as_str().unwrap_or("auto").to_string()),
        sni: v["sni"].as_str().map(|s| s.to_string()),
        network: Some(v["net"].as_str().unwrap_or("tcp").to_string()),
        ws_path: v["path"].as_str().map(|s| s.to_string()),
        flow: None,
        tls,
        raw: format!("vmess://{}", rest),
    }))
}

fn parse_vless(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "vless 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    let uuid = userinfo.split('?').next().unwrap_or_default().to_string();
    let network = params
        .get("type")
        .cloned()
        .unwrap_or_else(|| "tcp".to_string());
    let security = params.get("security").cloned().unwrap_or_default();
    let path = params
        .get("path")
        .cloned()
        .or_else(|| params.get("host").cloned().filter(|h| !h.starts_with('.')));
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "vless".to_string(),
        password: None,
        uuid: Some(uuid),
        cipher: None,
        sni: params.get("sni").cloned(),
        network: Some(network),
        ws_path: path,
        flow: params.get("flow").cloned(),
        tls: security == "tls" || security == "reality",
        raw: format!("vless://{}", rest),
    })
}

fn parse_trojan(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "trojan 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    let password = userinfo.split('?').next().unwrap_or_default().to_string();
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "trojan".to_string(),
        password: Some(password),
        uuid: None,
        cipher: None,
        sni: params.get("sni").cloned(),
        network: params
            .get("type")
            .cloned()
            .or_else(|| Some("tcp".to_string())),
        ws_path: params.get("path").cloned(),
        flow: None,
        tls: params.get("security").map(|s| s == "tls").unwrap_or(true),
        raw: format!("trojan://{}", rest),
    })
}

fn parse_ss(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    // 两种格式：
    // 1. ss://base64(method:password@server:port)#name
    // 2. ss://base64(method:password)@server:port#name
    let (userinfo, hostport) = match auth.split_once('@') {
        Some((u, h)) => (u.to_string(), Some(h.to_string())),
        None => {
            // 尝试整体 base64 解码
            let decoded = BASE64
                .decode(auth)
                .map_err(|_| "ss:// 非 base64 编码".to_string())?;
            let text = String::from_utf8(decoded).map_err(|_| "ss 内容非 UTF-8".to_string())?;
            let (u, h) = text
                .split_once('@')
                .ok_or_else(|| "ss 链接缺少 @".to_string())?;
            (u.to_string(), Some(h.to_string()))
        }
    };
    let hostport = hostport.ok_or_else(|| "ss 链接缺少服务器地址".to_string())?;
    let (server, port) = split_host_port(&hostport)?;
    // userinfo 可能整体是 base64(method:password)
    let (method, password) = if let Some((m, p)) = userinfo.split_once(':') {
        (m.to_string(), p.to_string())
    } else {
        let decoded = BASE64
            .decode(&userinfo)
            .map_err(|_| "ss 用户信息非 base64".to_string())?;
        let text = String::from_utf8(decoded).map_err(|_| "ss 用户信息非 UTF-8".to_string())?;
        let (m, p) = text
            .split_once(':')
            .ok_or_else(|| "ss 用户信息缺少密码".to_string())?;
        (m.to_string(), p.to_string())
    };
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "ss".to_string(),
        password: Some(password),
        uuid: None,
        cipher: Some(method),
        sni: None,
        network: Some("tcp".to_string()),
        ws_path: None,
        flow: None,
        tls: false,
        raw: format!("ss://{}", rest),
    })
}

fn parse_hysteria2(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "hysteria2 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "hysteria2".to_string(),
        password: Some(userinfo.split('?').next().unwrap_or_default().to_string()),
        uuid: None,
        cipher: None,
        sni: params.get("sni").cloned(),
        network: None,
        ws_path: None,
        flow: None,
        tls: true,
        raw: format!("hysteria2://{}", rest),
    })
}

/// 拆分 fragment（#名称），返回 (去除 fragment 的主体, Option<名称>)
fn split_fragment(s: &str) -> (String, Option<String>) {
    match s.split_once('#') {
        Some((head, frag)) => (head.to_string(), Some(frag.trim().to_string())),
        None => (s.to_string(), None),
    }
}

fn split_host_port(hostport: &str) -> Result<(String, u16), String> {
    let (host, port) = hostport
        .split_once(':')
        .ok_or_else(|| format!("链接缺少端口: {}", hostport))?;
    let port = port
        .split('?')
        .next()
        .unwrap_or_default()
        .parse::<u16>()
        .map_err(|_| format!("端口无效: {}", port))?;
    Ok((host.to_string(), port))
}

/// 从 query 字符串解析参数（仅取首个 ? 之后的部分）
fn parse_query<'a>(userinfo: &'a str, full: &'a str) -> std::collections::HashMap<String, String> {
    let mut map = std::collections::HashMap::new();
    let q = full.split('?').nth(1).unwrap_or("");
    for pair in q.split('&') {
        if let Some((k, v)) = pair.split_once('=') {
            map.insert(k.to_string(), v.to_string());
        }
    }
    let _ = userinfo;
    map
}

/// 订阅缓存文件路径
pub fn subscription_cache_path() -> PathBuf {
    crate::config::Config::config_dir().join("subscription.json")
}

/// 持久化订阅节点缓存（供节点列表与实例启动读取）
pub fn save_subscription_cache(nodes: &[SubscribeNode]) -> Result<(), String> {
    let data =
        serde_json::to_string_pretty(nodes).map_err(|e| format!("序列化订阅缓存失败: {}", e))?;
    std::fs::write(subscription_cache_path(), data).map_err(|e| format!("写入订阅缓存失败: {}", e))
}

/// 读取订阅缓存（不存在时返回空列表）
pub fn load_subscription_cache() -> Vec<SubscribeNode> {
    let path = subscription_cache_path();
    if !path.exists() {
        return Vec::new();
    }
    std::fs::read_to_string(&path)
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

/// 从订阅缓存中删除节点（按名称），返回实际删除数量。
/// 供节点池「删除节点」使用——仅订阅缓存中的节点可删（外部 Clash 节点只读）。
pub fn remove_subscription_node(name: &str) -> Result<usize, String> {
    let mut nodes = load_subscription_cache();
    let before = nodes.len();
    nodes.retain(|n| n.name != name);
    if nodes.len() == before {
        return Ok(0);
    }
    save_subscription_cache(&nodes)?;
    Ok(before - nodes.len())
}

/// 批量删除订阅缓存节点（一次加载 + 持久化），返回实际删除数量。
/// 供节点池「删除选中」使用——仅订阅缓存中的节点可删（外部 Clash 节点只读）。
/// 已入实例的节点照常列入（实例仍保留其完整配置），对外部 Clash 节点静默跳过。
pub fn remove_subscription_nodes(names: &[String]) -> Result<usize, String> {
    if names.is_empty() {
        return Ok(0);
    }
    let wanted: std::collections::HashSet<&String> = names.iter().collect();
    let mut nodes = load_subscription_cache();
    let before = nodes.len();
    nodes.retain(|n| !wanted.contains(&n.name));
    if nodes.len() == before {
        return Ok(0);
    }
    save_subscription_cache(&nodes)?;
    Ok(before - nodes.len())
}

/// 订阅节点 → ClashNode（供 sing-box 生成与节点列表合并）
pub fn to_clash_node(n: &SubscribeNode) -> ClashNode {
    ClashNode {
        name: n.name.clone(),
        server: n.server.clone(),
        port: n.port,
        node_type: n.node_type.clone(),
        password: n.password.clone(),
        uuid: n.uuid.clone(),
        cipher: n.cipher.clone(),
        sni: n.sni.clone(),
        servername: n.sni.clone(),
        tls: Some(n.tls),
        skip_cert_verify: None,
        network: n.network.clone(),
        up: None,
        down: None,
        obfs: None,
        obfs_password: None,
        ws_opts: n.ws_path.as_ref().map(|p| crate::clash_yaml::WsOpts {
            path: Some(p.clone()),
            headers: None,
        }),
        ws_headers: None,
        client_fingerprint: None,
        flow: n.flow.clone(),
        reality_opts: None,
        alpn: None,
        group: String::new(),
    }
}

/// 仅拉取并缓存订阅节点（不创建实例），返回节点数。
/// 节点池页「从订阅导入」使用：节点进入订阅缓存，随后用户可在节点池页
/// 勾选并按「独享 / 进池」批量添加为实例。
pub fn import_subscription_pool(url: &str) -> Result<usize, String> {
    let nodes = fetch_subscription(url)?;
    if nodes.is_empty() {
        return Err("订阅中未解析到任何节点".to_string());
    }
    save_subscription_cache(&nodes)?;
    Ok(nodes.len())
}

/// 批量导入订阅节点为实例（含持久化订阅缓存）。
/// `join_gateway` 为 true 时导入的实例打上入池标记（不自动启动，启停由实例池页控制）。
pub fn import_subscription(
    core: &crate::core::AppCore,
    url: &str,
    join_gateway: bool,
) -> Result<usize, String> {
    let nodes = fetch_subscription(url)?;
    if nodes.is_empty() {
        return Err("订阅中未解析到任何节点".to_string());
    }
    save_subscription_cache(&nodes)?;

    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let _ = mgr.load();
    let existing_instances = mgr.list_instances().to_vec();
    let mut existing_names: std::collections::HashSet<String> =
        existing_instances.iter().map(|i| i.name.clone()).collect();
    let mut used_ports: std::collections::HashSet<u16> =
        existing_instances.iter().map(|i| i.port).collect();
    // 按节点身份（node 名 + 端口）匹配已存在实例，重复的订阅节点不重复创建
    // （自动拉取每轮调用本函数，否则实例会无限增长）。
    let existing_ids: std::collections::HashSet<String> = existing_instances
        .iter()
        .map(|i| format!("{}|{}", i.node, i.port))
        .collect();

    let mut imported = 0usize;
    for node in &nodes {
        let node_id = format!("{}|{}", node.name, node.port);
        if existing_ids.contains(&node_id) {
            continue;
        }
        let mut name = crate::commands::sanitize_instance_name(&node.name);
        if existing_names.contains(&name) {
            let mut i = 2u32;
            while existing_names.contains(&format!("{}-{}", name, i)) {
                i += 1;
            }
            name = format!("{}-{}", name, i);
        }
        let mut port = node.port;
        while used_ports.contains(&port) {
            port = port.saturating_add(1);
        }
        let ip = format!("{}:{}", node.server, node.port);
        let sk = crate::commands::gen_sk_key();
        mgr.add_instance(name.clone(), port, node.name.clone(), sk, ip)
            .map_err(|e| format!("导入实例 '{}' 失败: {}", node.name, e))?;
        if join_gateway {
            let _ = mgr.set_join_gateway(&name, true);
        }
        existing_names.insert(name);
        used_ports.insert(port);
        imported += 1;
    }
    mgr.save_state().map_err(|e| e.to_string())?;
    drop(mgr);
    crate::commands::sync_gateway_core(core);
    Ok(imported)
}

/// 后台订阅循环：按配置间隔自动拉取并入实例。
/// interval_min <= 0 或 URL 为空时休眠 30s 再查配置（配置变更无需重启）。
pub async fn subscribe_loop(core: std::sync::Arc<crate::core::AppCore>) {
    loop {
        let config = crate::config::Config::load().unwrap_or_default();
        let interval_min = config.subscribe_interval_min.unwrap_or(0);
        let url = config.subscribe_url.clone().unwrap_or_default();
        if interval_min > 0 && !url.is_empty() {
            let core2 = core.clone();
            let url2 = url.clone();
            let _ = tokio::task::spawn_blocking(move || match import_subscription(&core2, &url2, false) {
                Ok(n) => println!("订阅自动拉取完成，导入 {} 个节点", n),
                Err(e) => eprintln!("订阅自动拉取失败: {}", e),
            })
            .await;
        }
        tokio::time::sleep(Duration::from_secs(interval_min.max(1) as u64 * 60)).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_clash_yaml_subscription() {
        let body = r#"proxies:
  - name: 🇭🇰 HK-01
    type: trojan
    server: hk.example.com
    port: 443
    password: secret123
  - name: 🇯🇵 JP-01
    type: vless
    server: jp.example.com
    port: 8443
    uuid: 12345678-1234-1234-1234-123456789012
    network: ws
    tls: true
"#;
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 2);
        assert_eq!(nodes[0].node_type, "trojan");
        assert_eq!(nodes[0].server, "hk.example.com");
        assert_eq!(nodes[1].node_type, "vless");
        assert_eq!(
            nodes[1].uuid.as_deref(),
            Some("12345678-1234-1234-1234-123456789012")
        );
    }

    #[test]
    fn test_parse_vless_link() {
        let body = "vless://abc-uuid-xyz@example.com:443?security=tls&sni=cdn.example.com&type=ws&path=%2Fws#MyNode";
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 1);
        let n = &nodes[0];
        assert_eq!(n.name, "MyNode");
        assert_eq!(n.server, "example.com");
        assert_eq!(n.port, 443);
        assert_eq!(n.node_type, "vless");
        assert_eq!(n.uuid.as_deref(), Some("abc-uuid-xyz"));
        assert_eq!(n.sni.as_deref(), Some("cdn.example.com"));
        assert_eq!(n.ws_path.as_deref(), Some("%2Fws"));
        assert!(n.tls);
    }

    #[test]
    fn test_parse_trojan_link() {
        let body = "trojan://password123@tg.example.com:443?security=tls&sni=tg.example.com#TG";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.name, "TG");
        assert_eq!(n.password.as_deref(), Some("password123"));
        assert_eq!(n.node_type, "trojan");
        assert!(n.tls);
    }

    #[test]
    fn test_parse_ss_link() {
        let body = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@ss.example.com:8388#SS1";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.name, "SS1");
        assert_eq!(n.node_type, "ss");
        assert_eq!(n.cipher.as_deref(), Some("aes-256-gcm"));
        assert_eq!(n.password.as_deref(), Some("password"));
    }

    #[test]
    fn test_parse_base64_wrapped_links() {
        // base64 包裹的多行 vless 链接
        let plain = "vless://uuid1@a.example.com:443?security=tls&sni=a.example.com#A\ntrojan://pw@b.example.com:443#B";
        let encoded = BASE64.encode(plain);
        let nodes = parse_subscription(&encoded).unwrap();
        assert_eq!(nodes.len(), 2);
        assert_eq!(nodes[0].name, "A");
        assert_eq!(nodes[1].name, "B");
    }

    #[test]
    fn test_import_duplicate_name_suffix() {
        let nodes = vec![SubscribeNode {
            name: "NodeX".to_string(),
            server: "1.2.3.4".to_string(),
            port: 443,
            node_type: "trojan".to_string(),
            password: Some("pw".to_string()),
            uuid: None,
            cipher: None,
            sni: None,
            network: None,
            ws_path: None,
            flow: None,
            tls: true,
            raw: "trojan://pw@1.2.3.4:443".to_string(),
        }];
        // to_clash_node 应保留关键字段
        let cn = to_clash_node(&nodes[0]);
        assert_eq!(cn.name, "NodeX");
        assert_eq!(cn.server, "1.2.3.4");
        assert_eq!(cn.node_type, "trojan");
        assert_eq!(cn.password.as_deref(), Some("pw"));
    }

    #[test]
    fn test_remove_subscription_nodes_batch() {
        // 触碰进程级 OPCODE2API_DATA_DIR env，需与 config/opencode_cfg 测试持同一串行锁，
        // 避免全量并行时互相覆盖 env 导致偶发失败。
        let _guard = crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        // 与 config.rs 测试相同的隔离方式：临时数据目录
        let orig = std::env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = std::env::temp_dir().join("opencode2api-manager-sub-rm-test");
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let _ = std::fs::remove_dir_all(&test_dir);

        let mk = |name: &str| SubscribeNode {
            name: name.to_string(),
            server: "1.2.3.4".to_string(),
            port: 443,
            node_type: "trojan".to_string(),
            password: Some("pw".to_string()),
            uuid: None,
            cipher: None,
            sni: None,
            network: None,
            ws_path: None,
            flow: None,
            tls: true,
            raw: format!("trojan://pw@1.2.3.4:443#{}", name),
        };
        save_subscription_cache(&[mk("A"), mk("B"), mk("C")]).unwrap();

        // 删两个存在的 + 一个不存在的 → 只删 2
        let removed = remove_subscription_nodes(&["A".to_string(), "B".to_string(), "X".to_string()]).unwrap();
        assert_eq!(removed, 2);
        let left = load_subscription_cache();
        assert_eq!(left.len(), 1);
        assert_eq!(left[0].name, "C");

        // 空列表 → 0
        assert_eq!(remove_subscription_nodes(&[]).unwrap(), 0);

        // 全部删光 → 缓存文件为空列表
        assert_eq!(remove_subscription_nodes(&["C".to_string()]).unwrap(), 1);
        assert_eq!(load_subscription_cache().len(), 0);

        let _ = std::fs::remove_dir_all(&test_dir);
        match orig {
            Some(v) => unsafe { std::env::set_var("OPCODE2API_DATA_DIR", v) },
            None => unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") },
        }
    }
}
