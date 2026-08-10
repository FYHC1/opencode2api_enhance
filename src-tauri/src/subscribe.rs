//! 订阅拉取与解析：Clash YAML / V2Ray base64 / 明文链接
//!
//! 拉取的订阅节点会持久化到本地缓存（`config_dir/subscription.json`），
//! 由 `clash_yaml::list_nodes_with_group()` 一并读取，保证：
//! - 实例启动时（`start_instance_inner`）能按节点名找到完整配置生成 sing-box
//! - 节点池页面可展示订阅节点
//! - `node_ip` 能解析订阅节点地址

use crate::clash_yaml::ClashNode;
use base64::{
    engine::general_purpose::{
        STANDARD as BASE64, STANDARD_NO_PAD as BASE64_NOPAD, URL_SAFE as BASE64_URLSAFE,
        URL_SAFE_NO_PAD as BASE64_URLSAFE_NOPAD,
    },
    Engine,
};
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
    /// VLESS REALITY 公钥（pbk），缺失会导致 TLS 握手失败
    pub reality_pbk: Option<String>,
    /// VLESS REALITY short-id（sid）
    pub reality_sid: Option<String>,
    /// TLS 指纹（fp，缺省 chrome）
    pub client_fingerprint: Option<String>,
    /// hysteria2 Salamander 混淆类型（obfs）
    pub obfs: Option<String>,
    /// hysteria2 Salamander 混淆密码（obfs-password）
    pub obfs_password: Option<String>,
    /// 跳过证书校验（insecure=1）
    pub skip_cert_verify: bool,
    /// 订阅来源分组名（订阅名；无名称时由导入方填 "订阅N"）
    pub group: String,
    pub raw: String,
}

/// 订阅元信息（来自 HTTP 响应头，clash-verge-rev 同款解析）
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct SubscriptionMeta {
    pub name: Option<String>,
    pub upload: Option<u64>,
    pub download: Option<u64>,
    pub total: Option<u64>,
    pub expire: Option<u64>,
    pub home: Option<String>,
}

/// 拉取并解析订阅，返回节点列表
pub fn fetch_subscription(url: &str) -> Result<Vec<SubscribeNode>, String> {
    fetch_subscription_with_meta(url).map(|(nodes, _)| nodes)
}

/// 拉取并解析订阅，同时返回响应头元信息（流量/到期/订阅名）
pub fn fetch_subscription_with_meta(url: &str) -> Result<(Vec<SubscribeNode>, SubscriptionMeta), String> {
    let resp = reqwest::blocking::get(url).map_err(|e| format!("订阅拉取失败: {}", e))?;
    if !resp.status().is_success() {
        return Err(format!("订阅拉取失败: HTTP {}", resp.status()));
    }
    let meta = parse_subscription_headers(&resp);
    let body = resp
        .text()
        .map_err(|e| format!("读取订阅内容失败: {}", e))?;
    let nodes = parse_subscription(&body)?;
    Ok((nodes, meta))
}

/// 解析订阅响应头（clash-verge-rev prfitem.rs::from_url 同款）：
/// - `*-subscription-userinfo`：upload/download/total/expire
/// - `Content-Disposition`：订阅文件名（filename*= percent-decode 优先）
/// - `profile-web-page-url`：订阅主页
fn parse_subscription_headers(resp: &reqwest::blocking::Response) -> SubscriptionMeta {
    let mut meta = SubscriptionMeta::default();
    let headers = resp.headers();
    for (k, v) in headers.iter() {
        let key = k.as_str().to_ascii_lowercase();
        if key.ends_with("subscription-userinfo") {
            let info = v.to_str().unwrap_or("");
            for part in info.split_whitespace() {
                if let Some((k2, v2)) = part.split_once('=') {
                    let val = v2.parse::<u64>().unwrap_or(0);
                    match k2 {
                        "upload" => meta.upload = Some(val),
                        "download" => meta.download = Some(val),
                        "total" => meta.total = Some(val),
                        "expire" => meta.expire = Some(val),
                        _ => {}
                    }
                }
            }
        } else if key == "content-disposition" {
            let raw = v.to_str().unwrap_or("");
            meta.name = parse_content_disposition_name(raw);
        } else if key == "profile-web-page-url" {
            meta.home = v.to_str().ok().map(|s| s.to_string());
        }
    }
    meta
}

/// 解析 Content-Disposition 里的文件名：`filename*=UTF-8''%E5%AD%90...` 优先，退化到 `filename=`
fn parse_content_disposition_name(raw: &str) -> Option<String> {
    for part in raw.split(';') {
        let part = part.trim();
        if let Some(val) = part.strip_prefix("filename*=") {
            let val = val.trim_matches('"');
            if let Some((_, encoded)) = val.split_once("''") {
                return Some(percent_decode(encoded));
            }
            return Some(percent_decode(val));
        }
    }
    for part in raw.split(';') {
        let part = part.trim();
        if let Some(val) = part.strip_prefix("filename=") {
            return Some(val.trim_matches('"').to_string());
        }
    }
    None
}

/// 解析订阅内容（自动识别 Clash YAML / base64 / 明文链接）
pub fn parse_subscription(body: &str) -> Result<Vec<SubscribeNode>, String> {
    let trimmed = body.trim().trim_start_matches('\u{feff}');
    if trimmed.is_empty() {
        return Err("订阅内容为空".to_string());
    }
    let nodes = if trimmed.starts_with("proxies:")
        || (trimmed.contains("proxies:") && trimmed.contains("type:"))
    {
        let nodes = crate::clash_yaml::parse_clash_yaml(trimmed)
            .map_err(|e| format!("解析 Clash YAML 失败: {}", e))?;
        nodes.iter().map(subscribe_from_clash).collect()
    } else if trimmed.starts_with("{\"outbounds\"") || trimmed.starts_with("{\n  \"outbounds\"") {
        parse_singbox_json(trimmed)?
    } else if let Some(text) = decode_base64_loose(trimmed) {
        if text.starts_with("{\"outbounds\"") {
            parse_singbox_json(&text)?
        } else if text.contains("://") {
            // base64 订阅整体解码成功且含协议链接 → 按解码后文本解析
            parse_plain_links(&text)?
        } else {
            parse_plain_links(trimmed)?
        }
    } else {
        parse_plain_links(trimmed)?
    };
    // 过滤订阅头部的「信息伪节点」（官网/更新时间/剩余时长等公告伪装成 vless 链接），
    // 它们不是真实节点，导入后探测必然失败。
    let mut nodes: Vec<SubscribeNode> = nodes
        .into_iter()
        .filter(|n| !is_info_pseudo_node(&n.name))
        .collect();
    if nodes.is_empty() {
        return Err("订阅内容中未解析到任何可用节点".to_string());
    }
    dedupe_node_names(&mut nodes);
    Ok(nodes)
}

