//! Headless HTTP 服务：与 Tauri 桌面共用同一套 *_core() 逻辑。
//!
//! 两种入口：
//! - 桌面模式：lib.rs `run()` 在 setup 中 spawn 本服务（127.0.0.1:19090），前端经它取数
//! - headless 模式：main.rs `serve` 子命令阻塞运行本服务（默认 0.0.0.0:19090），
//!   同时托管打包后的前端静态文件（dist/），纯浏览器即可完成全部管理

use crate::commands;
use crate::core::AppCore;
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use serde::Deserialize;
use serde_json::json;
use std::sync::Arc;
use tower_http::cors::CorsLayer;
use tower_http::services::ServeDir;

/// 启动 headless HTTP 服务（阻塞）。bind_addr 形如 "127.0.0.1:19090" 或 "0.0.0.0:19090"。
pub async fn serve(bind_addr: &str, core: Arc<AppCore>) -> std::io::Result<()> {
    let app = build_router(core);
    let listener = tokio::net::TcpListener::bind(bind_addr).await?;
    println!("Headless 管理服务已启动: http://{}", bind_addr);
    axum::serve(listener, app).await
}

/// 构建 Router（桌面与 headless 共用同一路由表）
pub fn build_router(core: Arc<AppCore>) -> Router {
    // 静态目录解析：优先 ../dist（release 打包），回退 ./dist（开发目录）
    let dist_dir = std::env::current_dir()
        .unwrap_or_default()
        .join("dist");
    Router::new()
        .route("/api/health", get(health_handler))
        .route("/api/instances", get(list_instances_handler))
        .route("/api/instances", post(add_instance_handler))
        .route("/api/instances/batch", post(batch_add_handler))
        .route("/api/instances/batch", delete(batch_delete_handler))
        .route("/api/instances/batch/start", post(batch_start_handler))
        .route("/api/instances/batch/stop", post(batch_stop_handler))
        .route("/api/instances/restart-pool", post(restart_pool_handler))
        .route("/api/instances/{name}", post(start_instance_handler))
        .route("/api/instances/{name}/stop", post(stop_instance_handler))
        .route("/api/instances/{name}/remove", post(remove_instance_handler))
        .route("/api/instances/{name}/test", post(test_instance_handler))
        .route("/api/gateway", get(gateway_status_handler))
        .route("/api/gateway/stop", post(gateway_stop_handler))
        .route("/api/gateway/route-mode", post(gateway_route_mode_handler))
        .route("/api/join-gateway", post(set_join_gateway_handler))
        .route("/api/config", get(config_get_handler))
        .route("/api/config/{key}", post(config_set_handler))
        .route("/api/stats", get(stats_handler))
        .route("/api/call-log", get(call_log_handler))
        .route("/api/nodes", get(nodes_handler))
        .route("/api/binaries", get(binaries_handler))
        .route("/api/port/suggest", get(port_suggest_handler))
        .route("/api/port/check", get(port_check_handler))
        .route("/api/scan/start", post(scan_start_handler))
        .route("/api/scan/status", get(scan_status_handler))
        .route("/api/scan/stop", post(scan_stop_handler))
        .route("/api/autostart", get(autostart_get_handler))
        .route("/api/autostart", post(autostart_set_handler))
        .route("/api/data-clean", post(data_clean_handler))
        .fallback_service(ServeDir::new(dist_dir).append_index_html_on_directories(true))
        .layer(CorsLayer::permissive())
        .with_state(core)
}

// ---------- Handler 实现 ----------

fn err(e: String) -> (StatusCode, String) {
    (StatusCode::BAD_REQUEST, e)
}

fn to_json<T: serde::Serialize>(value: T) -> Json<serde_json::Value> {
    Json(serde_json::to_value(value).unwrap_or(json!({})))
}

async fn health_handler() -> impl IntoResponse {
    Json(json!({
        "ok": true,
        "service": "opencode2api-manager",
        "version": env!("CARGO_PKG_VERSION"),
    }))
}

async fn list_instances_handler(
    State(core): State<Arc<AppCore>>,
    Query(query): Query<RefreshQuery>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    if let Some(raw) = query.refresh {
        let names: Vec<String> = serde_json::from_str(&raw).unwrap_or_default();
        let core2 = core.clone();
        let result = tokio::task::spawn_blocking(move || commands::refresh_states_core(&core2, names))
            .await
            .map_err(|e| err(format!("刷新实例任务失败: {}", e)))?
            .map_err(err)?;
        return Ok(to_json(result));
    }
    Ok(to_json(commands::list_instances_core(&core).map_err(err)?))
}

#[derive(Deserialize)]
struct RefreshQuery {
    refresh: Option<String>,
}

#[derive(Deserialize)]
struct AddInstancePayload {
    name: String,
    port: u16,
    node: String,
    password: String,
}

async fn add_instance_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<AddInstancePayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let instance = commands::add_instance_core(
        &core,
        payload.name,
        payload.port,
        payload.node,
        payload.password,
    )
    .map_err(err)?;
    Ok(to_json(instance))
}

