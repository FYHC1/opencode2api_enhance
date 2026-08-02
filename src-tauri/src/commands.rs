//! Tauri command 层：替代原 enhance 的 axum Web API。
//! 所有前端交互经由 #[tauri::command] invoke 进入本模块。

use crate::clash_yaml;
use crate::config::Config;
use crate::instance::{Instance, InstanceManager};
use crate::probe::{DEFAULT_PROBE_API_PORT, DEFAULT_PROBE_SOCKS_PORT};
use crate::AppState;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

// ======================== 路径与共享状态 ========================

pub fn manager_paths() -> (PathBuf, PathBuf, PathBuf) {
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

fn lock_manager<'a>(state: &'a tauri::State<'a, AppState>) -> Result<std::sync::MutexGuard<'a, InstanceManager>, String> {
    state
        .manager
        .lock()
        .map_err(|_| "状态锁失败".to_string())
}

// ======================== 响应结构 ========================

#[derive(Debug, Serialize)]
pub struct NodeView {
    pub name: String,
    pub node_type: String,
    pub server: String,
    pub port: u16,
    pub has_cred: bool,
    pub group: String,
}

#[derive(Debug, Deserialize)]
pub struct BatchAddItem {
    pub node: String,
    pub name: Option<String>,
    pub port: Option<u16>,
}

#[derive(Debug, Serialize)]
pub struct BatchAddResult {
    pub added: Vec<serde_json::Value>,
    pub errors: Vec<serde_json::Value>,
    pub added_count: usize,
    pub error_count: usize,
}

#[derive(Debug, Serialize)]
pub struct BatchOpResult {
    pub success: Vec<String>,
    pub errors: serde_json::Map<String, serde_json::Value>,
    pub success_count: usize,
    pub error_count: usize,
}

#[derive(Debug, Serialize)]
pub struct PortCheckResult {
    pub available: bool,
    pub reason: String,
}

#[derive(Debug, Serialize)]
pub struct ConfigView {
    pub base_url: String,
    pub default_password: String,
    pub has_password: bool,
    pub clash_external_url: String,
    pub has_clash_token: bool,
}

#[derive(Debug, Serialize)]
pub struct BinariesInfo {
    pub bin_dir: String,
    pub oc_exists: bool,
    pub sb_exists: bool,
}

// ======================== 节点 ========================

/// 列出全部节点（外部控制 API 优先 + 本地 Clash Verge profiles 补充）
#[tauri::command]
pub fn list_nodes() -> Result<Vec<NodeView>, String> {
    match clash_yaml::list_nodes_with_group() {
        Ok(nodes) => Ok(nodes
            .into_iter()
            .map(|n| NodeView {
                has_cred: n.password.is_some() || n.uuid.is_some(),
                name: n.name,
                node_type: n.node_type,
                server: n.server,
                port: n.port,
                group: n.group,
            })
            .collect()),
        Err(e) => Err(e.to_string()),
    }
}

// ======================== 实例 CRUD ========================

#[tauri::command]
pub fn list_instances(state: tauri::State<'_, AppState>) -> Result<Vec<Instance>, String> {
    let mut mgr = lock_manager(&state)?;
    let _ = mgr.load();
    Ok(mgr.list_instances().to_vec())
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

#[tauri::command]
pub fn add_instance(
    state: tauri::State<'_, AppState>,
    name: String,
    port: u16,
    node: String,
) -> Result<Instance, String> {
    if node.trim().is_empty() {
        return Err("节点不能为空".to_string());
    }
    if port < 1024 {
        return Err("端口需 >= 1024".to_string());
    }
    let mut mgr = lock_manager(&state)?;
    let _ = mgr.load();
    let final_name = if name.trim().is_empty() {
        next_auto_name(&mgr)
    } else {
        name.trim().to_string()
    };
    mgr.add_instance(final_name.clone(), port, node.trim().to_string())
        .map_err(|e| e.to_string())?;
    Ok(mgr
        .list_instances()
        .iter()
        .find(|i| i.name == final_name)
        .cloned()
        .ok_or_else(|| "实例创建后未找到".to_string())?)
}

#[tauri::command]
pub fn remove_instance(state: tauri::State<'_, AppState>, name: String) -> Result<(), String> {
    let mut mgr = lock_manager(&state)?;
    let _ = mgr.load();
    mgr.remove_instance(&name).map_err(|e| e.to_string())
}

// ======================== 实例启停（阻塞，走 spawn_blocking） ========================

#[tauri::command]
pub async fn start_instance(state: tauri::State<'_, AppState>, name: String) -> Result<(), String> {
    let password = default_password();
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        mgr.start_instance(&name, &password).map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn stop_instance(state: tauri::State<'_, AppState>, name: String) -> Result<(), String> {
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        mgr.stop_instance(&name).map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn test_instance(
    state: tauri::State<'_, AppState>,
    name: String,
) -> Result<crate::instance::TestResult, String> {
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        let name_owned = name.clone();
        let port = mgr
            .prepare_test(&name_owned)
            .map_err(|e| e.to_string())?;
        drop(mgr); // 探测在锁外进行，避免长阻塞
        Ok(crate::instance::probe_models(&name_owned, port))
    })
    .await
    .map_err(|e| e.to_string())?
}
// ======================== 批量操作 ========================

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

#[tauri::command]
pub fn batch_add(
    state: tauri::State<'_, AppState>,
    nodes: Vec<BatchAddItem>,
    base_port: Option<u16>,
    use_node_name: Option<bool>,
    name_prefix: Option<String>,
) -> Result<BatchAddResult, String> {
    if nodes.is_empty() {
        return Err("nodes 不能为空".to_string());
    }
    let base_port = base_port.unwrap_or(18100);
    let use_node_name = use_node_name.unwrap_or(true);
    let prefix = name_prefix.unwrap_or_else(|| "n".to_string());

    let mut mgr = lock_manager(&state)?;
    let _ = mgr.load();

    let mut added = Vec::new();
    let mut errors = Vec::new();

    for (i, item) in nodes.iter().enumerate() {
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

    let added_count = added.len();
    let error_count = errors.len();
    Ok(BatchAddResult {
        added,
        errors,
        added_count,
        error_count,
    })
}

/// 对一批实例执行同一操作，汇总成功与失败明细。
fn batch_op_response(
    names: Vec<String>,
    mut op: impl FnMut(&str) -> anyhow::Result<()>,
) -> BatchOpResult {
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
    BatchOpResult {
        success_count: success.len(),
        error_count: errors.len(),
        success,
        errors,
    }
}

#[tauri::command]
pub async fn batch_start(
    state: tauri::State<'_, AppState>,
    names: Vec<String>,
) -> Result<BatchOpResult, String> {
    let password = default_password();
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        Ok(batch_op_response(names, |name| {
            mgr.start_instance(name, &password)
        }))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn batch_stop(
    state: tauri::State<'_, AppState>,
    names: Vec<String>,
) -> Result<BatchOpResult, String> {
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        Ok(batch_op_response(names, |name| mgr.stop_instance(name)))
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn batch_delete(
    state: tauri::State<'_, AppState>,
    names: Vec<String>,
) -> Result<BatchOpResult, String> {
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        Ok(batch_op_response(names, |name| mgr.remove_instance(name)))
    })
    .await
    .map_err(|e| e.to_string())?
}

// ======================== 端口建议 / 校验 ========================

fn is_port_used(mgr: &InstanceManager, port: u16) -> bool {
    if mgr.list_instances().iter().any(|i| i.port == port) {
        return true;
    }
    crate::instance::wait_for_port(port, Duration::from_millis(300))
}

fn rand_seed() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0x1234_5678)
}

/// 建议一个可用端口（>10000，未被实例占用，本地未监听）
#[tauri::command]
pub async fn port_suggest(state: tauri::State<'_, AppState>) -> Result<u16, String> {
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let mut rng = rand_seed();
        let start = 18100 + (rng % 20000) as u16;
        for _ in 0..200 {
            let port = start.saturating_add(1 + (rng % 200) as u16) % 30000 + 10000;
            if !is_port_used(&mgr, port) {
                return Ok(port);
            }
            rng = rng
                .wrapping_mul(6364136223846793005)
                .wrapping_add(1442695040888963407);
        }
        Err("未找到可用端口".to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub async fn port_check(
    state: tauri::State<'_, AppState>,
    port: u16,
) -> Result<PortCheckResult, String> {
    if port < 1024 {
        return Err("端口需 >= 1024".to_string());
    }
    let manager = Arc::clone(&state.manager);
    tauri::async_runtime::spawn_blocking(move || {
        let mut mgr = manager.lock().map_err(|_| "状态锁失败".to_string())?;
        let _ = mgr.load();
        if mgr.list_instances().iter().any(|i| i.port == port) {
            return Ok(PortCheckResult {
                available: false,
                reason: "已被实例占用".to_string(),
            });
        }
        if crate::instance::wait_for_port(port, Duration::from_millis(300)) {
            return Ok(PortCheckResult {
                available: false,
                reason: "端口已被本机程序监听".to_string(),
            });
        }
        Ok(PortCheckResult {
            available: true,
            reason: "端口可用".to_string(),
        })
    })
    .await
    .map_err(|e| e.to_string())?
}
// ======================== 节点扫描 ========================

#[tauri::command]
pub fn scan_start(
    state: tauri::State<'_, AppState>,
    nodes: Option<Vec<String>>,
    api_port: Option<u16>,
    socks_port: Option<u16>,
    timeout: Option<u64>,
) -> Result<crate::probe::ScanProgress, String> {
    let (_, binary_dir, runtime_dir) = manager_paths();
    let password = default_password();
    let api_port = api_port.unwrap_or(DEFAULT_PROBE_API_PORT);
    let socks_port = socks_port.unwrap_or(DEFAULT_PROBE_SOCKS_PORT);
    let timeout = timeout.unwrap_or(12);
    let filter = nodes.filter(|v| !v.is_empty());

    match state.scan.start_scan(
        binary_dir,
        runtime_dir,
        password,
        api_port,
        socks_port,
        filter,
        timeout,
    ) {
        Ok(()) => Ok(state.scan.progress_snapshot()),
        Err(e) => Err(e.to_string()),
    }
}

#[tauri::command]
pub fn scan_status(state: tauri::State<'_, AppState>) -> Result<crate::probe::ScanProgress, String> {
    Ok(state.scan.progress_snapshot())
}

#[tauri::command]
pub fn scan_stop(state: tauri::State<'_, AppState>) -> Result<crate::probe::ScanProgress, String> {
    state.scan.request_stop();
    Ok(state.scan.progress_snapshot())
}

// ======================== 配置 ========================

#[tauri::command]
pub fn config_get() -> Result<ConfigView, String> {
    let cfg = Config::load().unwrap_or_default();
    Ok(ConfigView {
        base_url: cfg.base_url,
        default_password: if cfg.default_password.is_empty() {
            "".to_string()
        } else {
            "***".to_string()
        },
        has_password: !cfg.default_password.is_empty(),
        clash_external_url: cfg.clash_external_url,
        has_clash_token: !cfg.clash_auth_token.is_empty(),
    })
}

#[tauri::command]
pub fn config_set(key: String, value: String) -> Result<(), String> {
    let mut cfg = Config::load().unwrap_or_default();
    cfg.set(&key, &value).map_err(|e| e.to_string())
}

// ======================== 开机自启（Windows 注册表） ========================

const RUN_KEY: &str = r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run";
const RUN_NAME: &str = "opencode2api-manager";

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
fn set_autostart(enabled: bool) -> anyhow::Result<()> {
    if enabled {
        let exe = std::env::current_exe().unwrap_or_default();
        let val = format!("\"{}\"", exe.display());
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

#[tauri::command]
pub fn autostart_get() -> Result<bool, String> {
    autostart_status().map_err(|e| e.to_string())
}

#[tauri::command]
pub fn autostart_set(enabled: bool) -> Result<(), String> {
    set_autostart(enabled).map_err(|e| e.to_string())
}

// ======================== 二进制信息 ========================

#[tauri::command]
pub fn get_binaries_info() -> BinariesInfo {
    let (_, binary_dir, _) = manager_paths();
    BinariesInfo {
        bin_dir: binary_dir.display().to_string(),
        oc_exists: binary_dir.join("opencode2api.exe").exists()
            || binary_dir.join("opencode2api").exists(),
        sb_exists: binary_dir.join("sing-box.exe").exists()
            || binary_dir.join("sing-box").exists(),
    }
}

// ======================== 窗口控制（托盘） ========================

/// 收起到托盘（前端关闭按钮调用）
#[tauri::command]
pub fn hide_to_tray(app: tauri::AppHandle) {
    use tauri::Manager;
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.hide();
    }
}

/// 退出进程（前端红点确认后调用）
#[tauri::command]
pub fn quit_app(app: tauri::AppHandle) {
    app.exit(0);
}

/// 最大化/还原（前端交通灯按钮调用）
#[tauri::command]
pub fn toggle_maximize(app: tauri::AppHandle) {
    use tauri::Manager;
    if let Some(w) = app.get_webview_window("main") {
        if let Ok(max) = w.is_maximized() {
            if max {
                let _ = w.unmaximize();
            } else {
                let _ = w.maximize();
            }
        }
    }
}