/// 重名节点追加 `-02`/`-03` 后缀（mihomo uniqueName 语义），避免导入实例时同名冲突。
fn dedupe_node_names(nodes: &mut [SubscribeNode]) {
    let mut seen: std::collections::HashMap<String, u32> = std::collections::HashMap::new();
    for node in nodes.iter_mut() {
        let idx = seen.entry(node.name.clone()).or_insert(0);
        if *idx > 0 {
            node.name = format!("{}-{:02}", node.name, *idx + 1);
        }
        *idx += 1;
    }
}

/// 订阅头部公告/信息伪节点名称前缀（如「官网：xuelian.pro」「更新时间：…」「剩余时长：…」）。
/// 这些行伪装成 vless/anytls 链接指向占位服务器，解码后名称不含国家地区且带全角冒号，据此识别。
/// 名称可能带 `[anytls]` 这类协议前缀，先剥离再判断。
fn is_info_pseudo_node(name: &str) -> bool {
    const BANNER_PREFIXES: &[&str] = &[
        "官网", "网站", "主页", "更新时间", "更新于", "剩余时长", "剩余流量", "到期时间",
        "过期时间", "套餐", "订阅", "公告", "通知", "电报", "频道", "群组", "客服", "工单",
        "说明", "注意", "流量", "账号", "节点数",
    ];
    let n = name.trim();
    // 剥离 `[anytls]` 等协议前缀标签
    let n = match n.strip_prefix('[').and_then(|s| s.split_once(']')) {
        Some((_, rest)) => rest.trim(),
        None => n,
    };
    BANNER_PREFIXES.iter().any(|p| n.starts_with(p)) && n.contains('：')
}

/// 解析 sing-box JSON 订阅（SFA / v2rayN 支持的第三种格式）：
/// `{"outbounds":[{"type":"vless","tag":"...","server":"...","server_port":443,...}]}`
/// 字段与 singbox.rs::build_outbound 输出同构，此处为逆向映射。
fn parse_singbox_json(body: &str) -> Result<Vec<SubscribeNode>, String> {
    let v: serde_json::Value =
        serde_json::from_str(body).map_err(|e| format!("解析 sing-box JSON 失败: {}", e))?;
    let outbounds = v["outbounds"].as_array().ok_or("sing-box JSON 缺少 outbounds 数组")?;
    let mut nodes = Vec::new();
    for ob in outbounds {
        let node_type = match ob["type"].as_str() {
            Some(t) => t.to_string(),
            None => continue,
        };
        let tag = ob["tag"].as_str().unwrap_or_default();
        let server = ob["server"].as_str().unwrap_or_default().to_string();
        let port = ob["server_port"].as_u64().unwrap_or(0) as u16;
        if server.is_empty() || port == 0 {
            continue;
        }
        let tls_enabled = ob["tls"]["enabled"].as_bool().unwrap_or(false);
        let sni = ob["tls"]["server_name"].as_str().map(|s| s.to_string());
        let reality_pbk = ob["tls"]["reality"]["public_key"].as_str().map(|s| s.to_string());
        let reality_sid = ob["tls"]["reality"]["short_id"].as_str().map(|s| s.to_string());
        let fingerprint = ob["tls"]["utls"]["fingerprint"].as_str().map(|s| s.to_string());
        let network = ob["transport"]["type"].as_str().map(|s| s.to_string());
        let ws_path = ob["transport"]["path"].as_str().map(|s| s.to_string());
        let flow = ob["flow"].as_str().map(|s| s.to_string());
        let obfs = ob["obfs"]["type"].as_str().map(|s| s.to_string());
        let obfs_password = ob["obfs"]["password"].as_str().map(|s| s.to_string());
        let skip_cert_verify = ob["tls"]["insecure"].as_bool().unwrap_or(false);

        let (password, uuid, cipher) = match node_type.as_str() {
            "vless" => (None, ob["uuid"].as_str().map(|s| s.to_string()), None),
            "vmess" => (
                None,
                ob["uuid"].as_str().map(|s| s.to_string()),
                ob["security"].as_str().map(|s| s.to_string()),
            ),
            "trojan" => (ob["password"].as_str().map(|s| s.to_string()), None, None),
            "shadowsocks" => (
                ob["password"].as_str().map(|s| s.to_string()),
                None,
                ob["method"].as_str().map(|s| s.to_string()),
            ),
            "hysteria2" => (ob["password"].as_str().map(|s| s.to_string()), None, None),
            "tuic" => (
                ob["password"].as_str().map(|s| s.to_string()),
                ob["uuid"].as_str().map(|s| s.to_string()),
                None,
            ),
            "wireguard" => (
                ob["private_key"].as_str().map(|s| s.to_string()),
                None,
                None,
            ),
            "socks" | "http" => {
                (ob["username"].as_str().map(|s| s.to_string()), None, None)
            }
            _ => continue,
        };

        let name = if tag.is_empty() {
            server.clone()
        } else {
            percent_decode(tag).to_string()
        };
        nodes.push(SubscribeNode {
            name,
            server,
            port,
            node_type,
            password,
            uuid,
            cipher,
            sni,
            network,
            ws_path,
            flow,
            tls: tls_enabled || reality_pbk.is_some(),
            reality_pbk,
            reality_sid,
            client_fingerprint: fingerprint,
            obfs,
            obfs_password,
            skip_cert_verify,
            group: String::new(),
            raw: serde_json::to_string(ob).unwrap_or_default(),
        });
    }
    if nodes.is_empty() {
        return Err("sing-box JSON 中未解析到任何可用节点".to_string());
    }
    Ok(nodes)
}

fn subscribe_from_clash(n: &ClashNode) -> SubscribeNode {
    // wireguard: private-key→password, public-key→cipher；hysteria: auth-str→password
    let password = n
        .password
        .clone()
        .or_else(|| n.private_key.clone())
        .or_else(|| n.auth_str.clone());
    let cipher = n.cipher.clone().or_else(|| n.public_key.clone());
    SubscribeNode {
        name: n.name.clone(),
        server: n.server.clone(),
        port: n.port,
        node_type: n.node_type.clone(),
        password,
        uuid: n.uuid.clone(),
        cipher,
        sni: n.sni.clone().or_else(|| n.servername.clone()),
        network: n.network.clone(),
        ws_path: n.ws_opts.as_ref().and_then(|w| w.path.clone()),
        flow: n.flow.clone(),
        // anytls/hysteria2/tuic/hysteria 强制 TLS，忽略 Clash 里错误的 tls: false
        tls: match n.node_type.as_str() {
            "anytls" | "hysteria2" | "hy2" | "tuic" | "hysteria" => true,
            _ => n.tls.unwrap_or(true),
        },
        reality_pbk: n.reality_opts.as_ref().and_then(|r| r.public_key.clone()),
        reality_sid: n.reality_opts.as_ref().and_then(|r| r.short_id.clone()),
        client_fingerprint: n.client_fingerprint.clone(),
        obfs: n.obfs.clone(),
        obfs_password: n.obfs_password.clone(),
        skip_cert_verify: n.skip_cert_verify.unwrap_or(false),
        group: String::new(),
        raw: format!("{}@{}:{}", n.node_type, n.server, n.port),
    }
}

