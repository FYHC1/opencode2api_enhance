use crate::clash_yaml;
use crate::config::Config;
use crate::instance::{Instance, InstanceManager, InstanceStatus};
use crate::probe::{
    ScanController, DEFAULT_PROBE_API_PORT, DEFAULT_PROBE_SOCKS_PORT,
};
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::{Html, IntoResponse, Response};
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tower_http::cors::CorsLayer;

const INDEX_HTML: &str = include_str!("../static/index.html");

#[derive(Clone)]
pub struct AppState {
    pub manager: Arc<Mutex<InstanceManager>>,
    pub scan: Arc<ScanController>,
}

#[derive(Debug, Deserialize)]
pub struct AddInstanceBody {
    pub name: String,
    pub port: u16,
    pub node: String,
}

#[derive(Debug, Deserialize)]
pub struct BatchAddBody {
    /// 要添加的节点名列表；也可传完整对象
    pub nodes: Vec<BatchAddItem>,
    /// 起始端口，默认 18100；每个节点 port = base + index
    pub base_port: Option<u16>,
    /// 实例名前缀，默认 node；最终 name = prefix + 序号 或用节点名 sanitize
    pub name_prefix: Option<String>,
    /// 是否用节点名作为实例名（可能含中文）
    pub use_node_name: Option<bool>,
}

#[derive(Debug, Deserialize)]
pub struct BatchAddItem {
    pub node: String,
    pub name: Option<String>,
    pub port: Option<u16>,
}

#[derive(Debug, Deserialize)]
pub struct BatchOpBody {
    pub names: Vec<String>,
}

#[derive(Debug, Deserialize)]
pub struct ScanStartBody {
    /// 可选：只扫这些节点名；空则扫全部含凭据节点
    pub nodes: Option<Vec<String>>,
    pub api_port: Option<u16>,
    pub socks_port: Option<u16>,
    /// 单节点超时秒
    pub timeout: Option<u64>,
}

#[derive(Debug, Serialize)]
pub struct NodeView {
    pub name: String,
    pub node_type: String,
    pub server: String,
    pub port: u16,
    pub has_cred: bool,
    pub group: String,
}

fn manager_paths() -> (PathBuf, PathBuf, PathBuf) {
    let config_dir = Config::config_dir();
    let instances_path = config_dir.join("instances.json");
    let binary_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."))
        .join("bin");
    let runtime_dir = config_dir.join("runtime");
    (instances_path, binary_dir, runtime_dir)
}

pub fn create_manager() -> InstanceManager {
    let (instances_path, binary_dir, runtime_dir) = manager_paths();
    let mut manager = InstanceManager::new(instances_path, binary_dir, runtime_dir);
    let _ = manager.load();
    manager
}

fn json_error(status: StatusCode, msg: impl ToString) -> Response {
    let body = Json(json!({ "ok": false, "error": msg.to_string() }));
    (status, body).into_response()
}

fn json_ok(value: serde_json::Value) -> Response {
    Json(json!({ "ok": true, "data": value })).into_response()
}

fn default_password() -> String {
    let password = Config::load()
        .unwrap_or_default()
        .default_password
        .clone();
    if password.is_empty() {
        "123456".to_string()
    } else {
        password
    }
}

async fn index() -> Html<&'static str> {
    Html(INDEX_HTML)
}

async fn api_nodes() -> Response {
    match clash_yaml::list_nodes_with_group() {
        Ok(nodes) => {
            let views: Vec<NodeView> = nodes
                .into_iter()
                .map(|n| NodeView {
                    has_cred: n.password.is_some() || n.uuid.is_some(),
                    name: n.name,
                    node_type: n.node_type,
                    server: n.server,
                    port: n.port,
                    group: n.group,
                })
                .collect();
            json_ok(json!(views))
        }
        Err(e) => json_error(StatusCode::INTERNAL_SERVER_ERROR, e.to_string()),
    }
}

async fn api_list_instances(State(state): State<AppState>) -> Response {
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    let _ = mgr.load();
    let list: Vec<Instance> = mgr.list_instances().to_vec();
    json_ok(json!(list))
}