async fn start_instance_handler(
    State(core): State<Arc<AppCore>>,
    Path(name): Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let core2 = core.clone();
    tokio::task::spawn_blocking(move || commands::start_instance_core(&core2, &name))
        .await
        .map_err(|e| err(format!("启动实例任务失败: {}", e)))?
        .map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn stop_instance_handler(
    State(core): State<Arc<AppCore>>,
    Path(name): Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let core2 = core.clone();
    tokio::task::spawn_blocking(move || commands::stop_instance_core(&core2, &name))
        .await
        .map_err(|e| err(format!("停止实例任务失败: {}", e)))?
        .map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn remove_instance_handler(
    State(core): State<Arc<AppCore>>,
    Path(name): Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::remove_instance_core(&core, &name).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn test_instance_handler(
    State(core): State<Arc<AppCore>>,
    Path(name): Path<String>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let result = tokio::task::spawn_blocking(move || commands::test_instance_core(&core2, &name))
        .await
        .map_err(|e| err(format!("测试实例任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

#[derive(Deserialize)]
struct BatchPayload {
    #[serde(default)]
    names: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct BatchAddPayload {
    #[serde(default)]
    nodes: Vec<commands::BatchAddItem>,
    base_port: Option<u16>,
    #[serde(default)]
    use_node_name: Option<bool>,
    name_prefix: Option<String>,
}

async fn batch_add_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<BatchAddPayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let result = tokio::task::spawn_blocking(move || {
        commands::batch_add_core(
            &core2,
            payload.nodes,
            payload.base_port,
            payload.use_node_name,
            payload.name_prefix,
        )
    })
    .await
    .map_err(|e| err(format!("批量添加任务失败: {}", e)))?
    .map_err(err)?;
    Ok(to_json(result))
}

async fn batch_start_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<BatchPayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let names = payload.names;
    let result = tokio::task::spawn_blocking(move || commands::batch_start_core(&core2, names))
        .await
        .map_err(|e| err(format!("批量启动任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

async fn batch_stop_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<BatchPayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let names = payload.names;
    let result = tokio::task::spawn_blocking(move || commands::batch_stop_core(&core2, names))
        .await
        .map_err(|e| err(format!("批量停止任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

async fn batch_delete_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<BatchPayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let names = payload.names;
    let result = tokio::task::spawn_blocking(move || commands::batch_delete_core(&core2, names))
        .await
        .map_err(|e| err(format!("批量删除任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

async fn restart_pool_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let result = tokio::task::spawn_blocking(move || commands::restart_pool_core(&core2))
        .await
        .map_err(|e| err(format!("重启池任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

async fn gateway_status_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::gateway_status_core(&core).map_err(err)?))
}

async fn gateway_stop_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::gateway_stop_core(&core).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

#[derive(Deserialize)]
struct RouteModePayload {
    mode: String,
}

async fn gateway_route_mode_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<RouteModePayload>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::gateway_set_route_mode_core(&core, &payload.mode).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

#[derive(Deserialize)]
struct JoinGatewayPayload {
    name: String,
    join: bool,
}

async fn set_join_gateway_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<JoinGatewayPayload>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::set_join_gateway_core(&core, &payload.name, payload.join).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn config_get_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::config_get_core().map_err(err)?))
}

#[derive(Deserialize)]
struct ConfigValuePayload {
    value: String,
}

async fn config_set_handler(
    State(core): State<Arc<AppCore>>,
    Path(key): Path<String>,
    Json(payload): Json<ConfigValuePayload>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::config_set_core(&core, &key, &payload.value).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn stats_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::get_stats_core().map_err(err)?))
}

#[derive(Deserialize)]
struct CallLogQuery {
    limit: Option<usize>,
}

async fn call_log_handler(Query(query): Query<CallLogQuery>) -> Json<serde_json::Value> {
    to_json(commands::get_call_log_core(query.limit))
}

async fn nodes_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::list_nodes_core().map_err(err)?))
}

async fn binaries_handler() -> Json<serde_json::Value> {
    to_json(commands::get_binaries_info_core())
}

async fn port_suggest_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let port = tokio::task::spawn_blocking(move || commands::port_suggest_core(&core2))
        .await
        .map_err(|e| err(format!("端口建议任务失败: {}", e)))?
        .map_err(err)?;
    Ok(Json(json!({ "port": port })))
}

#[derive(Deserialize)]
struct PortCheckQuery {
    port: u16,
}

async fn port_check_handler(
    State(core): State<Arc<AppCore>>,
    Query(query): Query<PortCheckQuery>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let core2 = core.clone();
    let port = query.port;
    let result = tokio::task::spawn_blocking(move || commands::port_check_core(&core2, port))
        .await
        .map_err(|e| err(format!("端口检查任务失败: {}", e)))?
        .map_err(err)?;
    Ok(to_json(result))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct ScanStartPayload {
    nodes: Option<Vec<String>>,
    api_port: Option<u16>,
    socks_port: Option<u16>,
    timeout: Option<u64>,
}

async fn scan_start_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<ScanStartPayload>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let progress = commands::scan_start_core(
        &core,
        commands::ScanStartOpts {
            nodes: payload.nodes,
            api_port: payload.api_port,
            socks_port: payload.socks_port,
            timeout: payload.timeout,
        },
    )
    .map_err(err)?;
    Ok(to_json(progress))
}

async fn scan_status_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::scan_status_core(&core).map_err(err)?))
}

async fn scan_stop_handler(
    State(core): State<Arc<AppCore>>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(commands::scan_stop_core(&core).map_err(err)?))
}

async fn autostart_get_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    Ok(to_json(json!({ "enabled": commands::autostart_get_core().map_err(err)? })))
}

#[derive(Deserialize)]
struct AutostartPayload {
    enabled: bool,
}

async fn autostart_set_handler(Json(payload): Json<AutostartPayload>) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::autostart_set_core(payload.enabled).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

#[derive(Deserialize)]
struct DataCleanPayload {
    level: u8,
}

async fn data_clean_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<DataCleanPayload>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::data_clean_core(&core, payload.level).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}