/// 容错 base64 解码（借鉴 v2rayN Base64Decode + mihomo DecodeBase64）：
/// 1. 去空白/换行；`_`→`/`、`-`→`+`（URL-safe 变体）；自动补 `=` padding
/// 2. 依次尝试 RawStd → Std → RawUrlSafe → UrlSafe
/// 3. 全部失败返回 None（调用方降级为明文解析），解码出非 UTF-8 也返回 None
fn decode_base64_loose(s: &str) -> Option<String> {
    let mut cleaned: String = s
        .chars()
        .filter(|c| !c.is_whitespace())
        .map(|c| match c {
            '_' => '/',
            '-' => '+',
            other => other,
        })
        .collect();
    let rem = cleaned.len() % 4;
    if rem != 0 {
        for _ in 0..(4 - rem) {
            cleaned.push('=');
        }
    }
    for engine in [BASE64_NOPAD, BASE64, BASE64_URLSAFE_NOPAD, BASE64_URLSAFE] {
        if let Ok(bytes) = engine.decode(&cleaned) {
            if let Ok(text) = String::from_utf8(bytes) {
                return Some(text);
            }
        }
    }
    None
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
    if let Some(rest) = line.strip_prefix("hysteria://") {
        return Ok(Some(parse_hysteria(rest)?));
    }
    if let Some(rest) = line.strip_prefix("tuic://") {
        return Ok(Some(parse_tuic(rest)?));
    }
    if let Some(rest) = line.strip_prefix("wg://") {
        return Ok(Some(parse_wireguard(rest)?));
    }
    if let Some(rest) = line.strip_prefix("socks://").or_else(|| line.strip_prefix("socks5://")) {
        return Ok(Some(parse_socks(rest)?));
    }
    if let Some(rest) = line.strip_prefix("anytls://") {
        return Ok(Some(parse_anytls(rest)?));
    }
    Ok(None)
}

fn parse_vmess(rest: &str) -> Result<Option<SubscribeNode>, String> {
    let decoded = decode_base64_loose(rest)
        .ok_or_else(|| "vmess:// 非 base64 编码".to_string())?;
    let v: serde_json::Value =
        serde_json::from_str(&decoded).map_err(|_| "vmess JSON 解析失败".to_string())?;
    let server = v["add"].as_str().unwrap_or_default().to_string();
    let port = v["port"]
        .as_str()
        .and_then(|p| p.parse::<u16>().ok())
        .unwrap_or(0);
    let name = percent_decode(v["ps"].as_str().unwrap_or(&server)).to_string();
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
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
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
        reality_pbk: params.get("pbk").cloned(),
        reality_sid: params.get("sid").cloned(),
        client_fingerprint: params.get("fp").cloned(),
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
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
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
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
            // 尝试整体 base64 解码（容错：URL-safe 变体/padding 缺失均可）
            let text = decode_base64_loose(&auth)
                .ok_or_else(|| "ss:// 非 base64 编码".to_string())?;
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
        let text = decode_base64_loose(&userinfo)
            .ok_or_else(|| "ss 用户信息非 base64".to_string())?;
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
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
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
    let insecure = params.get("insecure").map(|s| s == "1").unwrap_or(false);
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
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: params.get("obfs").cloned(),
        obfs_password: params.get("obfs-password").cloned(),
        skip_cert_verify: insecure,
        group: String::new(),
        raw: format!("hysteria2://{}", rest),
    })
}

/// hysteria:// (v1)：`hysteria://host:port?protocol=...&auth=...&peer=...&upmbps=...&downmbps=...`
fn parse_hysteria(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (_, hostport) = auth
        .split_once('@')
        .unwrap_or(("", auth.as_str()));
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query("", &auth);
    let password = params
        .get("auth")
        .cloned()
        .or_else(|| params.get("auth_str").cloned());
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "hysteria".to_string(),
        password,
        uuid: None,
        cipher: None,
        sni: params.get("peer").cloned(),
        network: None,
        ws_path: None,
        flow: None,
        tls: true,
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
        raw: format!("hysteria://{}", rest),
    })
}

/// tuic://：`tuic://uuid:password@host:port?sni=...&congestion_control=...&alpn=...&udp_relay_mode=...`
fn parse_tuic(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "tuic 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    let (uuid, password) = match userinfo.split_once(':') {
        Some((u, p)) => (Some(u.to_string()), Some(p.to_string())),
        None => (Some(userinfo.to_string()), None),
    };
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "tuic".to_string(),
        password,
        uuid,
        cipher: None,
        sni: params.get("sni").cloned(),
        network: None,
        ws_path: None,
        flow: None,
        tls: true,
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
        raw: format!("tuic://{}", rest),
    })
}

/// wireguard://：`wg://public_key@host:port?private_key=...&reserved=...&mtu=...`
fn parse_wireguard(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "wireguard 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "wireguard".to_string(),
        password: params.get("private_key").cloned(),
        uuid: None,
        cipher: Some(userinfo.split('?').next().unwrap_or_default().to_string()),
        sni: None,
        network: None,
        ws_path: None,
        flow: None,
        tls: true,
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
        raw: format!("wg://{}", rest),
    })
}

/// anytls://：`anytls://password@host:port?insecure=1#name`
/// 注意：anytls 基于 TLS，insecure=1 仅跳过证书校验，不能关闭 TLS。
fn parse_anytls(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "anytls 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let params = parse_query(userinfo, &auth);
    let password = userinfo.split('?').next().unwrap_or_default().to_string();
    let insecure = params.get("insecure").map(|s| s == "1").unwrap_or(false);
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "anytls".to_string(),
        password: Some(password),
        uuid: None,
        cipher: None,
        sni: params.get("sni").cloned(),
        network: None,
        ws_path: None,
        flow: None,
        tls: true,
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: insecure,
        group: String::new(),
        raw: format!("anytls://{}", rest),
    })
}