async fn api_add_instance(
    State(state): State<AppState>,
    Json(body): Json<AddInstanceBody>,
) -> Response {
    if body.node.trim().is_empty() {
        return json_error(StatusCode::BAD_REQUEST, "节点不能为空");
    }
    if body.port < 1024 {
        return json_error(StatusCode::BAD_REQUEST, "端口需 >= 1024");
    }
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    let name = if body.name.trim().is_empty() {
        next_auto_name(&mgr)
    } else {
        body.name.trim().to_string()
    };
    match mgr.add_instance(name.clone(), body.port, body.node.trim().to_string()) {
        Ok(()) => json_ok(json!({ "name": name, "port": body.port })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

/// 生成不重复的实例名：实例1、实例2…
fn next_auto_name(mgr: &InstanceManager) -> String {
    let used: std::collections::HashSet<String> = mgr
        .list_instances()
        .iter()
        .map(|i| i.name.clone())
        .collect();
    let mut n = 1u32;
    loop {
        let candidate = format!("实例{}", n);
        if !used.contains(&candidate) {
            return candidate;
        }
        n += 1;
    }
}

fn is_port_used(mgr: &InstanceManager, port: u16) -> bool {
    if mgr.list_instances().iter().any(|i| i.port == port) {
        return true;
    }
    // 本地 TCP 是否被监听
    crate::instance::wait_for_port(port, Duration::from_millis(300))
}

/// 建议一个可用端口（>10000，未被实例占用，本地未监听）
async fn api_port_suggest(State(state): State<AppState>) -> Response {
    let mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    let mut rng = rand_seed();
    let start = 18100 + (rng % 20000) as u16;
    for _ in 0..200 {
        let port = start.saturating_add(1 + (rng % 200) as u16) % 30000 + 10000;
        if !is_port_used(&mgr, port) {
            return json_ok(json!({ "port": port }));
        }
        rng = rng.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
    }
    json_error(StatusCode::INTERNAL_SERVER_ERROR, "未找到可用端口")
}

fn rand_seed() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0x1234_5678)
}

#[derive(Debug, Deserialize)]
pub struct PortCheckBody {
    pub port: u16,
}

async fn api_port_check(
    State(state): State<AppState>,
    Json(body): Json<PortCheckBody>,
) -> Response {
    if body.port < 1024 {
        return json_error(StatusCode::BAD_REQUEST, "端口需 >= 1024");
    }
    let mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    let used_by_instance = mgr.list_instances().iter().any(|i| i.port == body.port);
    if used_by_instance {
        return json_ok(json!({ "available": false, "reason": "已被实例占用" }));
    }
    if crate::instance::wait_for_port(body.port, Duration::from_millis(300)) {
        return json_ok(json!({ "available": false, "reason": "端口已被本机程序监听" }));
    }
    json_ok(json!({ "available": true, "reason": "端口可用" }))
}

async fn api_batch_add(
    State(state): State<AppState>,
    Json(body): Json<BatchAddBody>,
) -> Response {
    if body.nodes.is_empty() {
        return json_error(StatusCode::BAD_REQUEST, "nodes 不能为空");
    }
    let base_port = body.base_port.unwrap_or(18100);
    let use_node_name = body.use_node_name.unwrap_or(true);
    let prefix = body
        .name_prefix
        .unwrap_or_else(|| "n".to_string());

    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    let _ = mgr.load();

    let mut added = Vec::new();
    let mut errors = Vec::new();

    for (i, item) in body.nodes.iter().enumerate() {
        let node = item.node.trim();
        if node.is_empty() {
            errors.push(json!({ "node": "", "error": "空节点名" }));
            continue;
        }
        let port = item.port.unwrap_or(base_port.saturating_add(i as u16));
        let name = item
            .name
            .clone()
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(|| {
                if use_node_name {
                    sanitize_instance_name(node)
                } else {
                    format!("{}{}", prefix, i + 1)
                }
            });

        // 名称冲突时自动加后缀
        let mut final_name = name.clone();
        let mut suffix = 2u32;
        while mgr.list_instances().iter().any(|x| x.name == final_name) {
            final_name = format!("{}-{}", name, suffix);
            suffix += 1;
            if suffix > 100 {
                break;
            }
        }

        // 端口冲突时递增
        let mut final_port = port;
        let mut tries = 0u16;
        while mgr.list_instances().iter().any(|x| x.port == final_port) {
            final_port = final_port.saturating_add(1);
            tries += 1;
            if tries > 200 {
                break;
            }
        }

        match mgr.add_instance(final_name.clone(), final_port, node.to_string()) {
            Ok(()) => {
                added.push(json!({
                    "name": final_name,
                    "port": final_port,
                    "node": node,
                }));
            }
            Err(e) => {
                errors.push(json!({ "node": node, "error": e.to_string() }));
            }
        }
    }

    json_ok(json!({
        "added": added,
        "errors": errors,
        "added_count": added.len(),
        "error_count": errors.len(),
    }))
}

fn sanitize_instance_name(node: &str) -> String {
    let s: String = node
        .chars()
        .map(|c| match c {
            '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|' => '-',
            c if c.is_control() => '-',
            c => c,
        })
        .collect();
    let s = s.trim().trim_matches('.').to_string();
    if s.is_empty() {
        "node".into()
    } else if s.chars().count() > 40 {
        s.chars().take(40).collect()
    } else {
        s
    }
}

async fn api_start_instance(State(state): State<AppState>, Path(name): Path<String>) -> Response {
    let password = default_password();
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    match mgr.start_instance(&name, &password) {
        Ok(()) => json_ok(json!({ "name": name, "status": "Running" })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

async fn api_stop_instance(State(state): State<AppState>, Path(name): Path<String>) -> Response {
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    match mgr.stop_instance(&name) {
        Ok(()) => json_ok(json!({ "name": name, "status": "Stopped" })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

async fn api_remove_instance(State(state): State<AppState>, Path(name): Path<String>) -> Response {
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    match mgr.remove_instance(&name) {
        Ok(()) => json_ok(json!({ "name": name, "removed": true })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

/// 对一批实例执行同一操作，汇总成功与失败明细。
fn batch_op_response(
    names: Vec<String>,
    mut op: impl FnMut(&str) -> anyhow::Result<()>,
) -> Response {
    let mut success = Vec::new();
    let mut errors = serde_json::Map::new();
    for name in names {
        match op(&name) {
            Ok(()) => success.push(name),
            Err(e) => {
                errors.insert(name, json!(e.to_string()));
            }
        }
    }
    json_ok(json!({
        "success": success,
        "errors": errors,
        "success_count": success.len(),
        "error_count": errors.len(),
    }))
}

async fn api_batch_start(
    State(state): State<AppState>,
    Json(body): Json<BatchOpBody>,
) -> Response {
    let password = default_password();
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    batch_op_response(body.names, |name| mgr.start_instance(name, &password))
}

async fn api_batch_stop(
    State(state): State<AppState>,
    Json(body): Json<BatchOpBody>,
) -> Response {
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    batch_op_response(body.names, |name| mgr.stop_instance(name))
}

async fn api_batch_delete(
    State(state): State<AppState>,
    Json(body): Json<BatchOpBody>,
) -> Response {
    let mut mgr = match state.manager.lock() {
        Ok(g) => g,
        Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
    };
    batch_op_response(body.names, |name| mgr.remove_instance(name))
}

async fn api_test_instance(State(state): State<AppState>, Path(name): Path<String>) -> Response {
    let result = {
        let mgr = match state.manager.lock() {
            Ok(g) => g,
            Err(_) => return json_error(StatusCode::INTERNAL_SERVER_ERROR, "锁失败"),
        };
        let name_owned = name.clone();
        match mgr.prepare_test(&name_owned) {
            Ok(port) => Ok((name_owned, port)),
            Err(e) => Err(e.to_string()),
        }
    };

    match result {
        Ok((name_owned, port)) => {
            let test_result = tokio::task::spawn_blocking(move || {
                crate::instance::probe_models(&name_owned, port)
            })
            .await;
            match test_result {
                Ok(r) => json_ok(serde_json::to_value(r).unwrap_or(json!({}))),
                Err(e) => json_error(StatusCode::INTERNAL_SERVER_ERROR, e.to_string()),
            }
        }
        Err(e) => json_error(StatusCode::BAD_REQUEST, e),
    }
}

async fn api_scan_start(
    State(state): State<AppState>,
    Json(body): Json<ScanStartBody>,
) -> Response {
    let (_, binary_dir, runtime_dir) = manager_paths();
    let password = default_password();
    let api_port = body.api_port.unwrap_or(DEFAULT_PROBE_API_PORT);
    let socks_port = body.socks_port.unwrap_or(DEFAULT_PROBE_SOCKS_PORT);
    let timeout = body.timeout.unwrap_or(12);
    let filter = body.nodes.filter(|v| !v.is_empty());

    match state.scan.start_scan(
        binary_dir,
        runtime_dir,
        password,
        api_port,
        socks_port,
        filter,
        timeout,
    ) {
        Ok(()) => {
            let snap = state.scan.progress_snapshot();
            json_ok(serde_json::to_value(snap).unwrap_or(json!({})))
        }
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

async fn api_scan_status(State(state): State<AppState>) -> Response {
    let snap = state.scan.progress_snapshot();
    json_ok(serde_json::to_value(snap).unwrap_or(json!({})))
}

async fn api_scan_stop(State(state): State<AppState>) -> Response {
    state.scan.request_stop();
    let snap = state.scan.progress_snapshot();
    json_ok(serde_json::to_value(snap).unwrap_or(json!({})))
}

async fn api_config_get() -> Response {
    let cfg = Config::load().unwrap_or_default();
    json_ok(json!({
        "base_url": cfg.base_url,
        "default_password": if cfg.default_password.is_empty() { "" } else { "***" },
        "has_password": !cfg.default_password.is_empty(),
        "clash_external_url": cfg.clash_external_url,
        "has_clash_token": !cfg.clash_auth_token.is_empty(),
    }))
}

#[derive(Debug, Deserialize)]
pub struct ConfigSetBody {
    pub key: String,
    pub value: String,
}

async fn api_config_set(Json(body): Json<ConfigSetBody>) -> Response {
    let mut cfg = Config::load().unwrap_or_default();
    match cfg.set(&body.key, &body.value) {
        Ok(()) => json_ok(json!({ "key": body.key })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

// ======================== 开机自启（Windows 注册表） ========================

const RUN_KEY: &str = r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run";
const RUN_NAME: &str = "opencode2api-manager";

fn autostart_command() -> String {
    let exe = std::env::current_exe().unwrap_or_default();
    format!("\"{}\"", exe.display())
}

#[cfg(windows)]
fn autostart_status() -> anyhow::Result<bool> {
    let out = std::process::Command::new("reg")
        .args(["query", RUN_KEY, "/v", RUN_NAME])
        .output()?;
    Ok(out.status.success())
}

#[cfg(not(windows))]
fn autostart_status() -> anyhow::Result<bool> {
    anyhow::bail!("仅 Windows 支持开机自启")
}

#[cfg(windows)]
fn autostart_set(enabled: bool) -> anyhow::Result<()> {
    if enabled {
        let val = autostart_command();
        std::process::Command::new("reg")
            .args(["add", RUN_KEY, "/v", RUN_NAME, "/t", "REG_SZ", "/d", &val, "/f"])
            .output()?;
    } else {
        // 幂等：值不存在时删除失败也可接受
        let _ = std::process::Command::new("reg")
            .args(["delete", RUN_KEY, "/v", RUN_NAME, "/f"])
            .output();
    }
    Ok(())
}

#[cfg(not(windows))]
fn autostart_set(_enabled: bool) -> anyhow::Result<()> {
    anyhow::bail!("仅 Windows 支持开机自启")
}

async fn api_autostart_get() -> Response {
    match autostart_status() {
        Ok(enabled) => json_ok(json!({ "enabled": enabled })),
        Err(e) => json_error(StatusCode::INTERNAL_SERVER_ERROR, e.to_string()),
    }
}

#[derive(Debug, Deserialize)]
pub struct AutostartBody {
    pub enabled: bool,
}

async fn api_autostart_set(Json(body): Json<AutostartBody>) -> Response {
    match autostart_set(body.enabled) {
        Ok(()) => json_ok(json!({ "enabled": body.enabled })),
        Err(e) => json_error(StatusCode::BAD_REQUEST, e.to_string()),
    }
}

pub async fn run_server(bind: &str) -> anyhow::Result<()> {
    let addr: SocketAddr = bind.parse()?;
    let listener = tokio::net::TcpListener::bind(addr).await?;
    let app = build_app();
    println!("管理界面: http://{}/", addr);
    println!("API: http://{}/api/instances", addr);
    println!("节点扫描: POST http://{}/api/nodes/scan", addr);
    axum::serve(listener, app).await?;
    Ok(())
}

/// 后台启动服务（桌面模式用）；端口被占用时返回 Err（已有实例在跑）
pub async fn spawn_server(bind: &str) -> anyhow::Result<SocketAddr> {
    let addr: SocketAddr = bind.parse()?;
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .map_err(|_| anyhow::anyhow!("端口 {} 已被占用", bind))?;
    let app = build_app();
    tokio::spawn(async move {
        if let Err(e) = axum::serve(listener, app).await {
            eprintln!("服务运行失败: {}", e);
        }
    });
    Ok(addr)
}

fn build_app() -> Router {
    let manager = Arc::new(Mutex::new(create_manager()));
    let scan = Arc::new(ScanController::new());
    let state = AppState { manager, scan };

    Router::new()
        .route("/", get(index))
        .route("/api/nodes", get(api_nodes))
        .route("/api/nodes/scan", post(api_scan_start))
        .route("/api/nodes/scan/status", get(api_scan_status))
        .route("/api/nodes/scan/stop", post(api_scan_stop))
        .route("/api/instances", get(api_list_instances).post(api_add_instance))
        .route("/api/instances/batch", post(api_batch_add))
        .route("/api/port/suggest", get(api_port_suggest))
        .route("/api/port/check", post(api_port_check))
        .route("/api/instances/{name}/start", post(api_start_instance))
        .route("/api/instances/{name}/stop", post(api_stop_instance))
        .route("/api/instances/{name}/test", post(api_test_instance))
        .route("/api/instances/batch/start", post(api_batch_start))
        .route("/api/instances/batch/stop", post(api_batch_stop))
        .route("/api/instances/batch/delete", post(api_batch_delete))
        .route("/api/instances/{name}", delete(api_remove_instance))
        .route("/api/config", get(api_config_get).post(api_config_set))
        .route("/api/autostart", get(api_autostart_get).post(api_autostart_set))
        .layer(CorsLayer::permissive())
        .with_state(state)
}

#[allow(dead_code)]
fn _status_running() -> InstanceStatus {
    InstanceStatus::Running
}