/// socks://：`socks://user:pass@host:port`（无 fragment 时用 server 作名）
fn parse_socks(rest: &str) -> Result<SubscribeNode, String> {
    let (auth, name) = split_fragment(rest);
    let (userinfo, hostport) = auth
        .split_once('@')
        .ok_or_else(|| "socks 链接缺少 @".to_string())?;
    let (server, port) = split_host_port(hostport)?;
    let (username, password) = match userinfo.split_once(':') {
        Some((u, p)) => (Some(u.to_string()), Some(p.to_string())),
        None => (Some(userinfo.to_string()), None),
    };
    Ok(SubscribeNode {
        name: name.unwrap_or_else(|| server.clone()),
        server,
        port,
        node_type: "socks".to_string(),
        password,
        uuid: None,
        cipher: username,
        sni: None,
        network: None,
        ws_path: None,
        flow: None,
        tls: false,
        reality_pbk: None,
        reality_sid: None,
        client_fingerprint: None,
        obfs: None,
        obfs_password: None,
        skip_cert_verify: false,
        group: String::new(),
        raw: format!("socks://{}", rest),
    })
}

/// 拆分 fragment（#名称），返回 (去除 fragment 的主体, Option<名称>)
/// 名称做 percent-decode（订阅中节点名为 URL 编码的中文，如 %E9%A6%99%E6%B8%AF → 香港）。
fn split_fragment(s: &str) -> (String, Option<String>) {
    match s.split_once('#') {
        Some((head, frag)) => (
            head.to_string(),
            Some(percent_decode(frag.trim()).to_string()),
        ),
        None => (s.to_string(), None),
    }
}

/// percent-decode：%XX → 字节，+ → 空格（application/x-www-form-urlencoded）。
/// 用于解码订阅链接 fragment / vmess ps 中的 URL 编码节点名。
fn percent_decode(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            if let (Some(h), Some(l)) = (hex_val(bytes[i + 1]), hex_val(bytes[i + 2])) {
                out.push(h * 16 + l);
                i += 3;
                continue;
            }
        }
        out.push(if bytes[i] == b'+' { b' ' } else { bytes[i] });
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn hex_val(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

/// 拆分 host:port，支持 IPv6 字面量 `[2001:db8::1]:443` 与 IPv4/域名。
/// 端口后可能带 query（`host:port?k=v`）。
fn split_host_port(hostport: &str) -> Result<(String, u16), String> {
    let (host, port) = if let Some(rest) = hostport.strip_prefix('[') {
        match rest.split_once(']') {
            Some((ipv6, tail)) => {
                let port = tail
                    .strip_prefix(':')
                    .ok_or_else(|| format!("IPv6 链接缺少端口: {}", hostport))?;
                (ipv6.to_string(), port.to_string())
            }
            None => return Err(format!("IPv6 地址缺少 ]: {}", hostport)),
        }
    } else {
        let (h, p) = hostport
            .split_once(':')
            .ok_or_else(|| format!("链接缺少端口: {}", hostport))?;
        (h.to_string(), p.to_string())
    };
    let port = port
        .split('?')
        .next()
        .unwrap_or_default()
        .parse::<u16>()
        .map_err(|_| format!("端口无效: {}", port))?;
    Ok((host, port))
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
        skip_cert_verify: n.skip_cert_verify.then_some(true),
        network: n.network.clone(),
        up: None,
        down: None,
        obfs: n.obfs.clone(),
        obfs_password: n.obfs_password.clone(),
        ws_opts: n.ws_path.as_ref().map(|p| crate::clash_yaml::WsOpts {
            path: Some(p.clone()),
            headers: None,
        }),
        ws_headers: None,
        client_fingerprint: n.client_fingerprint.clone(),
        flow: n.flow.clone(),
        reality_opts: (n.reality_pbk.is_some() || n.reality_sid.is_some()).then(|| {
            crate::clash_yaml::RealityOpts {
                public_key: n.reality_pbk.clone(),
                short_id: n.reality_sid.clone(),
            }
        }),
        alpn: None,
        private_key: n.password.clone(),
        public_key: n.cipher.clone(),
        auth_str: n.password.clone(),
        group: n.group.clone(),
    }
}

/// 仅拉取并缓存订阅节点（不创建实例），返回本次拉取的节点数。
/// 节点池页「从订阅导入」使用：节点进入订阅缓存，随后用户可在节点池页
/// 勾选并按「独享 / 进池」批量添加为实例。
/// 节点按订阅名分组（Content-Disposition 订阅名；无名称时用 URL 末段；
/// 都没有则 "订阅N"，N 为已有分组序号）。
/// 多次导入**合并**到缓存：新订阅追加，同订阅同节点（name+server+port）替换，
/// 不会顶掉已导入的其他订阅。
pub fn import_subscription_pool(url: &str) -> Result<usize, String> {
    let (mut nodes, meta) = fetch_subscription_with_meta(url)?;
    if nodes.is_empty() {
        return Err("订阅中未解析到任何节点".to_string());
    }
    let group = subscription_group_name(url, meta.name.as_deref());
    for node in nodes.iter_mut() {
        node.group = group.clone();
    }
    let count = nodes.len();
    merge_subscription_cache(&nodes)?;
    Ok(count)
}

/// 合并订阅节点到缓存：按身份（name+server+port）去重，
/// 已存在则原位替换（同订阅刷新），不存在则追加（新订阅），保留既有订阅。
fn merge_subscription_cache(nodes: &[SubscribeNode]) -> Result<(), String> {
    let mut all = load_subscription_cache();
    let mut index: std::collections::HashMap<String, usize> = all
        .iter()
        .enumerate()
        .map(|(i, n)| (node_identity(n), i))
        .collect();
    for node in nodes {
        let id = node_identity(node);
        match index.get(&id) {
            Some(&i) => all[i] = node.clone(),
            None => {
                index.insert(id, all.len());
                all.push(node.clone());
            }
        }
    }
    save_subscription_cache(&all)
}

/// 节点身份键：name+server+port（跨订阅同名不同服务器视为不同节点）
fn node_identity(n: &SubscribeNode) -> String {
    format!("{}|{}|{}", n.name, n.server, n.port)
}

/// 确定订阅分组名：响应头订阅名 > URL 路径末段 > "订阅N"。
/// URL 末段若是纯 hash（32+ 位十六进制，订阅 token 常见）则不作为名字。
fn subscription_group_name(url: &str, header_name: Option<&str>) -> String {
    if let Some(name) = header_name.filter(|n| !n.trim().is_empty()) {
        return name.trim().to_string();
    }
    // URL 末段：去掉路径分隔符与查询串，作为候选名
    let last = url
        .split('?')
        .next()
        .unwrap_or(url)
        .trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or("")
        .trim();
    let looks_like_hash = last.len() >= 24
        && last
            .chars()
            .all(|c| c.is_ascii_hexdigit() || c == '-' || c == '_');
    if !last.is_empty() && !looks_like_hash {
        return last.to_string();
    }
    // fallback：按当前缓存已有分组数编号
    let existing = load_subscription_cache();
    let max = existing
        .iter()
        .filter_map(|n| {
            n.group
                .strip_prefix("订阅")
                .and_then(|s| s.parse::<u32>().ok())
        })
        .max()
        .unwrap_or(0);
    format!("订阅{}", max + 1)
}

/// 批量导入订阅节点为实例（含持久化订阅缓存）。
/// `join_gateway` 为 true 时导入的实例打上入池标记（不自动启动，启停由实例池页控制）。
pub fn import_subscription(
    core: &crate::core::AppCore,
    url: &str,
    join_gateway: bool,
) -> Result<usize, String> {
    let (mut nodes, meta) = fetch_subscription_with_meta(url)?;
    if nodes.is_empty() {
        return Err("订阅中未解析到任何节点".to_string());
    }
    let group = subscription_group_name(url, meta.name.as_deref());
    for node in nodes.iter_mut() {
        node.group = group.clone();
    }
    merge_subscription_cache(&nodes)?;

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
    fn test_percent_decode_fragment_name() {
        // 节点名为 URL 编码中文（真实机场订阅格式）
        let body = "vless://uuid1@hk.example.com:443?security=reality&sni=s.example.com#%E9%A6%99%E6%B8%AF+02";
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].name, "香港 02");
        // + 号解码为空格
        assert_eq!(
            percent_decode("%E6%9B%B4%E6%96%B0%E6%97%B6%E9%97%B4%EF%BC%9A08-09+16%3A29"),
            "更新时间：08-09 16:29"
        );
        // emoji 多字节
        assert_eq!(
            percent_decode("%F0%9F%87%AD%F0%9F%87%B0%E9%A6%99%E6%B8%AF+02"),
            "🇭🇰香港 02"
        );
    }

    #[test]
    fn test_parse_vmess_percent_decoded_name() {
        let payload = r#"{"v":"2","ps":"%E9%A6%99%E6%B8%AF+01","add":"hk.example.com","port":"443","id":"uuid1","net":"tcp","tls":"tls"}"#;
        let body = format!("vmess://{}", BASE64.encode(payload));
        let nodes = parse_subscription(&body).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].name, "香港 01");
    }

    #[test]
    fn test_parse_subscription_filters_banner_pseudo_nodes() {
        // 真实机场订阅格式：公告行伪装成 vless 链接（同一占位服务器），节点名为 URL 编码
        let plain = [
            "vless://uuid1@hk.xlz.claw-api.xyz:443?security=reality&sni=support.example.com#%E5%AE%98%E7%BD%91%EF%BC%9Axuelian.pro",
            "vless://uuid1@hk.xlz.claw-api.xyz:443?security=reality&sni=support.example.com#%E6%9B%B4%E6%96%B0%E6%97%B6%E9%97%B4%EF%BC%9A08-09+16%3A29",
            "vless://uuid1@hk.xlz.claw-api.xyz:443?security=reality&sni=support.example.com#%E5%89%A9%E4%BD%99%E6%97%B6%E9%95%BF%EF%BC%9A40%E5%B0%8F%E6%97%B621%E5%88%86%E9%92%9F",
            "vless://uuid1@hk.xlz.claw-api.xyz:443?security=reality&sni=support.example.com#%F0%9F%87%AD%F0%9F%87%B0%E9%A6%99%E6%B8%AF+02",
            "vless://uuid1@sg.z.claw-api.xyz:8080?security=reality&sni=api.example.com#%F0%9F%87%B8%F0%9F%87%AC%E6%96%B0%E5%8A%A0%E5%9D%A1+03",
        ]
        .join("\n");
        let encoded = BASE64.encode(plain);
        let nodes = parse_subscription(&encoded).unwrap();
        // 3 条公告伪节点被过滤，剩 2 个真实节点且名称已解码
        assert_eq!(nodes.len(), 2);
        assert_eq!(nodes[0].name, "🇭🇰香港 02");
        assert_eq!(nodes[0].server, "hk.xlz.claw-api.xyz");
        assert_eq!(nodes[1].name, "🇸🇬新加坡 03");
        assert_eq!(nodes[1].server, "sg.z.claw-api.xyz");
    }

    #[test]
    fn test_parse_subscription_only_banners_errors() {
        // 纯公告伪节点 → 无真实节点 → 报错
        let plain = "vless://uuid1@hk.example.com:443#%E5%AE%98%E7%BD%91%EF%BC%9Axuelian.pro\nvless://uuid1@hk.example.com:443#%E6%9B%B4%E6%96%B0%E6%97%B6%E9%97%B4%EF%BC%9A08-09+16%3A29";
        let err = parse_subscription(&plain).unwrap_err();
        assert!(err.contains("未解析到任何可用节点"));
    }

    #[test]
    fn test_decode_base64_loose_variants() {
        // 1. 无 padding 的 RawStd（mihomo 先试 NOPAD）
        let raw = "dmxlc3M6Ly91dWlkMUBhLmV4YW1wbGUuY29tOjQ0MyNB";
        let decoded = decode_base64_loose(raw).unwrap();
        assert!(decoded.starts_with("vless://"));
        // 2. URL-safe 变体（_ - 替换）：真实含 + / 的 base64 经 URL-safe 编码
        let url_safe = "dmxlc3M6Ly9BQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWmFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6MDEyMzQ1Njc4OSsvQG5vZGUyLmV4YW1wbGUuY29tOjg0NDMjTm9kZS8wMg";
        let decoded2 = decode_base64_loose(url_safe).unwrap();
        assert!(decoded2.starts_with("vless://"));
        // 3. 含换行/空白的 base64（v2rayN 去空白）
        let messy = "dmxlc3M6Ly91\ndWlkMUBhLmV4YW1w\nbGUuY29tOjQ0MyNB";
        let decoded3 = decode_base64_loose(messy).unwrap();
        assert!(decoded3.starts_with("vless://"));
        // 4. 非 base64 明文 → None（降级明文解析）
        assert!(decode_base64_loose("vless://uuid1@a.example.com:443#A").is_none());
        // 5. 可解码但内容不是订阅（无 ://）→ 整体订阅层降级为明文，解析失败而非误解析
        let not_sub = base64::engine::general_purpose::STANDARD.encode("Hello World");
        let err = parse_subscription(&not_sub).unwrap_err();
        assert!(err.contains("未解析到任何可用节点"));
    }

    #[test]
    fn test_parse_subscription_urlsafe_base64() {
        // URL-safe base64 编码的订阅（真实机场可能用 - _ 变体）
        let plain = "vless://uuid1@a.example.com:443?security=tls#NodeA";
        let encoded = BASE64_URLSAFE_NOPAD.encode(plain);
        let nodes = parse_subscription(&encoded).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].name, "NodeA");
    }

    #[test]
    fn test_parse_subscription_plaintext_lines_unchanged() {
        // 明文多行 URI 订阅不受影响
        let plain = "vless://uuid1@a.example.com:443#A\ntrojan://pw@b.example.com:443#B";
        let nodes = parse_subscription(plain).unwrap();
        assert_eq!(nodes.len(), 2);
        assert_eq!(nodes[0].name, "A");
        assert_eq!(nodes[1].name, "B");
    }

    #[test]
    fn test_parse_ipv6_host_port() {
        let body = "vless://uuid1@[2001:db8::1]:443?security=tls#IPv6Node";
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].server, "2001:db8::1");
        assert_eq!(nodes[0].port, 443);
        assert_eq!(nodes[0].name, "IPv6Node");
        // IPv6 + query 混合
        let body2 = "vless://uuid1@[2408:8000::2]:8443?security=reality&sni=s.example.com#NodeV6";
        let nodes2 = parse_subscription(body2).unwrap();
        assert_eq!(nodes2[0].server, "2408:8000::2");
        assert_eq!(nodes2[0].port, 8443);
        assert_eq!(nodes2[0].sni.as_deref(), Some("s.example.com"));
    }

    #[test]
    fn test_parse_vmess_urlsafe_base64() {
        // vmess JSON 用 URL-safe base64 编码（v2rayN 变体）
        let payload = r#"{"v":"2","ps":"NodeV","add":"hk.example.com","port":"443","id":"uuid1","net":"tcp","tls":"tls"}"#;
        let body = format!("vmess://{}", BASE64_URLSAFE_NOPAD.encode(payload));
        let nodes = parse_subscription(&body).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].name, "NodeV");
        assert_eq!(nodes[0].server, "hk.example.com");
    }

    #[test]
    fn test_parse_ss_legacy_and_sip002() {
        // legacy：ss://base64(method:password@server:port)#name
        let legacy = format!("ss://{}#LegacySS", BASE64.encode("aes-256-gcm:pw@ss.example.com:8388"));
        let nodes = parse_subscription(&legacy).unwrap();
        let n = &nodes[0];
        assert_eq!(n.node_type, "ss");
        assert_eq!(n.server, "ss.example.com");
        assert_eq!(n.port, 8388);
        assert_eq!(n.cipher.as_deref(), Some("aes-256-gcm"));
        assert_eq!(n.password.as_deref(), Some("pw"));
        assert_eq!(n.name, "LegacySS");
        // sip002：ss://base64(method:password)@server:port
        let sip002 = format!("ss://{}@ss.example.com:8388#SipSS", BASE64.encode("aes-256-gcm:pw"));
        let nodes2 = parse_subscription(&sip002).unwrap();
        assert_eq!(nodes2[0].server, "ss.example.com");
        assert_eq!(nodes2[0].password.as_deref(), Some("pw"));
        assert_eq!(nodes2[0].name, "SipSS");
    }

    #[test]
    fn test_dedupe_node_names() {
        // 重名 → -02/-03 后缀（mihomo uniqueName 语义）
        let mut nodes = vec![
            mk_node("A"),
            mk_node("B"),
            mk_node("A"),
            mk_node("A"),
        ];
        dedupe_node_names(&mut nodes);
        let names: Vec<&str> = nodes.iter().map(|n| n.name.as_str()).collect();
        assert_eq!(names, vec!["A", "B", "A-02", "A-03"]);
    }

    #[test]
    fn test_parse_content_disposition_name() {
        // filename*= percent-decode 优先
        let raw1 = r#"attachment; filename*=UTF-8''%E9%A6%99%E6%B8%AF%E8%8A%82%E7%82%B9.txt"#;
        assert_eq!(parse_content_disposition_name(raw1).as_deref(), Some("香港节点.txt"));
        // filename= 退化
        let raw2 = r#"attachment; filename="sub.yaml""#;
        assert_eq!(parse_content_disposition_name(raw2).as_deref(), Some("sub.yaml"));
        // 无 filename
        assert_eq!(parse_content_disposition_name("attachment"), None);
    }

    fn mk_node(name: &str) -> SubscribeNode {
        SubscribeNode {
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
            reality_pbk: None,
            reality_sid: None,
            client_fingerprint: None,
            obfs: None,
            obfs_password: None,
            skip_cert_verify: false,
            group: String::new(),
            raw: format!("trojan://pw@1.2.3.4:443#{}", name),
        }
    }

    #[test]
    fn test_parse_singbox_json() {
        let body = r#"{"outbounds":[
            {"type":"vless","tag":"HK-01","server":"hk.example.com","server_port":443,
             "uuid":"11111111-2222-3333-4444-555555555555",
             "tls":{"enabled":true,"server_name":"cdn.example.com","reality":{"enabled":true,"public_key":"abc123","short_id":"deadbeef"}}},
            {"type":"trojan","tag":"SG-02","server":"sg.example.com","server_port":443,"password":"pw123"},
            {"type":"shadowsocks","tag":"SS-03","server":"ss.example.com","server_port":8388,"method":"aes-256-gcm","password":"sspw"},
            {"type":"hysteria2","tag":"HY2-04","server":"hy2.example.com","server_port":8443,"password":"hype"}
        ]}"#;
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 4);
        assert_eq!(nodes[0].name, "HK-01");
        assert_eq!(nodes[0].node_type, "vless");
        assert_eq!(nodes[0].server, "hk.example.com");
        assert_eq!(nodes[0].port, 443);
        assert_eq!(nodes[0].uuid.as_deref(), Some("11111111-2222-3333-4444-555555555555"));
        assert_eq!(nodes[0].sni.as_deref(), Some("cdn.example.com"));
        assert!(nodes[0].tls);
        assert_eq!(nodes[1].node_type, "trojan");
        assert_eq!(nodes[1].password.as_deref(), Some("pw123"));
        assert_eq!(nodes[2].node_type, "shadowsocks");
        assert_eq!(nodes[2].cipher.as_deref(), Some("aes-256-gcm"));
        assert_eq!(nodes[3].node_type, "hysteria2");
        assert_eq!(nodes[3].password.as_deref(), Some("hype"));
    }

    #[test]
    fn test_parse_singbox_json_base64_wrapped() {
        let body = r#"{"outbounds":[{"type":"vless","tag":"HK-01","server":"hk.example.com","server_port":443,"uuid":"11111111-2222-3333-4444-555555555555"}]}"#;
        let encoded = BASE64.encode(body);
        let nodes = parse_subscription(&encoded).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].name, "HK-01");
        assert_eq!(nodes[0].node_type, "vless");
    }

    #[test]
    fn test_parse_singbox_json_tag_percent_decoded() {
        let body = r#"{"outbounds":[{"type":"vless","tag":"%E9%A6%99%E6%B8%AF+01","server":"hk.example.com","server_port":443,"uuid":"11111111-2222-3333-4444-555555555555"}]}"#;
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes[0].name, "香港 01");
    }

    #[test]
    fn test_parse_tuic_link() {
        let body = "tuic://uuid1:pass123@tuic.example.com:443?sni=tuic.example.com&congestion_control=bbr#TUIC-01";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.node_type, "tuic");
        assert_eq!(n.server, "tuic.example.com");
        assert_eq!(n.port, 443);
        assert_eq!(n.uuid.as_deref(), Some("uuid1"));
        assert_eq!(n.password.as_deref(), Some("pass123"));
        assert_eq!(n.sni.as_deref(), Some("tuic.example.com"));
        assert_eq!(n.name, "TUIC-01");
    }

    #[test]
    fn test_parse_wireguard_link() {
        let body = "wg://PUBKEY123@wg.example.com:51820?private_key=PRIVKEY&mtu=1420#WG-01";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.node_type, "wireguard");
        assert_eq!(n.server, "wg.example.com");
        assert_eq!(n.port, 51820);
        assert_eq!(n.password.as_deref(), Some("PRIVKEY"));
        assert_eq!(n.name, "WG-01");
    }

    #[test]
    fn test_parse_hysteria_v1_link() {
        let body = "hysteria://hy1.example.com:36712?protocol=udp&auth=secretauth&peer=hy1.example.com&upmbps=100&downmbps=200#HY1-01";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.node_type, "hysteria");
        assert_eq!(n.server, "hy1.example.com");
        assert_eq!(n.port, 36712);
        assert_eq!(n.password.as_deref(), Some("secretauth"));
        assert_eq!(n.sni.as_deref(), Some("hy1.example.com"));
        assert_eq!(n.name, "HY1-01");
    }

    #[test]
    fn test_parse_socks_link() {
        let body = "socks://user1:pass1@socks.example.com:1080#SOCKS-01";
        let nodes = parse_subscription(body).unwrap();
        let n = &nodes[0];
        assert_eq!(n.node_type, "socks");
        assert_eq!(n.server, "socks.example.com");
        assert_eq!(n.port, 1080);
        assert_eq!(n.password.as_deref(), Some("pass1"));
        assert_eq!(n.cipher.as_deref(), Some("user1"));
        assert_eq!(n.name, "SOCKS-01");
    }

    #[test]
    fn test_parse_real_subscription_anytls_and_obfs_hysteria2() {
        // 真实订阅格式：anytls 节点 + 带 obfs 的 hysteria2 节点 + [anytls] 公告伪节点
        let plain = [
            "anytls://a5b309a5-d952-4fa2-9630-901ffeb1f429@hklumen.094180.xyz:9999?insecure=1#%5Banytls%5D%E5%89%A9%E4%BD%99%E6%B5%81%E9%87%8F%EF%BC%9A49.82%20GB",
            "anytls://a5b309a5-d952-4fa2-9630-901ffeb1f429@hklumen.094180.xyz:9999?insecure=1#%5Banytls%5D%E5%A5%97%E9%A4%90%E5%88%B0%E6%9C%9F%EF%BC%9A2026-09-04",
            "anytls://a5b309a5-d952-4fa2-9630-901ffeb1f429@hklumen.094180.xyz:9999?insecure=1#%5Banytls%5D%F0%9F%87%AD%F0%9F%87%B0%20%5BLUMEN%5D%20%E9%A6%99%E6%B8%AF%E7%A7%BB%E5%8A%A8%E4%B8%93%E7%BA%BF",
            "hysteria2://a5b309a5-d952-4fa2-9630-901ffeb1f429@bagesg2.ravenhash.org:21353?insecure=1&obfs=salamander&obfs-password=C2kU7kDk0iMVMWHo#%5BHy2%5D%F0%9F%87%B8%F0%9F%87%AC%20%E6%96%B0%E5%8A%A0%E5%9D%A1BGP2",
            "hysteria2://a5b309a5-d952-4fa2-9630-901ffeb1f429@jpali.fuuny.org:25565?insecure=1#%5BHy2%5D%F0%9F%87%AF%F0%9F%87%B5%20%E6%97%A5%E6%9C%AC%E4%B8%89%E7%BD%91%E4%B8%93%E7%BA%BF",
        ]
        .join("\n");
        let encoded = BASE64.encode(plain);
        let nodes = parse_subscription(&encoded).unwrap();
        // 2 条 [anytls] 公告伪节点被过滤，剩 3 个真实节点
        assert_eq!(nodes.len(), 3);
        // anytls 节点：基于 TLS，insecure=1 只跳过证书校验，tls 必须为 true
        let anytls = nodes.iter().find(|n| n.node_type == "anytls").unwrap();
        assert_eq!(anytls.name, "[anytls]🇭🇰 [LUMEN] 香港移动专线");
        assert_eq!(anytls.server, "hklumen.094180.xyz");
        assert_eq!(anytls.port, 9999);
        assert_eq!(anytls.password.as_deref(), Some("a5b309a5-d952-4fa2-9630-901ffeb1f429"));
        assert!(anytls.tls);
        assert!(anytls.skip_cert_verify);
        // anytls 生成配置：tls.enabled=true + insecure=true（否则 sing-box 报 TLS required）
        let cn = to_clash_node(anytls);
        let config = crate::singbox::build_singbox_config(&cn, 7899).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        let ob = &v["outbounds"][0];
        assert_eq!(ob["type"], "anytls");
        assert_eq!(ob["tls"]["enabled"], true);
        assert_eq!(ob["tls"]["insecure"], true);
        // obfs hysteria2 节点：obfs 字段必须解析
        let hy2_obfs = nodes.iter().find(|n| n.server == "bagesg2.ravenhash.org").unwrap();
        assert_eq!(hy2_obfs.node_type, "hysteria2");
        assert_eq!(hy2_obfs.obfs.as_deref(), Some("salamander"));
        assert_eq!(hy2_obfs.obfs_password.as_deref(), Some("C2kU7kDk0iMVMWHo"));
        assert!(hy2_obfs.skip_cert_verify);
        // 普通 hysteria2
        let hy2_plain = nodes.iter().find(|n| n.server == "jpali.fuuny.org").unwrap();
        assert_eq!(hy2_plain.obfs, None);
        // 端到端：obfs 必须进入 sing-box 配置
        let cn = to_clash_node(hy2_obfs);
        let config = crate::singbox::build_singbox_config(&cn, 7899).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        let ob = &v["outbounds"][0];
        assert_eq!(ob["type"], "hysteria2");
        assert_eq!(ob["obfs"]["type"], "salamander");
        assert_eq!(ob["obfs"]["password"], "C2kU7kDk0iMVMWHo");
        assert_eq!(ob["tls"]["insecure"], true);
    }

    #[test]
    fn test_singbox_json_to_singbox_config_end_to_end() {
        // sing-box JSON 订阅 → SubscribeNode → ClashNode → sing-box outbound 完整链路
        let body = r#"{"outbounds":[
            {"type":"vless","tag":"HK-01","server":"hk.example.com","server_port":443,
             "uuid":"11111111-2222-3333-4444-555555555555",
             "tls":{"enabled":true,"server_name":"cdn.example.com","reality":{"enabled":true,"public_key":"abc123","short_id":"deadbeef"}}},
            {"type":"trojan","tag":"SG-02","server":"sg.example.com","server_port":443,"password":"pw123"}
        ]}"#;
        let nodes = parse_subscription(body).unwrap();
        assert_eq!(nodes.len(), 2);
        // 转 ClashNode 再生成 sing-box outbound
        for n in &nodes {
            let cn = to_clash_node(n);
            let config = crate::singbox::build_singbox_config(&cn, 7899).unwrap();
            let v: serde_json::Value = serde_json::from_str(&config).unwrap();
            let ob = &v["outbounds"][0];
            assert_eq!(ob["type"], n.node_type);
            assert_eq!(ob["server"], n.server);
            assert_eq!(ob["server_port"], n.port);
        }
        // vless reality 参数透传
        let cn = to_clash_node(&nodes[0]);
        let config = crate::singbox::build_singbox_config(&cn, 7899).unwrap();
        let v: serde_json::Value = serde_json::from_str(&config).unwrap();
        let ob = &v["outbounds"][0];
        assert_eq!(ob["tls"]["reality"]["public_key"], "abc123");
        assert_eq!(ob["tls"]["reality"]["short_id"], "deadbeef");
        assert_eq!(ob["tls"]["server_name"], "cdn.example.com");
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
            reality_pbk: None,
            reality_sid: None,
            client_fingerprint: None,
            obfs: None,
            obfs_password: None,
            skip_cert_verify: false,
            group: String::new(),
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
            reality_pbk: None,
            reality_sid: None,
            client_fingerprint: None,
            obfs: None,
            obfs_password: None,
            skip_cert_verify: false,
            group: String::new(),
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




    #[test]
    fn test_subscription_group_name_priority() {
        // 1. 响应头订阅名优先
        let name = subscription_group_name("https://example.com/sub", Some("雪莲Pro"));
        assert_eq!(name, "雪莲Pro");
        // 2. 无头名 → URL 末段（非 hash 时可用）
        let name2 = subscription_group_name("https://example.com/NekoCatNetWork", None);
        assert_eq!(name2, "NekoCatNetWork");
        // 3. 无头名且 URL 末段是 hash → 订阅N（N 递增）
        let name3 = subscription_group_name("https://subscribe.456123987.xyz/file/57c3229160ac34b9a4d98344745a404d", None);
        assert!(name3.starts_with("订阅"));
    }

    #[test]
    fn test_import_subscription_pool_sets_group() {
        // 触碰进程级 OPCODE2API_DATA_DIR，与 config 测试持同一串行锁
        let _guard = crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let orig = std::env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = std::env::temp_dir().join("opencode2api-manager-group-test");
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let _ = std::fs::remove_dir_all(&test_dir);

        // 直接构造带组名的缓存验证 list_nodes_with_group 分组
        let mut n = mk_node("NodeA");
        n.group = "雪莲Pro".to_string();
        save_subscription_cache(&[n]).unwrap();
        let nodes = crate::clash_yaml::list_nodes_with_group().unwrap();
        let sub_nodes: Vec<_> = nodes.iter().filter(|x| x.group == "雪莲Pro").collect();
        assert_eq!(sub_nodes.len(), 1);
        assert_eq!(sub_nodes[0].name, "NodeA");

        let _ = std::fs::remove_dir_all(&test_dir);
        match orig {
            Some(v) => unsafe { std::env::set_var("OPCODE2API_DATA_DIR", v) },
            None => unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") },
        }
    }

    #[test]
    fn test_merge_subscription_cache_no_override() {
        // 触碰进程级 OPCODE2API_DATA_DIR，与 config 测试持同一串行锁
        let _guard = crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let orig = std::env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = std::env::temp_dir().join("opencode2api-manager-merge-test");
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };
        let _ = std::fs::remove_dir_all(&test_dir);

        // 订阅1：两个节点
        let mut n1 = mk_node("Sub1-NodeA");
        n1.group = "订阅1".to_string();
        n1.server = "1.2.3.4".to_string();
        let mut n2 = mk_node("Sub1-NodeB");
        n2.group = "订阅1".to_string();
        n2.server = "1.2.3.5".to_string();
        merge_subscription_cache(&[n1, n2]).unwrap();

        // 订阅2：一个节点，同 server 不同名
        let mut n3 = mk_node("Sub2-NodeA");
        n3.group = "订阅2".to_string();
        n3.server = "2.2.2.2".to_string();
        merge_subscription_cache(&[n3]).unwrap();

        // 两订阅都在，共 3 节点
        let all = load_subscription_cache();
        assert_eq!(all.len(), 3);
        let groups: std::collections::HashSet<&str> =
            all.iter().map(|n| n.group.as_str()).collect();
        assert!(groups.contains("订阅1"));
        assert!(groups.contains("订阅2"));

        // 同订阅刷新：同身份节点替换（不增数量）
        let mut n1b = mk_node("Sub1-NodeA");
        n1b.group = "订阅1".to_string();
        n1b.server = "1.2.3.4".to_string();
        n1b.port = 444; // 改端口 → 身份变化 → 追加
        merge_subscription_cache(&[n1b]).unwrap();
        assert_eq!(load_subscription_cache().len(), 4);

        let _ = std::fs::remove_dir_all(&test_dir);
        match orig {
            Some(v) => unsafe { std::env::set_var("OPCODE2API_DATA_DIR", v) },
            None => unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") },
        }
    }

}
