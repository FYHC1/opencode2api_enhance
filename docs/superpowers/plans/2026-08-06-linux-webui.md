# Linux 适配 + WebUI（桌面 + Headless）+ 功能完善 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Windows 优先的 Tauri 桌面应用改造为「Rust core + 双入口」架构：桌面模式与纯浏览器 headless 模式共用一套 React 前端与一套 Rust 管理逻辑，并完成功能完善（订阅拉取/健康巡检/报表导出/日志过滤/自定义网关密钥与端口/配置文件驱动），最终可在 Linux 上以 Docker / headless 形态运行。

**Architecture:** 抽取 `AppCore`（纯逻辑，无 Tauri 依赖）持有 manager/scan/gateway；axum HTTP 层与 Tauri command 层均调用同一批 `*_core()` 函数。前端 `api.ts` 从 `invoke` 全量切换为 `fetch`。桌面入口保留 Tauri 壳（窗口/托盘/自启）；新增 headless 入口（`serve` 子命令）提供 HTTP API + 静态文件托管。所有平台相关代码用 `cfg!` 分支隔离。

**Tech Stack:** Rust（axum 0.8 + tokio + serde）、Tauri 2、React 19 + Vite 8 + Tailwind 4、Go 核心（不动）、sing-box、Docker。

**Spec:** `docs/superpowers/specs/2026-08-06-linux-webui-design.md`

---

## 分阶段执行策略（2026-08-06 用户确认）

用户要求**分阶段推进**，而非一次完成全部：

- **Phase 1（当前，Windows 基础）**：以现有 Windows 版为基底实现 WebUI（桌面 + headless 双入口）+ 全部功能完善（订阅拉取/健康巡检/报表导出/日志过滤/自定义网关密钥/配置化端口）。本阶段代码保持 Windows 编译优先，`cfg!` 分支写好但 Linux 侧可暂不交付产物。
  - 执行范围：Task 1-13 + Task 17-22（M1 架构重构 + M2 配置化 + M3 功能完善），全部在 Windows/当前平台编译验证。
- **Phase 2（Docker 化）**：将 headless 模式打包为 Docker 镜像（multi-stage：Rust 编译管理服务 + Go 编译 opencode2api 核心 + sing-box + 前端静态文件），在 Linux 容器中运行。
  - 执行范围：新增 Task 25（Dockerfile）+ Task 26（docker-compose/部署验证）。依赖 Phase 1 完成。
- **Phase 3（Linux 完全适配）**：Linux 二进制内嵌/释放、CI ubuntu job、systemd 服务、部署文档。**Linux 桌面端 GUI 短期内不实现，仅预留扩展点**（`tauri_main` 保持平台无关壳，Linux 上 headless 为第一公民）。
  - 执行范围：Task 14-16 + Task 23 + 新增 Task 27（Linux GUI 预留扩展点说明）。

> **任务归属总览**：Phase 1 = Task 1-13 + 17-22；Phase 2 = Task 25-26；Phase 3 = Task 14-16 + 23 + 27。Task 24（端到端回归）在 Phase 1 末尾执行一次，Phase 3 末尾再执行一次。

---

## 文件结构地图

**新建：**
- `src-tauri/src/core.rs` — `AppCore` 纯逻辑结构 + 构造器（embed 释放/manager 加载/网关同步）
- `src-tauri/src/server.rs` — axum Router、全部 HTTP handler、静态文件托管、headless 入口 `serve()`
- `src-tauri/src/subscribe.rs`（M3）— 订阅 URL 拉取与解析（Clash YAML / V2Ray base64）
- `src-tauri/src/health.rs`（M3）— 后台健康巡检任务 + health.json 读写
- `docs/systemd/opencode2api-manager.service`（M4）— headless systemd 示例

**修改：**
- `src-tauri/Cargo.toml` — 加 axum、tower-http、reqwest、base64
- `src-tauri/src/main.rs` — 子命令分发（`serve` / 默认桌面）
- `src-tauri/src/lib.rs` — `AppState` 包 `AppCore`；`run()` 启动本地 HTTP 服务线程
- `src-tauri/src/commands.rs` — 逐 command 抽取 `*_core()` 纯函数，command 变薄壳
- `src-tauri/src/embed.rs` — 按平台选择内嵌二进制
- `src-tauri/src/instance.rs` — 二进制文件名平台化
- `src-tauri/src/config.rs` — 新增配置字段
- `src-tauri/src/gateway.rs` — 端口/密钥配置化
- `src-tauri/src/call_log.rs` — 过滤/聚合查询（M3）
- `src/lib/api.ts` — `invoke` → `fetch` 全量替换
- `src/components/TitleBar.tsx` — 非 Tauri 环境隐藏窗口按钮
- `vite.config.ts` — dev 代理 /api
- `.github/workflows/build-release.yml`（M2）— 增加 ubuntu 构建 job
- `README.md`、`docs/DEPLOYMENT.md`（M4）— Linux/headless 部署文档

**关键现状（实施前必须知晓）：**
- `src-tauri/src/lib.rs`：`AppState { manager: Arc<Mutex<InstanceManager>>, scan: Arc<ScanController>, gateway: Arc<Mutex<GatewayManager>> }`；`run()` 含 debug 数据目录隔离 + SSE_DEBUG 环境变量 + embed 释放 + manager 加载 + 网关同步 + 托盘 + 退出清理
- `src-tauri/src/commands.rs`：40+ `#[tauri::command]`；`manager_paths()` 返回 `(instances_path, binary_dir, runtime_dir)`；`no_window` 来自 instance.rs；`kill_process` 已有 Windows taskkill / 非 Windows sysinfo 双分支
- `src-tauri/src/gateway.rs`：`UNIFIED_GATEWAY_PORT`（debug 21080 / release 18080）、`UNIFIED_GATEWAY_KEY = "sk-unified-local"` 硬编码
- `src-tauri/src/config.rs`：`Config::config_dir()` 支持 `OPCODE2API_DATA_DIR` 环境变量；`get()/set()` 字符串键模式；`#[serde(default)]` 向后兼容
- `src/lib/api.ts`：全部类型定义（Instance/NodeView/ConfigView/GatewayStatus/StatsSummary/CallLogRecord 等）+ `api` 对象（invoke 封装）
- 前端页面：`src/pages/` 下 InstancesPage/PoolPage/NodesPage/StatsPage/LogsPage/SettingsPage；`TitleBar.tsx` 已对 `getCurrentWindow()` 失败做了 try/catch

---

## M1：架构重构（core 抽取 + axum HTTP API + 前端切 HTTP）

### Task 1: 添加 axum 依赖

**Files:**
- Modify: `src-tauri/Cargo.toml`

- [ ] **Step 1: 修改 Cargo.toml 添加依赖**

在 `[dependencies]` 段 `dirs = "5"` 之后追加：

```toml
axum = "0.8"
tower-http = { version = "0.6", features = ["fs", "cors"] }
reqwest = { version = "0.12", features = ["blocking", "json"] }
base64 = "0.22"
```

并在 `[dev-dependencies]` 段追加（若不存在则新建）：

```toml
[dev-dependencies]
tower = { version = "0.5", features = ["util"] }
```

- [ ] **Step 2: 验证依赖解析**

Run: `cd src-tauri && cargo check`
Expected: 编译通过（首次会拉取新依赖，无编译错误即可；`main.rs` 未引用 axum 属正常）。

- [ ] **Step 3: 提交**

```bash
git add src-tauri/Cargo.toml src-tauri/Cargo.lock
git commit -m "chore: 添加 axum + tower-http + reqwest + base64 依赖（headless HTTP API 层）"
```

---

### Task 2: 创建 core.rs（AppCore 纯逻辑结构）

**Files:**
- Create: `src-tauri/src/core.rs`

- [ ] **Step 1: 写 AppCore 结构与构造器**

创建 `src-tauri/src/core.rs`：

```rust
//! 纯逻辑核心：与 Tauri 完全解耦，桌面/headless 共用。
//! 持有实例管理器、扫描控制器、统一网关管理器。

use crate::commands;
use crate::embed;
use crate::gateway::GatewayManager;
use crate::instance::InstanceManager;
use crate::probe::ScanController;
use std::sync::{Arc, Mutex};

/// 全局共享状态（纯 Rust 类型，无 tauri 依赖）
pub struct AppCore {
    pub manager: Arc<Mutex<InstanceManager>>,
    pub scan: Arc<ScanController>,
    pub gateway: Arc<Mutex<GatewayManager>>,
}

impl AppCore {
    /// 构建核心：释放内嵌二进制 → 加载实例 → 校正僵尸状态 → 同步统一网关。
    pub fn new() -> Self {
        let (_, binary_dir, _) = commands::manager_paths();
        match embed::ensure_binaries(&binary_dir) {
            Ok(wrote) => {
                if wrote {
                    println!("已释放内置组件到 {}", binary_dir.display());
                }
            }
            Err(e) => eprintln!("警告: 释放内置组件失败: {}", e),
        }

        let (instances_path, binary_dir, runtime_dir) = commands::manager_paths();
        let mut manager = InstanceManager::new(instances_path, binary_dir.clone(), runtime_dir.clone());
        let _ = manager.load();
        let _ = manager.reconcile_states();

        let manager = Arc::new(Mutex::new(manager));
        let gateway_manager = Arc::new(Mutex::new(GatewayManager::new(binary_dir, runtime_dir)));
        if let (Ok(mgr), Ok(mut gateway)) = (manager.lock(), gateway_manager.lock()) {
            let _ = gateway.sync(mgr.list_instances());
        }

        AppCore {
            manager,
            scan: Arc::new(ScanController::new()),
            gateway: gateway_manager,
        }
    }
}

impl Default for AppCore {
    fn default() -> Self {
        Self::new()
    }
}
```

- [ ] **Step 2: 在 lib.rs 注册模块并验证编译**

在 `src-tauri/src/lib.rs` 顶部模块声明区加入：

```rust
pub mod core;
```

Run: `cd src-tauri && cargo check`
Expected: 编译通过（若报未使用警告属正常）。

- [ ] **Step 3: 提交**

```bash
git add src-tauri/src/core.rs src-tauri/src/lib.rs
git commit -m "feat(core): 抽取 AppCore 纯逻辑核心结构（桌面/headless 共用）"
```

---

### Task 3: AppState 改为包裹 AppCore

**Files:**
- Modify: `src-tauri/src/lib.rs:15-20`

- [ ] **Step 1: 替换 AppState 定义**

将 lib.rs 中的：

```rust
/// 全局共享状态（与 Windsurf Account Manager 的 AppState 模式一致）
pub struct AppState {
    pub manager: Arc<Mutex<instance::InstanceManager>>,
    pub scan: Arc<probe::ScanController>,
    pub gateway: Arc<Mutex<gateway::GatewayManager>>,
}
```

替换为：

```rust
/// Tauri 侧状态壳：仅包一层 Arc<AppCore>，全部逻辑走 core
pub struct AppState {
    pub core: Arc<core::AppCore>,
}
```

- [ ] **Step 2: 重写 run() 的状态构建部分**

将 lib.rs `run()` 中从 `let (instances_path, binary_dir, runtime_dir) = commands::manager_paths();` 到 `gateway.sync(...)` 的整段（含 embed 释放）替换为：

```rust
    let core = Arc::new(core::AppCore::new());
```

并将 `.manage(AppState { manager, scan: Arc::new(probe::ScanController::new()), gateway: gateway_manager })` 替换为：

```rust
        .manage(AppState {
            core: core.clone(),
        })
```

注意：`run()` 顶部 debug 数据目录隔离与 SSE_DEBUG 环境变量逻辑**保留不动**。

- [ ] **Step 3: 适配 commands.rs 的 state 访问**

Run: `cd src-tauri && cargo check`
Expected: 大量编译错误，均指向 `commands.rs` 与 `lib.rs` 中 `state.manager` / `state.gateway` / `state.scan` 的访问。

先做最小编译修复：在 `commands.rs` 顶部 import 区加入：

```rust
use crate::core::AppCore;
```

并将 `lock_manager` 函数改为：

```rust
fn lock_manager<'a>(
    state: &'a tauri::State<'a, AppState>,
) -> Result<std::sync::MutexGuard<'a, InstanceManager>, String> {
    state
        .core
        .manager
        .lock()
        .map_err(|_| "状态锁失败".to_string())
}
```

其余 `state.manager` → `state.core.manager`、`state.gateway` → `state.core.gateway`、`state.scan` → `state.core.scan` 用 `replaceAll` 全局替换完成。`sync_gateway`、`stop_all_instances` 等辅助函数同样处理。

Run: `cd src-tauri && cargo check`
Expected: 编译通过。

- [ ] **Step 4: 提交**

```bash
git add src-tauri/src/lib.rs src-tauri/src/commands.rs
git commit -m "refactor: AppState 改为包裹 AppCore，commands 访问路径适配"
```

---

### Task 4: 抽取第一组 *_core() 纯函数（节点/实例/网关）

**Files:**
- Modify: `src-tauri/src/commands.rs`

模式：每个 `#[tauri::command]` 拆成「`pub fn xxx_core(core: &AppCore, args...)` 纯函数」+「command 薄壳调 `&state.core`」。axum 层后续复用同一批 `*_core()`。

- [ ] **Step 1: 抽取 list_nodes**

将 `list_nodes` 改为：

```rust
/// 核心逻辑：列出全部节点（外部控制 API 优先 + 本地 Clash Verge profiles 补充）
pub fn list_nodes_core() -> Result<Vec<NodeView>, String> {
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

#[tauri::command]
pub fn list_nodes() -> Result<Vec<NodeView>, String> {
    list_nodes_core()
}
```

- [ ] **Step 2: 抽取网关命令**

```rust
/// 核心逻辑：查询统一网关状态
pub fn gateway_status_core(core: &AppCore) -> Result<crate::gateway::GatewayStatus, String> {
    let total_instances = core
        .manager
        .lock()
        .map_err(|_| "状态锁失败".to_string())?
        .list_instances()
        .iter()
        .filter(|i| i.join_gateway)
        .count();
    let mut gateway = core
        .gateway
        .lock()
        .map_err(|_| "网关锁失败".to_string())?;
    Ok(gateway.status(total_instances))
}

#[tauri::command]
pub fn gateway_status(state: tauri::State<'_, AppState>) -> Result<crate::gateway::GatewayStatus, String> {
    gateway_status_core(&state.core)
}

/// 核心逻辑：同步统一网关（根据运行中且 join_gateway=true 的实例更新网关池）
pub fn sync_gateway_core(core: &AppCore) {
    let instances = core
        .manager
        .lock()
        .map(|mut mgr| {
            let _ = mgr.reconcile_states();
            mgr.list_instances().to_vec()
        })
        .unwrap_or_default();
    if let Ok(mut gateway) = core.gateway.lock() {
        if let Err(e) = gateway.sync(&instances) {
            eprintln!("统一网关同步失败: {}", e);
        }
    }
}

pub fn sync_gateway(state: &tauri::State<'_, AppState>) {
    sync_gateway_core(&state.core);
}

/// 核心逻辑：切换网关路由模式
pub fn gateway_set_route_mode_core(core: &AppCore, mode: &str) -> Result<(), String> {
    if mode != "smart" && mode != "failover" && mode != "round_robin" {
        return Err("路由模式仅支持 smart / failover / round_robin".to_string());
    }
    let instances = core
        .manager
        .lock()
        .map_err(|_| "状态锁失败".to_string())?
        .list_instances()
        .to_vec();
    let mut gateway = core.gateway.lock().map_err(|_| "网关锁失败".to_string())?;
    gateway.set_route_mode(mode);
    gateway.stop();
    gateway
        .sync(&instances)
        .map_err(|e| format!("切换路由模式失败: {}", e))
}

#[tauri::command]
pub fn gateway_set_route_mode(state: tauri::State<'_, AppState>, mode: String) -> Result<(), String> {
    gateway_set_route_mode_core(&state.core, &mode)
}

/// 核心逻辑：关闭统一网关
pub fn gateway_stop_core(core: &AppCore) -> Result<(), String> {
    let mut gateway = core.gateway.lock().map_err(|_| "网关锁失败".to_string())?;
    gateway.stop();
    Ok(())
}

#[tauri::command]
pub fn gateway_stop(state: tauri::State<'_, AppState>) -> Result<(), String> {
    gateway_stop_core(&state.core)
}

/// 核心逻辑：切换实例 join_gateway 并同步网关
pub fn set_join_gateway_core(core: &AppCore, name: &str, join: bool) -> Result<(), String> {
    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let _ = mgr.load();
    mgr.set_join_gateway(name, join).map_err(|e| e.to_string())?;
    mgr.save_state().map_err(|e| e.to_string())?;
    let instances = mgr.list_instances().to_vec();
    drop(mgr);
    if let Ok(mut gateway) = core.gateway.lock() {
        gateway
            .sync(&instances)
            .map_err(|e| format!("同步网关失败: {}", e))?;
    }
    Ok(())
}

#[tauri::command]
pub fn set_join_gateway(state: tauri::State<'_, AppState>, name: String, join: bool) -> Result<(), String> {
    set_join_gateway_core(&state.core, &name, join)
}
```

- [ ] **Step 3: 抽取实例 CRUD（list_instances / refresh_states / add_instance / remove_instance）**

```rust
/// 核心逻辑：列出全部实例
pub fn list_instances_core(core: &AppCore) -> Result<Vec<Instance>, String> {
    let mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let _ = mgr.reconcile_states();
    Ok(mgr.list_instances().to_vec())
}

#[tauri::command]
pub fn list_instances(state: tauri::State<'_, AppState>) -> Result<Vec<Instance>, String> {
    list_instances_core(&state.core)
}

/// 核心逻辑：手动刷新指定实例状态
pub fn refresh_states_core(core: &AppCore, names: Vec<String>) -> Result<Vec<Instance>, String> {
    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let _ = mgr.load();
    let _ = mgr.refresh_states(&names);
    Ok(mgr.list_instances().to_vec())
}

#[tauri::command]
pub fn refresh_states(state: tauri::State<'_, AppState>, names: Vec<String>) -> Result<Vec<Instance>, String> {
    refresh_states_core(&state.core, names)
}

/// 核心逻辑：新增实例
pub fn add_instance_core(
    core: &AppCore,
    name: String,
    port: u16,
    node: String,
    password: String,
) -> Result<Instance, String> {
    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let ip = node_ip(&node);
    mgr.add_instance(name.clone(), port, node, password, ip)
        .map_err(|e| e.to_string())?;
    mgr.save_state().map_err(|e| e.to_string())?;
    Ok(mgr
        .list_instances()
        .iter()
        .find(|i| i.name == name)
        .cloned()
        .ok_or_else(|| "实例添加后未找到".to_string())?)
}

#[tauri::command]
pub fn add_instance(
    state: tauri::State<'_, AppState>,
    name: String,
    port: u16,
    node: String,
    password: String,
) -> Result<Instance, String> {
    add_instance_core(&state.core, name, port, node, password)
}

/// 核心逻辑：移除实例（运行中先自动关闭）
pub fn remove_instance_core(core: &AppCore, name: &str) -> Result<(), String> {
    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    mgr.remove_instance(name).map_err(|e| e.to_string())?;
    mgr.save_state().map_err(|e| e.to_string())?;
    drop(mgr);
    sync_gateway_core(core);
    Ok(())
}

#[tauri::command]
pub fn remove_instance(state: tauri::State<'_, AppState>, name: String) -> Result<(), String> {
    remove_instance_core(&state.core, &name)
}
```

> 注：`node_ip` 辅助函数（查节点 server:port）已在 commands.rs 存在（约 249 行），保持不变。

- [ ] **Step 4: 验证编译 + 运行既有 Rust 测试**

Run: `cd src-tauri && cargo check && cargo test`
Expected: 编译通过；既有测试（gateway 端口隔离测试等）全绿。

- [ ] **Step 5: 提交**

```bash
git add src-tauri/src/commands.rs
git commit -m "refactor: 抽取节点/实例/网关核心逻辑为 *_core() 纯函数"
```

---

### Task 5: 抽取启停/批量/端口命令核心逻辑

**Files:**
- Modify: `src-tauri/src/commands.rs`

异步命令（`start_instance`/`stop_instance`/`test_instance`/`batch_start`/`batch_stop`/`batch_delete`/`restart_pool`/`port_suggest`/`port_check`）内部已有并行逻辑。抽取原则：**把不依赖 tauri 的内部逻辑整体搬入 `*_core()`（同步签名，返回 `Result<_, String>`），异步 wrapper 保留**。

- [ ] **Step 1: 抽取 start_instance**

阅读 `start_instance`（约 540-578 行）现有实现，将命令体内逻辑（不涉及 `state.` 的部分）原样搬入：

```rust
/// 核心逻辑：启动实例
pub fn start_instance_core(core: &AppCore, name: &str) -> Result<(), String> {
    // 将原 #[tauri::command] start_instance 的函数体原样搬入，
    // 把其中的 state.manager / state.gateway 替换为 core.manager / core.gateway
    // （原逻辑：锁 manager → 启动实例 → 若 join_gateway 则 sync_gateway_core(core)）
}
```

`#[tauri::command]` 薄壳变为：

```rust
#[tauri::command]
pub async fn start_instance(state: tauri::State<'_, AppState>, name: String) -> Result<(), String> {
    let core = state.core.clone();
    tokio::task::spawn_blocking(move || start_instance_core(&core, &name))
        .await
        .map_err(|e| format!("启动实例任务失败: {}", e))?
}
```

（保持与原实现一致的并发模型；若原实现未用 spawn_blocking，薄壳直接 `start_instance_core(&state.core, &name)`。）

- [ ] **Step 2: 按同一模式抽取 stop_instance / test_instance**

同 Step 1 模式。`stop_instance` 结束后需 `sync_gateway_core`；`test_instance` 返回 `TestResult`（保持签名 `test_instance_core(core, name) -> Result<TestResult, String>`）。

- [ ] **Step 3: 按同一模式抽取 batch_start / batch_stop / batch_delete / restart_pool**

这四个命令内部均含批量并行逻辑（`InstanceManager::new_ephemeral` worker 模式），将逻辑搬入 `*_core()`，wrapper 调 `spawn_blocking`。`batch_delete` 结束需 `sync_gateway_core(core)`。`batch_add` 抽取为 `batch_add_core(req: &serde_json::Value) -> Result<BatchAddResult, String>`（req 为前端原始参数结构，用 `serde_json::from_value` 反序列化到原参数结构后复用原逻辑）。

- [ ] **Step 4: 抽取 port_suggest / port_check**

```rust
/// 核心逻辑：建议可用端口
pub fn port_suggest_core(core: &AppCore) -> Result<u16, String> {
    // 原实现：锁定 manager 获取已用端口集合，从 18000 起递增找 is_port_free，搬入原函数体
}

#[tauri::command]
pub async fn port_suggest(state: tauri::State<'_, AppState>) -> Result<u16, String> {
    let core = state.core.clone();
    tokio::task::spawn_blocking(move || port_suggest_core(&core))
        .await
        .map_err(|e| format!("端口建议任务失败: {}", e))?
}

/// 核心逻辑：检查端口是否可用
pub fn port_check_core(core: &AppCore, port: u16) -> Result<PortCheckResult, String> {
    // 原实现：manager 已用端口 + is_port_free 双重判断，搬入原函数体
}
```

- [ ] **Step 5: 编译 + 测试 + 确认无残留**

Run: `cd src-tauri && cargo check && cargo test && grep -rn "todo!" src-tauri/src/ || echo clean`
Expected: 全绿且无 `todo!()`。

- [ ] **Step 6: 提交**

```bash
git add src-tauri/src/commands.rs
git commit -m "refactor: 抽取启停/测试/批量/端口逻辑为 *_core() 纯函数"
```

---

### Task 6: 抽取扫描/配置/统计/日志核心逻辑

**Files:**
- Modify: `src-tauri/src/commands.rs`

- [ ] **Step 1: 抽取 scan_start / scan_status / scan_stop**

```rust
/// 扫描启动参数（axum 与 tauri command 共用）
#[derive(Debug, Default, Clone)]
pub struct ScanStartOpts {
    pub nodes: Option<Vec<String>>,
    pub api_port: Option<u16>,
    pub socks_port: Option<u16>,
    pub timeout: Option<u64>,
}

/// 核心逻辑：启动节点扫描
pub fn scan_start_core(core: &AppCore, opts: ScanStartOpts) -> Result<crate::probe::ScanProgress, String> {
    core.scan
        .start_scan(opts.nodes, opts.api_port, opts.socks_port, opts.timeout)
}

#[tauri::command]
pub fn scan_start(
    state: tauri::State<'_, AppState>,
    nodes: Option<Vec<String>>,
    api_port: Option<u16>,
    socks_port: Option<u16>,
    timeout: Option<u64>,
) -> Result<crate::probe::ScanProgress, String> {
    scan_start_core(&state.core, ScanStartOpts { nodes, api_port, socks_port, timeout })
}

/// 核心逻辑：扫描状态快照
pub fn scan_status_core(core: &AppCore) -> Result<crate::probe::ScanProgress, String> {
    Ok(core.scan.progress_snapshot())
}

#[tauri::command]
pub fn scan_status(state: tauri::State<'_, AppState>) -> Result<crate::probe::ScanProgress, String> {
    scan_status_core(&state.core)
}

/// 核心逻辑：请求停止扫描
pub fn scan_stop_core(core: &AppCore) -> Result<crate::probe::ScanProgress, String> {
    core.scan.request_stop();
    Ok(core.scan.progress_snapshot())
}

#[tauri::command]
pub fn scan_stop(state: tauri::State<'_, AppState>) -> Result<crate::probe::ScanProgress, String> {
    scan_stop_core(&state.core)
}
```

> 注：若 `ScanController::start_scan` 的签名与上述参数不完全一致，以 probe.rs 实际签名为准调整（参数顺序/类型保持一一对应）。

- [ ] **Step 2: 抽取 config_get / config_set**

```rust
/// 核心逻辑：读取配置视图
pub fn config_get_core() -> Result<ConfigView, String> {
    let config = Config::load().map_err(|e| e.to_string())?;
    Ok(ConfigView {
        base_url: config.base_url.clone(),
        default_password: config.default_password.clone(),
        has_password: !config.default_password.is_empty(),
        clash_external_url: config.clash_external_url.clone(),
        has_clash_token: !config.clash_auth_token.is_empty(),
        timeout_ttft_min_ms: config.timeout_ttft_min_ms.unwrap_or(0),
        timeout_ttft_max_ms: config.timeout_ttft_max_ms.unwrap_or(0),
        timeout_silence_min_ms: config.timeout_silence_min_ms.unwrap_or(0),
        timeout_silence_max_ms: config.timeout_silence_max_ms.unwrap_or(0),
        failover_probe_min: config.failover_probe_min.unwrap_or(0),
        failover_probe_max: config.failover_probe_max.unwrap_or(0),
        call_log_max: config.call_log_max.unwrap_or(0),
        show_node_prefix: config.show_node_prefix.unwrap_or(false),
    })
}

#[tauri::command]
pub fn config_get() -> Result<ConfigView, String> {
    config_get_core()
}

/// 核心逻辑：设置配置项
pub fn config_set_core(key: &str, value: &str) -> Result<(), String> {
    let mut config = Config::load().map_err(|e| e.to_string())?;
    config.set(key, value).map_err(|e| e.to_string())?;
    config.save().map_err(|e| e.to_string())
}

#[tauri::command]
pub fn config_set(key: String, value: String) -> Result<(), String> {
    config_set_core(&key, &value)
}
```

> 注：原 `config_set` 若在保存后触发配置热更新（applyConfig），需一并搬入（保持行为一致）。

- [ ] **Step 3: 抽取 autostart / get_binaries_info / get_stats / get_call_log**

```rust
/// 核心逻辑：二进制信息
pub fn get_binaries_info_core() -> BinariesInfo {
    let (_, binary_dir, _) = manager_paths();
    BinariesInfo {
        bin_dir: binary_dir.display().to_string(),
        oc_exists: binary_dir.join("opencode2api.exe").exists()
            || binary_dir.join("opencode2api").exists(),
        sb_exists: binary_dir.join("sing-box.exe").exists()
            || binary_dir.join("sing-box").exists(),
    }
}

#[tauri::command]
pub fn get_binaries_info() -> BinariesInfo {
    get_binaries_info_core()
}

/// 核心逻辑：统计摘要（原 get_stats 函数体搬入）
pub fn get_stats_core() -> Result<StatsSummary, String> {
    // 搬入原 get_stats 函数体（不变更逻辑）
}

#[tauri::command]
pub fn get_stats() -> Result<StatsSummary, String> {
    get_stats_core()
}

/// 核心逻辑：调用日志（原 get_call_log 函数体搬入）
pub fn get_call_log_core(limit: Option<usize>) -> Vec<crate::call_log::CallLogRecord> {
    // 搬入原 get_call_log 函数体
}

#[tauri::command]
pub fn get_call_log(limit: Option<usize>) -> Vec<crate::call_log::CallLogRecord> {
    get_call_log_core(limit)
}
```

`autostart_get`/`autostart_set` 抽取为 `autostart_get_core()` / `autostart_set_core(enabled: bool)`（原函数体搬入，含 Windows/Linux cfg 分支）。`data_clean` 抽取为 `data_clean_core(core: &AppCore, level: u8)`（原函数体搬入，内部 stop_all_instances 改为基于 `&AppCore`）。

- [ ] **Step 4: 仅桌面命令（hide_to_tray / quit_app / toggle_maximize）**

这三个命令依赖 `tauri::AppHandle`，**不抽 core**，保留原样（axum 层不暴露）。

- [ ] **Step 5: 编译 + 测试 + 确认无 todo! 残留**

Run: `cd src-tauri && cargo check && cargo test && grep -rn "todo!" src-tauri/src/ || echo clean`
Expected: 全绿且无 `todo!()`。

- [ ] **Step 6: 提交**

```bash
git add src-tauri/src/commands.rs
git commit -m "refactor: 抽取扫描/配置/统计/日志核心逻辑为 *_core() 纯函数"
```

---

### Task 7: 创建 server.rs（axum Router + HTTP API）

**Files:**
- Create: `src-tauri/src/server.rs`

- [ ] **Step 1: 写 HTTP 层骨架**

创建 `src-tauri/src/server.rs`：

```rust
//! Headless HTTP 服务：与 Tauri 桌面共用同一套 *_core() 逻辑。

use crate::commands;
use crate::core::AppCore;
use crate::AppState;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde_json::json;
use std::sync::Arc;
use tower_http::cors::CorsLayer;
use tower_http::services::ServeDir;

/// 启动 headless HTTP 服务（阻塞）。bind_addr 形如 "127.0.0.1:19090"。
pub async fn serve(bind_addr: &str, core: Arc<AppCore>) -> std::io::Result<()> {
    let app = build_router(core);
    let listener = tokio::net::TcpListener::bind(bind_addr).await?;
    println!("Headless 管理服务已启动: http://{}", bind_addr);
    axum::serve(listener, app).await
}

/// 构建 Router（桌面模式亦复用：在内嵌 http 线程中 serve 本 router）
pub fn build_router(core: Arc<AppCore>) -> Router {
    // 静态目录解析：优先 ../dist（release 打包），回退 ./dist（开发目录）
    let dist_dir = std::env::current_dir()
        .unwrap_or_default()
        .join("dist");
    Router::new()
        .route("/api/instances", get(list_instances_handler))
        .route("/api/instances", post(add_instance_handler))
        .route("/api/instances/{name}", post(start_instance_handler))
        .route("/api/instances/{name}/stop", post(stop_instance_handler))
        .route("/api/instances/{name}/remove", post(remove_instance_handler))
        .route("/api/gateway", get(gateway_status_handler))
        .route("/api/gateway/stop", post(gateway_stop_handler))
        .route("/api/gateway/route-mode", post(gateway_route_mode_handler))
        .route("/api/config", get(config_get_handler))
        .route("/api/config/{key}", post(config_set_handler))
        .route("/api/stats", get(stats_handler))
        .route("/api/call-log", get(call_log_handler))
        .route("/api/nodes", get(nodes_handler))
        .route("/api/binaries", get(binaries_handler))
        .route("/api/scan/start", post(scan_start_handler))
        .route("/api/scan/status", get(scan_status_handler))
        .route("/api/scan/stop", post(scan_stop_handler))
        .route("/api/health", get(health_handler))
        .route("/api/autostart", get(autostart_get_handler))
        .route("/api/autostart", post(autostart_set_handler))
        .fallback_service(ServeDir::new(dist_dir).append_index_html_on_directories(true))
        .layer(CorsLayer::permissive())
        .with_state(core)
}
```

- [ ] **Step 2: 在 lib.rs 注册 server 模块**

在 `src-tauri/src/lib.rs` 模块声明区加入：

```rust
pub mod server;
```

Run: `cd src-tauri && cargo check`
Expected: 编译失败属预期（handler 未定义）。继续 Step 3。

- [ ] **Step 3: 写第一批 handler（health/instances/gateway/config）**

在 server.rs 中追加：

```rust
// ---------- Handler 实现 ----------

async fn health_handler() -> impl IntoResponse {
    Json(json!({ "ok": true, "service": "opencode2api-manager", "version": env!("CARGO_PKG_VERSION") }))
}

async fn list_instances_handler(State(core): State<Arc<AppCore>>) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let instances = commands::list_instances_core(&core).map_err(err)?;
    Ok(Json(serde_json::to_value(instances).unwrap_or(json!([]))))
}

async fn add_instance_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<serde_json::Value>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let name = payload["name"].as_str().unwrap_or_default().to_string();
    let port = payload["port"].as_u64().unwrap_or(0) as u16;
    let node = payload["node"].as_str().unwrap_or_default().to_string();
    let password = payload["password"].as_str().unwrap_or_default().to_string();
    let instance = commands::add_instance_core(&core, name, port, node, password).map_err(err)?;
    Ok(Json(serde_json::to_value(instance).unwrap_or(json!({}))))
}

async fn start_instance_handler(
    State(core): State<Arc<AppCore>>,
    axum::extract::Path(name): axum::extract::Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::start_instance_core(&core, &name).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn stop_instance_handler(
    State(core): State<Arc<AppCore>>,
    axum::extract::Path(name): axum::extract::Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::stop_instance_core(&core, &name).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn remove_instance_handler(
    State(core): State<Arc<AppCore>>,
    axum::extract::Path(name): axum::extract::Path<String>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::remove_instance_core(&core, &name).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn gateway_status_handler(State(core): State<Arc<AppCore>>) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let status = commands::gateway_status_core(&core).map_err(err)?;
    Ok(Json(serde_json::to_value(status).unwrap_or(json!({}))))
}

async fn gateway_stop_handler(State(core): State<Arc<AppCore>>) -> Result<impl IntoResponse, (StatusCode, String)> {
    commands::gateway_stop_core(&core).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn gateway_route_mode_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<serde_json::Value>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let mode = payload["mode"].as_str().unwrap_or_default();
    commands::gateway_set_route_mode_core(&core, mode).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

async fn config_get_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let view = commands::config_get_core().map_err(err)?;
    Ok(Json(serde_json::to_value(view).unwrap_or(json!({}))))
}

async fn config_set_handler(
    axum::extract::Path(key): axum::extract::Path<String>,
    Json(payload): Json<serde_json::Value>,
) -> Result<impl IntoResponse, (StatusCode, String)> {
    let value = payload["value"].as_str().unwrap_or_default();
    commands::config_set_core(&key, value).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}

/// 统一错误转换：String → (StatusCode, String)
fn err(e: String) -> (StatusCode, String) {
    (StatusCode::BAD_REQUEST, e)
}
```

- [ ] **Step 4: 写第二批 handler（stats/call-log/nodes/binaries/scan/autostart）**

在 server.rs 中追加（每个 handler 调对应 `*_core()`）：

```rust
async fn stats_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let stats = commands::get_stats_core().map_err(err)?;
    Ok(Json(serde_json::to_value(stats).unwrap_or(json!({}))))
}

async fn call_log_handler(Query(params): axum::extract::Query<CallLogQuery>) -> Json<serde_json::Value> {
    let records = commands::get_call_log_core(params.limit);
    Json(serde_json::to_value(records).unwrap_or(json!([])))
}

#[derive(serde::Deserialize)]
struct CallLogQuery { limit: Option<usize> }

async fn nodes_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let nodes = commands::list_nodes_core().map_err(err)?;
    Ok(Json(serde_json::to_value(nodes).unwrap_or(json!([]))))
}

async fn binaries_handler() -> Json<serde_json::Value> {
    let info = commands::get_binaries_info_core();
    Json(serde_json::to_value(info).unwrap_or(json!({})))
}

async fn scan_start_handler(
    State(core): State<Arc<AppCore>>,
    Json(payload): Json<serde_json::Value>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let opts = commands::ScanStartOpts {
        nodes: payload["nodes"].as_array().map(|a| a.iter().filter_map(|v| v.as_str().map(String::from)).collect()),
        api_port: payload["api_port"].as_u64().map(|p| p as u16),
        socks_port: payload["socks_port"].as_u64().map(|p| p as u16),
        timeout: payload["timeout"].as_u64(),
    };
    let progress = commands::scan_start_core(&core, opts).map_err(err)?;
    Ok(Json(serde_json::to_value(progress).unwrap_or(json!({}))))
}

async fn scan_status_handler(State(core): State<Arc<AppCore>>) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let progress = commands::scan_status_core(&core).map_err(err)?;
    Ok(Json(serde_json::to_value(progress).unwrap_or(json!({}))))
}

async fn scan_stop_handler(State(core): State<Arc<AppCore>>) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let progress = commands::scan_stop_core(&core).map_err(err)?;
    Ok(Json(serde_json::to_value(progress).unwrap_or(json!({}))))
}

async fn autostart_get_handler() -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let enabled = commands::autostart_get_core().map_err(err)?;
    Ok(Json(json!({ "enabled": enabled })))
}

async fn autostart_set_handler(Json(payload): Json<serde_json::Value>) -> Result<impl IntoResponse, (StatusCode, String)> {
    let enabled = payload["enabled"].as_bool().unwrap_or(false);
    commands::autostart_set_core(enabled).map_err(err)?;
    Ok(Json(json!({ "ok": true })))
}
```

- [ ] **Step 5: 编译 + 单元测试**

Run: `cd src-tauri && cargo check && cargo test`
Expected: 编译通过；测试全绿。若 `start_instance_core`/`stop_instance_core` 为同步函数，Step 3 直接调用即可（无需 spawn_blocking）。

- [ ] **Step 6: 提交**

```bash
git add src-tauri/src/server.rs src-tauri/src/lib.rs
git commit -m "feat(server): 新增 axum HTTP 层（/api 全量路由 + 静态托管 + CORS）"
```

---

### Task 8: 桌面模式内嵌启动本地 HTTP 服务

**Files:**
- Modify: `src-tauri/src/lib.rs`

- [ ] **Step 1: run() 中启动内嵌 HTTP 线程**

在 lib.rs `run()` 中 `.setup(...)` 回调内、`app.manage(AppState { core })` 之后追加：

```rust
            // 启动本地管理 HTTP 服务（桌面与 headless 共用同一 API）
            let core_for_http = core.clone();
            tauri::async_runtime::spawn(async move {
                let _ = server::serve(&format!("127.0.0.1:{}", local_http_port()), core_for_http).await;
            });
```

并新增辅助函数（lib.rs 底部）：

```rust
/// 本地管理 HTTP 端口（桌面模式固定 19090；后续 M2 配置化）
fn local_http_port() -> u16 {
    std::env::var("OPCODE2API_HTTP_PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(19090)
}
```

- [ ] **Step 2: 确认窗口加载地址不变**

`tauri.conf.json` 的 frontendDist/main 保持 `../dist` 与 `index.html` 不变（桌面仍加载打包前端；前端内部 fetch 到 127.0.0.1:19090）。

- [ ] **Step 3: 验证桌面模式整体编译**

Run: `cd src-tauri && cargo check`
Expected: 编译通过（`tauri::async_runtime::spawn` 已在依赖中；若否，改用 `std::thread::spawn` + `tokio::runtime::Runtime` 亦可，但首选前者）。

- [ ] **Step 4: 提交**

```bash
git add src-tauri/src/lib.rs
git commit -m "feat: 桌面模式内嵌启动本地 HTTP 服务（127.0.0.1:19090）"
```

---

### Task 9: 前端 api.ts 全量切换 invoke → fetch

**Files:**
- Modify: `src/lib/api.ts`（类型定义 1-209 行保留不动）

- [ ] **Step 1: 写 fetch 基座**

在 api.ts 中定义：

```typescript
// ---- HTTP 基座（headless 与桌面共用）----
const BASE = import.meta.env.DEV ? "/api" : "/api";

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { "Content-Type": "application/json", ...(options?.headers ?? {}) },
    ...options,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text || `HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}

export const http = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
};
```

- [ ] **Step 2: 将 api 对象逐方法替换为 fetch 实现**

保留 `api` 对象的方法名与签名**完全不变**（前端各页面 import 不动），仅将内部 `invoke(...)` 替换为 `http.get/http.post`。对照表：

| 原 invoke 调用 | 替换为 |
|---|---|
| `invoke("list_instances")` | `http.get<Instance[]>("/instances")` |
| `invoke("add_instance", { name, port, node, password })` | `http.post<Instance>("/instances", { name, port, node, password })` |
| `invoke("remove_instance", { name })` | `http.post<null>(\`/instances/${name}/remove\`)` |
| `invoke("start_instance", { name })` | `http.post<null>(\`/instances/${name}\`)` |
| `invoke("stop_instance", { name })` | `http.post<null>(\`/instances/${name}/stop\`)` |
| `invoke("gateway_status")` | `http.get<GatewayStatus>("/gateway")` |
| `invoke("gateway_stop")` | `http.post<null>("/gateway/stop")` |
| `invoke("gateway_set_route_mode", { mode })` | `http.post<null>("/gateway/route-mode", { mode })` |
| `invoke("config_get")` | `http.get<ConfigView>("/config")` |
| `invoke("config_set", { key, value })` | `http.post<null>(\`/config/${key}\`, { value })` |
| `invoke("get_stats")` | `http.get<StatsSummary>("/stats")` |
| `invoke("get_call_log", { limit })` | `http.get<CallLogRecord[]>(\`/call-log?limit=${limit ?? ""}\`)` |
| `invoke("list_nodes")` | `http.get<NodeView[]>("/nodes")` |
| `invoke("get_binaries_info")` | `http.get<BinariesInfo>("/binaries")` |
| `invoke("scan_start", { nodes, api_port, socks_port, timeout })` | `http.post<ScanProgress>("/scan/start", { nodes, api_port, socks_port, timeout })` |
| `invoke("scan_status")` | `http.get<ScanProgress>("/scan/status")` |
| `invoke("scan_stop")` | `http.post<ScanProgress>("/scan/stop")` |
| `invoke("autostart_get")` | `http.get<{ enabled: boolean }>("/autostart")` |
| `invoke("autostart_set", { enabled })` | `http.post<null>("/autostart", { enabled })` |

批量命令（batch_add/batch_start/batch_stop/batch_delete/restart_pool）需在 server.rs 补路由（Task 7 若未包含则本 Task 一并补）：
- `POST /api/instances/batch`（batch_add）、`POST /api/instances/batch/start`、`POST /api/instances/batch/stop`、`DELETE /api/instances/batch`、`POST /api/instances/restart-pool`

`port_suggest` → `GET /api/port/suggest`；`port_check` → `GET /api/port/check?port=xxxx`。

- [ ] **Step 3: 删除 invoke 相关 import 与残留**

替换完成后，移除 `import { invoke } from "@tauri-apps/api/core"`；用 `grep -rn "invoke" src/` 确认无残留（`api` 对象方法名不含 invoke 字样即可）。

- [ ] **Step 4: 前端构建验证**

Run: `cd frontend-dir && npm run build`（或项目实际构建命令，参考 package.json scripts）
Expected: tsc + vite build 通过。若某页面仍引用未替换的 invoke，会在此步暴露。

- [ ] **Step 5: 提交**

```bash
git add src/lib/api.ts
git commit -m "refactor(ui): api.ts 全量切换 invoke → fetch（桌面/headless 共用 HTTP 层）"
```

---

### Task 10: 前端非 Tauri 环境适配（TitleBar + Vite 代理）

**Files:**
- Modify: `src/components/TitleBar.tsx`
- Modify: `vite.config.ts`

- [ ] **Step 1: TitleBar 非 Tauri 环境隐藏窗口按钮**

在 TitleBar.tsx 中，将窗口按钮（最小化/最大化/关闭）渲染条件改为：

```tsx
const [isTauri, setIsTauri] = useState(false);
useEffect(() => {
  (async () => {
    try {
      // 非 Tauri 环境（纯浏览器）getCurrentWindow 会抛错
      await getCurrentWindow();
      setIsTauri(true);
    } catch {
      setIsTauri(false);
    }
  })();
}, []);
```

窗口按钮区块：`{isTauri && (<>...</>)}`。其余布局逻辑保持不变。

- [ ] **Step 2: vite.config.ts 添加 /api 代理**

```ts
export default defineConfig({
  base: "./",
  plugins: [react()],
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:19090",
        changeOrigin: true,
      },
    },
  },
});
```

- [ ] **Step 3: 验证**

Run: `npm run build`
Expected: 构建通过。dev 模式下 `npm run dev` 后浏览器访问 `http://localhost:5173`，`/api` 请求经代理到 19090。

- [ ] **Step 4: 提交**

```bash
git add src/components/TitleBar.tsx vite.config.ts
git commit -m "feat(ui): 非 Tauri 环境隐藏窗口按钮 + vite /api 代理（headless 开发支持）"
```

---

### Task 11: main.rs 子命令分发 + headless serve 入口

**Files:**
- Modify: `src-tauri/src/main.rs`

- [ ] **Step 1: 子命令分发**

将 main.rs 改写为：

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() > 1 && args[1] == "serve" {
        // headless 模式：--port 指定端口，默认 19090
        let port = args
            .iter()
            .position(|a| a == "--port")
            .and_then(|i| args.get(i + 1))
            .and_then(|p| p.parse::<u16>().ok())
            .or_else(|| std::env::var("OPCODE2API_HTTP_PORT").ok().and_then(|p| p.parse().ok()))
            .unwrap_or(19090);
        headless_main(port);
    } else {
        tauri_main();
    }
}

/// headless 入口：仅启动 HTTP 服务，不创建窗口
fn headless_main(port: u16) {
    use opencode2api_manager_lib::core::AppCore;
    use opencode2api_manager_lib::server;
    use std::sync::Arc;

    let core = Arc::new(AppCore::new());
    let rt = tokio::runtime::Runtime::new().expect("无法创建运行时");
    if let Err(e) = rt.block_on(server::serve(&format!("0.0.0.0:{}", port), core)) {
        eprintln!("Headless 服务启动失败: {}", e);
        std::process::exit(1);
    }
}

/// 桌面入口：原 tauri::Builder 逻辑整体保留
fn tauri_main() {
    // —— 原 main() 中的 tauri::Builder 代码原样保留 ——
}
```

> 注：headless 监听 `0.0.0.0` 便于容器/远程访问；如需仅本机，改为 `127.0.0.1`。若 crate 名与 lib 目标名不同，以 `src-tauri/Cargo.toml` 的 `[lib] name` 为准调整 use 路径。

- [ ] **Step 2: 桌面模式（tauri_main）编译验证**

Run: `cd src-tauri && cargo check`
Expected: 编译通过。

- [ ] **Step 3: headless 模式手工验证**

Run: `cd src-tauri && cargo build && ./target/debug/<crate-name> serve --port 19090`
Expected: 终端输出「Headless 管理服务已启动」，随后：

```bash
curl -s http://127.0.0.1:19090/api/health   # 期望 {"ok":true,...}
curl -s http://127.0.0.1:19090/api/instances # 期望 JSON 数组
```

- [ ] **Step 4: 提交**

```bash
git add src-tauri/src/main.rs
git commit -m "feat: main.rs 子命令分发，新增 headless serve 入口"
```

---

### Task 12: 配置项扩展（网关密钥/端口 + HTTP 端口 + 订阅/巡检参数）

**Files:**
- Modify: `src-tauri/src/config.rs`
- Modify: `src-tauri/src/gateway.rs`

- [ ] **Step 1: Config 结构新增字段**

在 config.rs 的 `Config` 结构体末尾追加（全部带 `#[serde(default)]` 保证向后兼容）：

```rust
    /// 统一网关监听端口（默认 18080）
    #[serde(default = "default_gateway_port")]
    pub gateway_port: u16,
    /// 统一网关鉴权密钥（默认 "sk-unified-local"）
    #[serde(default = "default_gateway_key")]
    pub gateway_key: String,
    /// 管理 HTTP 服务端口（桌面/headless 共用，默认 19090）
    #[serde(default = "default_http_port")]
    pub http_port: u16,
    /// 订阅 URL（可选）
    #[serde(default)]
    pub subscribe_url: String,
    /// 订阅自动拉取间隔分钟（0 表示不自动）
    #[serde(default)]
    pub subscribe_interval_min: u32,
    /// 健康巡检间隔秒（0 表示关闭）
    #[serde(default)]
    pub health_check_interval_sec: u32,
    /// 健康巡检失败 N 次自动重启（0 表示不自动重启）
    #[serde(default)]
    pub health_restart_threshold: u32,
    /// 日志过滤关键词（逗号分隔）
    #[serde(default)]
    pub log_filter_keywords: String,
```

并新增默认值函数：

```rust
fn default_gateway_port() -> u16 { 18080 }
fn default_gateway_key() -> String { "sk-unified-local".to_string() }
fn default_http_port() -> u16 { 19090 }
```

同时将原 `UNIFIED_GATEWAY_PORT`/`UNIFIED_GATEWAY_KEY` 常量从 gateway.rs 移除，改由 Config 驱动（保留 debug/release 差异的处理：`gateway_port` 默认值若需区分 debug（21080）/release（18080），在 gateway.rs 读取处做 `#[cfg(debug_assertions)]` 分支，默认值函数内不可用 cfg 时采用「加载后覆写」策略——见 Step 3）。

- [ ] **Step 2: ConfigView / config_get_core / config_set_core 同步扩展**

在 commands.rs 的 `ConfigView` 结构体中追加对应字段（与 Config 新增字段一一对应：`gateway_port: u16`、`gateway_key: String`、`has_gateway_key: bool`、`http_port: u16`、`subscribe_url: String`、`subscribe_interval_min: u32`、`health_check_interval_sec: u32`、`health_restart_threshold: u32`、`log_filter_keywords: String`）。`config_get_core` 填充新字段；`config_set_core` 的 `set()` 匹配分支补充新键（gateway_port/http_port/health_check_interval_sec/health_restart_threshold/subscribe_interval_min 走 u16/u32 解析，subscribe_url/log_filter_keywords 走字符串，gateway_key 走「非空才允许覆盖」校验）。

- [ ] **Step 3: gateway.rs 使用配置值**

在 gateway.rs 中，`GatewayManager::new` 或 `sync()` 处读取配置：

```rust
use crate::config::Config;

// 在 GatewayManager 初始化时：
let config = Config::load().unwrap_or_default();
let gateway_port = if cfg!(debug_assertions) && !config.gateway_port_set_explicitly() {
    21080 // debug 默认
} else {
    config.gateway_port
};
```

> 简化方案（推荐）：不区分 debug/release 端口，统一 `config.gateway_port`（默认 18080）。若既有测试依赖 21080 端口，则保留 debug 分支。以 `cargo test` 结果为准调整。

密钥读取：`config.gateway_key.clone()` 替换原常量 `UNIFIED_GATEWAY_KEY`。

- [ ] **Step 4: 同步测试更新**

Run: `cd src-tauri && cargo test`
Expected: 全绿。若 gateway 端口测试硬编码 21080/18080，更新为读取配置后仍为默认值（断言改为 `Config::load().unwrap_or_default().gateway_port`）。

- [ ] **Step 5: 提交**

```bash
git add src-tauri/src/config.rs src-tauri/src/gateway.rs src-tauri/src/commands.rs
git commit -m "feat(config): 新增网关端口/密钥、HTTP 端口、订阅/巡检/日志过滤配置项"
```

---

### Task 13: 前端配置页适配新字段 + 网关设置入口

**Files:**
- Modify: `src/pages/SettingsPage.tsx`
- Modify: `src/lib/api.ts`

- [ ] **Step 1: ConfigView 类型补字段**

在 api.ts 中 `ConfigView` 接口追加：

```typescript
gateway_port: number;
gateway_key: string;
has_gateway_key: boolean;
http_port: number;
subscribe_url: string;
subscribe_interval_min: number;
health_check_interval_sec: number;
health_restart_threshold: number;
log_filter_keywords: string;
```

- [ ] **Step 2: SettingsPage 增加配置表单**

在 SettingsPage 的「通用配置」区追加新字段输入（与既有配置项表单风格一致）：

- 网关端口（number input，保存键 `gateway_port`）
- 网关密钥（password input + 「显示」切换；保存键 `gateway_key`；`has_gateway_key` 为 true 时显示「已设置」占位而非明文，密钥为空且 has_gateway_key 时不覆盖）
- 管理 HTTP 端口（number input，保存键 `http_port`，提示「修改后需重启服务生效」）
- 订阅 URL（text input，保存键 `subscribe_url`）
- 订阅自动拉取间隔（number input，分钟，0=不自动，保存键 `subscribe_interval_min`）
- 健康巡检间隔（number input，秒，0=关闭，保存键 `health_check_interval_sec`）
- 自动重启阈值（number input，次，0=不自动，保存键 `health_restart_threshold`）
- 日志过滤关键词（text input，逗号分隔，保存键 `log_filter_keywords`）

保存逻辑复用既有 `api.config_set(key, value)`（已切 fetch 版本）。

- [ ] **Step 3: 构建验证**

Run: `npm run build`
Expected: 构建通过。

- [ ] **Step 4: 提交**

```bash
git add src/pages/SettingsPage.tsx src/lib/api.ts
git commit -m "feat(ui): 配置页新增网关端口/密钥、HTTP 端口、订阅/巡检/日志过滤字段"
```

---

### Task 14: embed.rs 平台化内嵌二进制

**Files:**
- Modify: `src-tauri/src/embed.rs`

- [ ] **Step 1: 按平台选择内嵌资源**

将 `include_bytes!` 的目标改为平台化选择：

```rust
// Windows: 内嵌 .exe；Linux/macOS: 内嵌无后缀二进制
#[cfg(target_os = "windows")]
const OPENCODE2API_BIN: &[u8] = include_bytes!("../resources/opencode2api.exe");
#[cfg(not(target_os = "windows"))]
const OPENCODE2API_BIN: &[u8] = include_bytes!("../resources/opencode2api");

#[cfg(target_os = "windows")]
const SINGBOX_BIN: &[u8] = include_bytes!("../resources/sing-box.exe");
#[cfg(not(target_os = "windows"))]
const SINGBOX_BIN: &[u8] = include_bytes!("../resources/sing-box");
```

`ensure_binaries` 中写文件的目标文件名同步平台化：

```rust
let oc_name = if cfg!(windows) { "opencode2api.exe" } else { "opencode2api" };
let sb_name = if cfg!(windows) { "sing-box.exe" } else { "sing-box" };
```

> 注：Linux 构建机器需在 `src-tauri/resources/` 放置 Linux 版二进制（编译脚本/CI 负责）；Windows 构建仍用 .exe。若当前仓库仅含 .exe 资源，先确认 Linux 二进制来源（CI 下载或本地构建），本 Task 仅保证代码层支持双平台。

- [ ] **Step 2: 编译验证**

Run: `cd src-tauri && cargo check`
Expected: 编译通过（若 resources 目录缺 Linux 二进制，`include_bytes!` 会在当前平台编译时报「文件不存在」——此时在 resources 放一个占位空文件先过编译，或按实际交付流程处理）。

- [ ] **Step 3: 提交**

```bash
git add src-tauri/src/embed.rs
git commit -m "feat: embed.rs 按平台选择内嵌二进制（Windows .exe / Linux 无后缀）"
```

---

### Task 15: instance.rs 二进制文件名平台化

**Files:**
- Modify: `src-tauri/src/instance.rs`

- [ ] **Step 1: 平台化二进制名**

搜索 instance.rs 中硬编码的 `"opencode2api.exe"` / `"sing-box.exe"` / `"sing-box"` / `"opencode2api"` 字符串，全部改为平台化辅助：

```rust
/// 平台对应的 opencode2api 二进制文件名
pub fn oc_binary_name() -> &'static str {
    if cfg!(windows) { "opencode2api.exe" } else { "opencode2api" }
}

/// 平台对应的 sing-box 二进制文件名
pub fn singbox_binary_name() -> &'static str {
    if cfg!(windows) { "sing-box.exe" } else { "sing-box" }
}
```

（若这些辅助函数更贴合放在 instance.rs，则放 instance.rs 并 `pub`；embed.rs 亦可复用。）将 `spawn_manager`/`spawn_singbox` 等处的硬编码替换为上述函数调用。`no_window()` 已有 `cfg!(windows)` 门控，保持不变。

- [ ] **Step 2: 编译 + 测试**

Run: `cd src-tauri && cargo check && cargo test`
Expected: 全绿。

- [ ] **Step 3: 提交**

```bash
git add src-tauri/src/instance.rs
git commit -m "refactor: instance.rs 二进制文件名平台化"
```

---

### Task 16: CI 增加 Linux 构建 job

**Files:**
- Modify: `.github/workflows/build-release.yml`

- [ ] **Step 1: 增加 ubuntu job**

在既有（windows）job 旁新增：

```yaml
  build-linux:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - name: 安装系统依赖
        run: |
          sudo apt-get update
          sudo apt-get install -y libwebkit2gtk-4.1-dev build-essential \
            curl wget file libxdo-dev libssl-dev libayatana-appindicator3-dev \
            librsvg2-dev patchelf
      - name: 安装 Rust (stable)
        uses: dtolnay/rust-toolchain@stable
      - uses: Swatinem/rust-cache@v2
        with:
          workspaces: src-tauri
      - name: 安装前端依赖并构建
        working-directory: ./
        run: |
          npm ci
          npm run build
      - name: 下载/放置 Linux 版 opencode2api 与 sing-box 资源
        run: |
          # TODO: 替换为实际资源获取方式（curl 下载 release 或解压）
          mkdir -p src-tauri/resources
          cp <oc-linux-binary> src-tauri/resources/opencode2api
          cp <sb-linux-binary> src-tauri/resources/sing-box
          chmod +x src-tauri/resources/opencode2api src-tauri/resources/sing-box
      - name: 构建 Tauri 应用
        working-directory: src-tauri
        run: cargo tauri build
      - name: 上传产物
        uses: actions/upload-artifact@v4
        with:
          name: opencode2api-manager-linux
          path: src-tauri/target/release/opencode2api-manager
      - name: 上传 deb/rpm
        uses: actions/upload-artifact@v4
        with:
          name: opencode2api-manager-linux-packages
          path: src-tauri/target/release/bundle/deb/*.deb
          if-no-files-found: ignore
```

> 注：Linux 二进制资源获取方式（`<oc-linux-binary>`）取决于 opencode2api 的发布形态，实施时需确认并填充实际 URL 或构建步骤。若资源在 CI 内可构建，改为 build 步骤。

- [ ] **Step 2: 验证 workflow 语法**

Run: `npx actionlint .github/workflows/build-release.yml`（若未安装 actionlint 则用 `python -c` 做基本 YAML 解析，或跳过仅目检）
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
git add .github/workflows/build-release.yml
git commit -m "ci: 增加 ubuntu-22.04 构建 job（Linux 桌面产物 + deb/rpm）"
```

---

### Task 17: 订阅拉取功能（subscribe.rs）

**Files:**
- Create: `src-tauri/src/subscribe.rs`
- Modify: `src-tauri/src/lib.rs`、`src-tauri/src/commands.rs`

- [ ] **Step 1: 写 subscribe.rs**

```rust
//! 订阅拉取与解析：Clash YAML / V2Ray base64 / 普通链接

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use std::collections::BTreeMap;

/// 拉取并解析订阅，返回节点列表
pub fn fetch_subscription(url: &str) -> Result<Vec<SubscribeNode>, String> {
    let resp = reqwest::blocking::get(url)
        .map_err(|e| format!("订阅拉取失败: {}", e))?;
    if !resp.status().is_success() {
        return Err(format!("订阅拉取失败: HTTP {}", resp.status()));
    }
    let body = resp.text().map_err(|e| format!("读取订阅内容失败: {}", e))?;
    parse_subscription(&body)
}

/// 解析订阅内容（自动识别 Clash YAML / base64 / 明文链接）
pub fn parse_subscription(body: &str) -> Result<Vec<SubscribeNode>, String> {
    let trimmed = body.trim();
    if trimmed.starts_with("proxies:") || trimmed.contains("proxies:") && trimmed.contains("type:") {
        parse_clash_yaml(trimmed)
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

/// 订阅节点（轻量结构，后续可落为实例）
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct SubscribeNode {
    pub name: String,
    pub server: String,
    pub port: u16,
    pub node_type: String,
    pub password: Option<String>,
    pub uuid: Option<String>,
    pub raw: String,
}

fn is_base64_like(s: &str) -> bool {
    s.len() > 40 && s.chars().take(60).all(|c| c.is_ascii_alphanumeric() || c == '+' || c == '/' || c == '=' || c == '-')
}

fn parse_clash_yaml(s: &str) -> Result<Vec<SubscribeNode>, String> {
    // 复用/简化 clash_yaml.rs 的解析思路：提取 proxies 段的 name/server/port/type/password/uuid
    // 若 clash_yaml.rs 已有通用解析函数则直接复用；否则在此做轻量解析
    // （实施时参考 src-tauri/src/clash_yaml.rs 的既有实现，避免重复造轮子）
    todo!("参考 clash_yaml.rs 实现 proxies 段解析")
}

fn parse_plain_links(s: &str) -> Result<Vec<SubscribeNode>, String> {
    // 逐行解析 vmess:// vless:// trojan:// ss:// ssr:// 链接
    // （v2rayN/v2rayNG 等标准订阅格式；实施时参考 singbox.rs 或网上标准解析）
    todo!("解析 v2ray 系列链接")
}

/// 批量导入订阅节点为实例（name 冲突自动加序号）
pub fn import_subscription(core: &crate::core::AppCore, url: &str) -> Result<usize, String> {
    let nodes = fetch_subscription(url)?;
    let mut mgr = core.manager.lock().map_err(|_| "状态锁失败".to_string())?;
    let existing: std::collections::HashSet<String> =
        mgr.list_instances().iter().map(|i| i.name.clone()).collect();
    let mut imported = 0usize;
    for node in nodes {
        let mut name = node.name;
        if existing.contains(&name) {
            let mut i = 2;
            while existing.contains(&format!("{} ({})", node.name, i)) { i += 1; }
            name = format!("{} ({})", node.name, i);
        }
        let ip = crate::commands::node_ip(&node.server);
        mgr.add_instance(name.clone(), node.port, node.raw, node.password.unwrap_or_default(), ip)
            .map_err(|e| e.to_string())?;
        existing.insert(name);
        imported += 1;
    }
    mgr.save_state().map_err(|e| e.to_string())?;
    drop(mgr);
    crate::commands::sync_gateway_core(core);
    Ok(imported)
}
```

- [ ] **Step 2: 注册模块 + command**

lib.rs 模块声明加 `pub mod subscribe;`。commands.rs 新增：

```rust
/// 拉取订阅并返回节点预览（不落库）
#[tauri::command]
pub fn subscribe_preview(url: String) -> Result<Vec<crate::subscribe::SubscribeNode>, String> {
    crate::subscribe::fetch_subscription(&url)
}

/// 拉取订阅并批量导入为实例
#[tauri::command]
pub fn subscribe_import(state: tauri::State<'_, AppState>, url: String) -> Result<usize, String> {
    crate::subscribe::import_subscription(&state.core, &url)
}
```

对应抽取 core 函数：`subscribe_preview_core(url) -> Result<Vec<SubscribeNode>, String>`（同 `subscribe_preview` 体）、`subscribe_import_core(core, url) -> Result<usize, String>`（同 `subscribe_import` 体）。

- [ ] **Step 3: server.rs 补路由**

```rust
.route("/api/subscribe/preview", post(subscribe_preview_handler))
.route("/api/subscribe/import", post(subscribe_import_handler))
```

handler 调 `*_core()` 版本。

- [ ] **Step 4: 前端入口**

- api.ts：`subscribePreview: (url) => http.post<SubscribeNode[]>("/subscribe/preview", { url })`、`subscribeImport: (url) => http.post<number>("/subscribe/import", { url })`、`SubscribeNode` 类型导出
- InstancesPage 增加「从订阅导入」按钮（Modal：输入 URL → 预览节点列表 → 确认导入；确认后刷新实例列表）。若 M3 时间紧，可先做「直接导入」按钮（仅 URL 输入 + 导入）。

- [ ] **Step 5: 编译 + 测试 + 前端构建**

Run: `cd src-tauri && cargo check && cargo test && npm run build`
Expected: 全绿（`todo!()` 占位在实施时全部实现，禁止残留）。

- [ ] **Step 6: 提交**

```bash
git add src-tauri/src/subscribe.rs src-tauri/src/lib.rs src-tauri/src/commands.rs src-tauri/src/server.rs src/lib/api.ts src/pages/InstancesPage.tsx
git commit -m "feat: 订阅拉取与批量导入（Clash YAML / base64 / v2ray 链接）"
```

---

### Task 18: 健康巡检（health.rs）

**Files:**
- Create: `src-tauri/src/health.rs`
- Modify: `src-tauri/src/lib.rs`、`src-tauri/src/commands.rs`

- [ ] **Step 1: 写 health.rs**

```rust
//! 后台健康巡检：周期探测实例 API 可达性，失败达阈值自动重启。

use crate::core::AppCore;
use crate::config::Config;
use std::sync::Arc;
use std::time::{Duration, Instant};

/// 单实例健康记录（持久化于 health.json）
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct HealthRecord {
    pub name: String,
    pub healthy: bool,
    pub last_check_ts: i64,
    pub consecutive_failures: u32,
    pub last_error: Option<String>,
}

/// 巡检汇总（前端展示）
#[derive(Debug, Default, serde::Serialize)]
pub struct HealthSummary {
    pub total: usize,
    pub healthy: usize,
    pub unhealthy: usize,
    pub records: Vec<HealthRecord>,
    pub last_scan_ts: i64,
}

fn health_file_path() -> std::path::PathBuf {
    let (_, _, runtime_dir) = crate::commands::manager_paths();
    runtime_dir.join("health.json")
}

fn load_records() -> Vec<HealthRecord> {
    std::fs::read_to_string(health_file_path())
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

fn save_records(records: &[HealthRecord]) {
    if let Ok(json) = serde_json::to_string_pretty(records) {
        let _ = std::fs::write(health_file_path(), json);
    }
}

/// 单轮巡检：探测运行中实例的 /api 端口（TTL 1s）
pub fn run_health_check_once(core: &AppCore) -> HealthSummary {
    let mut records = load_records();
    let mut by_name: std::collections::HashMap<String, HealthRecord> =
        records.drain(..).map(|r| (r.name.clone(), r)).collect();

    let instances: Vec<crate::instance::Instance> = core
        .manager
        .lock()
        .map(|m| m.list_instances().to_vec())
        .unwrap_or_default();

    let mut summary = HealthSummary::default();
    let now = chrono::Utc::now().timestamp();
    summary.last_scan_ts = now;

    for inst in instances.iter().filter(|i| i.status == "running") {
        let url = format!("http://127.0.0.1:{}/api/ping", inst.port);
        let healthy = reqwest::blocking::Client::builder()
            .timeout(Duration::from_secs(1))
            .build()
            .ok()
            .and_then(|c| c.get(&url).send().ok())
            .map(|r| r.status().is_success())
            .unwrap_or(false);

        let prev = by_name.entry(inst.name.clone()).or_insert(HealthRecord {
            name: inst.name.clone(),
            healthy: true,
            last_check_ts: now,
            consecutive_failures: 0,
            last_error: None,
        });
        prev.last_check_ts = now;
        if healthy {
            prev.healthy = true;
            prev.consecutive_failures = 0;
            prev.last_error = None;
        } else {
            prev.healthy = false;
            prev.consecutive_failures += 1;
            prev.last_error = Some("API 探测超时或无响应".to_string());
        }
    }

    // 依据配置自动重启
    let config = Config::load().unwrap_or_default();
    let threshold = config.health_restart_threshold;
    let mut to_restart: Vec<String> = Vec::new();
    for rec in by_name.values() {
        if threshold > 0 && rec.consecutive_failures >= threshold {
            to_restart.push(rec.name.clone());
        }
    }
    for name in to_restart {
        let _ = crate::commands::stop_instance_core(core, &name);
        let _ = crate::commands::start_instance_core(core, &name);
        if let Some(rec) = by_name.get_mut(&name) {
            rec.consecutive_failures = 0;
        }
    }

    let mut records_out: Vec<HealthRecord> = by_name.into_values().collect();
    records_out.sort_by(|a, b| a.name.cmp(&b.name));
    summary.total = instances.iter().filter(|i| i.status == "running").count();
    summary.healthy = records_out.iter().filter(|r| r.healthy).count();
    summary.unhealthy = records_out.iter().filter(|r| !r.healthy).count();
    summary.records = records_out.clone();
    save_records(&records_out);
    summary
}

/// 后台巡检循环（由 run() 按配置间隔 spawn）
pub async fn health_loop(core: Arc<AppCore>) {
    loop {
        let interval = Config::load()
            .unwrap_or_default()
            .health_check_interval_sec;
        if interval == 0 {
            tokio::time::sleep(Duration::from_secs(30)).await;
            continue;
        }
        let started = Instant::now();
        let core2 = core.clone();
        let _ = tokio::task::spawn_blocking(move || run_health_check_once(&core2)).await;
        let elapsed = started.elapsed().as_secs();
        let sleep = Duration::from_secs(interval.saturating_sub(elapsed));
        tokio::time::sleep(sleep).await;
    }
}
```

> 注：`chrono` 若未在依赖中，改用 `std::time::SystemTime` 取秒时间戳（`SystemTime::now().duration_since(UNIX_EPOCH)`）。探测端点 `/api/ping` 是 opencode2api 管理端口的健康端点——实施时确认实例管理 API 实际健康端点路径，否则用 TCP connect（`std::net::TcpStream::connect_timeout`）代替 HTTP GET。

- [ ] **Step 2: 注册模块 + command**

lib.rs 模块声明加 `pub mod health;`。commands.rs 新增：

```rust
/// 立即执行一轮健康巡检
#[tauri::command]
pub fn health_check_now() -> crate::health::HealthSummary {
    crate::health::run_health_check_once(&crate::core::AppCore::new())
}

/// 读取最近一次巡检汇总
#[tauri::command]
pub fn health_summary() -> crate::health::HealthSummary {
    crate::health::run_health_check_once(&crate::core::AppCore::new())
}
```

> 注：`health_check_now` 每次 new AppCore 开销大。改为抽取 core 函数并接受 `&AppCore`（command 用 `state.core`），server handler 亦复用：

```rust
pub fn health_check_now_core(core: &AppCore) -> crate::health::HealthSummary {
    crate::health::run_health_check_once(core)
}
```

- [ ] **Step 3: run() 启动后台巡检循环**

lib.rs `run()` 中追加：

```rust
            // 后台健康巡检（按配置间隔；配置为 0 时不巡检）
            let core_for_health = core.clone();
            tauri::async_runtime::spawn(async move {
                crate::health::health_loop(core_for_health).await;
            });
```

- [ ] **Step 4: server.rs 补路由**

```rust
.route("/api/health/check", post(health_check_handler))
.route("/api/health/summary", get(health_summary_handler))
```

- [ ] **Step 5: 前端入口**

- api.ts：`healthCheck: () => http.post<HealthSummary>("/health/check")`、`healthSummary: () => http.get<HealthSummary>("/health/summary")`、`HealthSummary`/`HealthRecord` 类型导出
- StatsPage 增加「健康巡检」卡片：显示 total/healthy/unhealthy + 「立即巡检」按钮 + 记录列表（名称/状态/失败次数/最近检查时间/错误信息）

- [ ] **Step 6: 编译 + 测试 + 前端构建**

Run: `cd src-tauri && cargo check && cargo test && npm run build`
Expected: 全绿。

- [ ] **Step 7: 提交**

```bash
git add src-tauri/src/health.rs src-tauri/src/lib.rs src-tauri/src/commands.rs src-tauri/src/server.rs src/lib/api.ts src/pages/StatsPage.tsx
git commit -m "feat: 健康巡检（周期探测 + 失败自动重启 + health.json 持久化）"
```

---

### Task 19: 日志过滤与高级查询

**Files:**
- Modify: `src-tauri/src/call_log.rs`、`src-tauri/src/commands.rs`、`src/lib/api.ts`、`src/pages/LogsPage.tsx`

- [ ] **Step 1: call_log.rs 扩展查询**

在 call_log.rs 追加：

```rust
/// 过滤参数（前端日志页）
#[derive(Debug, Default, serde::Deserialize)]
pub struct CallLogFilter {
    pub instance: Option<String>,
    pub keyword: Option<String>,       // 消息内容包含
    pub status: Option<String>,        // ok / error / timeout
    pub limit: Option<usize>,
    pub offset: Option<usize>,
    pub from_ts: Option<i64>,
    pub to_ts: Option<i64>,
}

/// 按过滤条件读取日志
pub fn read_call_log_filtered(filter: &CallLogFilter) -> Vec<CallLogRecord> {
    let all = read_call_log();
    all.into_iter()
        .filter(|r| filter.instance.as_ref().map(|i| r.instance == *i).unwrap_or(true))
        .filter(|r| filter.keyword.as_ref().map(|k| r.message.contains(k)).unwrap_or(true))
        .filter(|r| {
            filter.status.as_ref().map(|s| match s.as_str() {
                "ok" => !has_issue(r),
                "error" => has_issue(r),
                _ => true,
            }).unwrap_or(true)
        })
        .filter(|r| filter.from_ts.map(|t| r.ts >= t).unwrap_or(true))
        .filter(|r| filter.to_ts.map(|t| r.ts <= t).unwrap_or(true))
        .skip(filter.offset.unwrap_or(0))
        .take(filter.limit.unwrap_or(100))
        .collect()
}

/// 日志聚合（按实例统计条数/错误数/最近时间）
pub fn call_log_aggregate() -> Vec<serde_json::Value> {
    let all = read_call_log();
    let mut map: std::collections::BTreeMap<String, (usize, usize, i64)> = Default::default();
    for r in &all {
        let e = map.entry(r.instance.clone()).or_insert((0, 0, 0));
        e.0 += 1;
        if has_issue(r) { e.1 += 1; }
        e.2 = e.2.max(r.ts);
    }
    map.into_iter()
        .map(|(instance, (total, errors, last_ts))| {
            serde_json::json!({ "instance": instance, "total": total, "errors": errors, "last_ts": last_ts })
        })
        .collect()
}
```

> 注：若 `read_call_log` 已有过滤能力（检查现有签名），以现有为准，仅补充聚合。`CallLogRecord` 字段名以现有定义为准（`instance`/`message`/`ts`/`status` 若不同则调整 filter 实现）。

- [ ] **Step 2: commands.rs 新 command**

```rust
/// 过滤查询调用日志
#[tauri::command]
pub fn call_log_filtered(filter: crate::call_log::CallLogFilter) -> Vec<crate::call_log::CallLogRecord> {
    crate::call_log::read_call_log_filtered(&filter)
}

/// 日志聚合统计
#[tauri::command]
pub fn call_log_aggregate() -> Vec<serde_json::Value> {
    crate::call_log::call_log_aggregate()
}
```

抽取 core 版本：`call_log_filtered_core(filter: &CallLogFilter)`、`call_log_aggregate_core()`（同函数体）。

- [ ] **Step 3: server.rs 补路由**

```rust
.route("/api/call-log/filtered", post(call_log_filtered_handler))
.route("/api/call-log/aggregate", get(call_log_aggregate_handler))
```

- [ ] **Step 4: 前端日志页增强**

- api.ts：`callLogFiltered(filter)`、`callLogAggregate()` + `CallLogFilter` 类型
- LogsPage 增加过滤栏：实例下拉（取自实例列表）、关键词输入、状态下拉（全部/正常/异常）、时间范围（可选）、分页（limit/offset）；增加「按实例汇总」切换视图（聚合表格：实例/总条数/错误数/最近时间）

- [ ] **Step 5: 构建 + 测试**

Run: `cd src-tauri && cargo check && cargo test && npm run build`
Expected: 全绿。

- [ ] **Step 6: 提交**

```bash
git add src-tauri/src/call_log.rs src-tauri/src/commands.rs src-tauri/src/server.rs src/lib/api.ts src/pages/LogsPage.tsx
git commit -m "feat: 调用日志过滤查询与按实例聚合统计"
```

---

### Task 20: 报表导出（CSV / JSON）

**Files:**
- Modify: `src-tauri/src/commands.rs`、`src-tauri/src/server.rs`、`src/lib/api.ts`、`src/pages/StatsPage.tsx`

- [ ] **Step 1: 导出核心函数**

在 commands.rs 追加：

```rust
/// 导出调用日志为 CSV 文本（调用日志页/统计页共用）
pub fn export_call_log_csv_core(limit: Option<usize>) -> Result<String, String> {
    let records = crate::call_log::read_call_log();
    let records: Vec<_> = records.into_iter().take(limit.unwrap_or(records.len())).collect();
    let mut wtr = csv_writer::Writer::from_writer(vec![]);
    // 若项目无 csv crate，手写 CSV（逗号转义 + 引号包裹）：
    let header = "ts,instance,status,api_key_prefix,node,message\n";
    let mut out = String::from(header);
    for r in records {
        out.push_str(&format!(
            "{},{},{},{},{},{}\n",
            r.ts,
            csv_escape(&r.instance),
            if crate::call_log::has_issue(&r) { "error" } else { "ok" },
            csv_escape(&r.api_key_prefix),
            csv_escape(&r.node),
            csv_escape(&r.message),
        ));
    }
    Ok(out)
}

fn csv_escape(s: &str) -> String {
    if s.contains(',') || s.contains('"') || s.contains('\n') {
        format!("\"{}\"", s.replace('"', "\"\""))
    } else {
        s.to_string()
    }
}

/// 导出全部实例快照为 JSON 文本
pub fn export_instances_json_core(core: &AppCore) -> Result<String, String> {
    let instances = list_instances_core(core)?;
    serde_json::to_string_pretty(&instances).map_err(|e| e.to_string())
}

/// 导出统计摘要为 JSON 文本
pub fn export_stats_json_core() -> Result<String, String> {
    let stats = get_stats_core()?;
    serde_json::to_string_pretty(&stats).map_err(|e| e.to_string())
}
```

> 注：`CallLogRecord` 字段若含 `api_key_prefix`/`node`，按实际字段名调整；若无这些字段则去掉对应列。

- [ ] **Step 2: command 薄壳 + server 路由**

commands.rs：

```rust
#[tauri::command]
pub fn export_call_log_csv(limit: Option<usize>) -> Result<String, String> {
    export_call_log_csv_core(limit)
}
#[tauri::command]
pub fn export_instances_json(state: tauri::State<'_, AppState>) -> Result<String, String> {
    export_instances_json_core(&state.core)
}
#[tauri::command]
pub fn export_stats_json() -> Result<String, String> {
    export_stats_json_core()
}
```

server.rs：

```rust
.route("/api/export/call-log.csv", get(export_csv_handler))
.route("/api/export/instances.json", get(export_instances_handler))
.route("/api/export/stats.json", get(export_stats_handler))
```

CSV handler 返回 `(headers, body)`：`Content-Type: text/csv` + `Content-Disposition: attachment; filename="call-log.csv"`（或前端下载由前端 blob 处理，后端仅返文本）。

- [ ] **Step 3: 前端下载入口**

- api.ts：`exportCallLogCsv(limit?)`（fetch 后以 blob 触发下载）、`exportInstancesJson()`、`exportStatsJson()`
- StatsPage / LogsPage 增加「导出 CSV / 导出 JSON」按钮（`const blob = new Blob([text]); const a = document.createElement("a"); a.href = URL.createObjectURL(blob); a.download = ...; a.click();`）

- [ ] **Step 4: 构建 + 测试**

Run: `cd src-tauri && cargo check && cargo test && npm run build`
Expected: 全绿。

- [ ] **Step 5: 提交**

```bash
git add src-tauri/src/commands.rs src-tauri/src/server.rs src/lib/api.ts src/pages/StatsPage.tsx src/pages/LogsPage.tsx
git commit -m "feat: 报表导出（日志 CSV / 实例 JSON / 统计 JSON）"
```

---

### Task 21: 自定义网关密钥（settings 页完成 + 配置热生效）

**Files:**
- Modify: `src-tauri/src/config.rs`、`src-tauri/src/gateway.rs`、`src-tauri/src/commands.rs`

- [ ] **Step 1: config_set 网关密钥校验与生效**

在 `config_set_core`（或 config.rs `set()`）中为 `gateway_key` 增加校验：

```rust
"gateway_key" => {
    if value.len() < 8 {
        return Err("网关密钥至少 8 个字符".to_string());
    }
    // 保存后重启网关使密钥生效
    config.gateway_key = value.to_string();
    // 触发统一网关重建（调用方：config_set 的 command 壳在保存后调 gateway_stop + sync）
}
```

在 commands.rs `config_set` command 薄壳中，保存成功后若 key == "gateway_key" 或 "gateway_port"，追加：

```rust
    if key == "gateway_key" || key == "gateway_port" {
        // 重启统一网关使新配置生效
        sync_gateway_core(&state.core);
    }
```

- [ ] **Step 2: gateway.rs 校验请求密钥**

在 gateway.rs 的请求鉴权处（查找现有鉴权逻辑——若有 `Authorization` 头校验或密钥比对），将常量比对替换为配置比对：

```rust
let config = crate::config::Config::load().unwrap_or_default();
if provided_key != config.gateway_key {
    return Err("网关密钥无效".to_string());
}
```

> 若 gateway.rs 当前无鉴权（仅密钥写在转发请求里），本步只做「转发用密钥=配置值」，鉴权属新增能力，按 YAGNI 边界判断——设计文档明确「自定义统一网关密钥」是功能点，需保证新密钥真正用于网关请求鉴权/转发。

- [ ] **Step 3: 前端密钥重置交互**

SettingsPage 网关密钥区：当 `has_gateway_key` 为 true 时显示「已设置（点击更换）」；点击后出现输入框，提交 `config_set("gateway_key", newKey)`；保存成功后提示「网关已重启生效」。

- [ ] **Step 4: 构建 + 测试**

Run: `cd src-tauri && cargo check && cargo test && npm run build`
Expected: 全绿。新增测试：设置短密钥返回错误；合法密钥保存后 `Config::load().gateway_key` 为新值。

- [ ] **Step 5: 提交**

```bash
git add src-tauri/src/config.rs src-tauri/src/gateway.rs src-tauri/src/commands.rs src/pages/SettingsPage.tsx
git commit -m "feat: 自定义统一网关密钥（校验 + 热生效 + 前端更换交互）"
```

---

### Task 22: 订阅自动拉取（后台定时）

**Files:**
- Modify: `src-tauri/src/subscribe.rs`、`src-tauri/src/lib.rs`

- [ ] **Step 1: subscribe_loop 后台任务**

在 subscribe.rs 追加：

```rust
/// 后台订阅循环：按配置间隔自动拉取并入实例
pub async fn subscribe_loop(core: Arc<AppCore>) {
    loop {
        let config = crate::config::Config::load().unwrap_or_default();
        let interval_min = config.subscribe_interval_min;
        let url = config.subscribe_url.clone();
        if interval_min > 0 && !url.is_empty() {
            let core2 = core.clone();
            let _ = tokio::task::spawn_blocking(move || {
                match import_subscription(&core2, &url) {
                    Ok(n) => println!("订阅自动拉取完成，导入 {} 个节点", n),
                    Err(e) => eprintln!("订阅自动拉取失败: {}", e),
                }
            })
            .await;
        }
        tokio::time::sleep(Duration::from_secs((interval_min.max(1) as u64) * 60)).await;
    }
}
```

- [ ] **Step 2: run() 启动订阅循环**

lib.rs `run()` 追加：

```rust
            // 后台订阅自动拉取（配置 interval>0 且 url 非空时启用）
            let core_for_sub = core.clone();
            tauri::async_runtime::spawn(async move {
                crate::subscribe::subscribe_loop(core_for_sub).await;
            });
```

- [ ] **Step 3: 编译 + 测试**

Run: `cd src-tauri && cargo check && cargo test`
Expected: 全绿。

- [ ] **Step 4: 提交**

```bash
git add src-tauri/src/subscribe.rs src-tauri/src/lib.rs
git commit -m "feat: 订阅自动拉取后台循环（按配置间隔）"
```

---

### Task 23: systemd 服务文件 + 部署文档

**Files:**
- Create: `docs/systemd/opencode2api-manager.service`
- Create: `docs/DEPLOYMENT.md`
- Modify: `README.md`

- [ ] **Step 1: systemd 服务文件**

创建 `docs/systemd/opencode2api-manager.service`：

```ini
[Unit]
Description=opencode2api Manager (headless)
After=network.target

[Service]
Type=simple
User=opencode2api
# 二进制路径按实际安装位置修改
ExecStart=/usr/local/bin/opencode2api-manager serve --port 19090
Restart=on-failure
RestartSec=5
# 数据目录（实例配置、运行态、日志均在此）
Environment=OPCODE2API_DATA_DIR=/var/lib/opencode2api
# 可选：限制仅本机访问（默认 0.0.0.0）
#Environment=OPCODE2API_BIND=127.0.0.1
# 日志
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: 部署文档**

创建 `docs/DEPLOYMENT.md`，覆盖：

1. **桌面模式（Linux）**：下载 .deb/AppImage → 安装 → 启动（自启可选）
2. **Headless 模式**：安装二进制 → `systemctl` 安装服务文件 → 配 `OPCODE2API_DATA_DIR` → 浏览器访问 `http://<host>:19090`
3. **配置说明**：`config.json` 全部字段（含 M2 新增字段）与 `OPCODE2API_*` 环境变量清单（`OPCODE2API_DATA_DIR`、`OPCODE2API_HTTP_PORT`、`OPCODE2API_BIND` 若实现）
4. **安全**：网关密钥设置、headless 默认监听 0.0.0.0 的风险、反向代理建议（nginx + 可选 TLS）
5. **systemd 运维**：`systemctl enable/start/status/logs`、开机自启

- [ ] **Step 3: README 增补**

README.md 增加「Linux / Headless 部署」章节，链接 `docs/DEPLOYMENT.md`，注明新增命令 `opencode2api-manager serve --port <port>`。

- [ ] **Step 4: 提交**

```bash
git add docs/systemd/opencode2api-manager.service docs/DEPLOYMENT.md README.md
git commit -m "docs: headless systemd 服务 + Linux/headless 部署文档"
```

---

### Task 24: 端到端回归 + 收尾

**Files:**
- 全仓验证

- [ ] **Step 1: Rust 全量测试**

Run: `cd src-tauri && cargo test`
Expected: 全绿。若有失败，修复后重跑，不跳过。

- [ ] **Step 2: 前端构建 + 静态检查**

Run: `npm run build`（tsc + vite）
Expected: 构建通过。

- [ ] **Step 3: headless 端到端冒烟**

Run: `./target/debug/opencode2api-manager serve --port 19090 &` 后执行：

```bash
curl -s http://127.0.0.1:19090/api/health
curl -s http://127.0.0.1:19090/api/instances
curl -s http://127.0.0.1:19090/api/gateway
curl -s http://127.0.0.1:19090/api/stats
curl -s http://127.0.0.1:19090/api/config
curl -s -X POST http://127.0.0.1:19090/api/autostart -d '{"enabled": false}'
curl -s -X POST http://127.0.0.1:19090/api/scan/start -d '{"nodes":["example.com:443"],"timeout":5}'
curl -s http://127.0.0.1:19090/api/scan/status
curl -s -X POST http://127.0.0.1:19090/api/scan/stop
```

Expected: 全部返回合法 JSON，无 404/500。浏览器打开 `http://127.0.0.1:19090` 页面正常渲染（静态托管 + API 联通）。

- [ ] **Step 4: 桌面模式冒烟**

Run: `cargo tauri dev`（Linux 桌面环境）或 `./target/debug/opencode2api-manager`
Expected: 窗口打开，实例列表/网关/统计页面数据正常，配置保存生效。

- [ ] **Step 5: 规格对照自查**

对照 `docs/superpowers/specs/2026-08-06-linux-webui-design.md` 逐条核对：

- [ ] Linux 适配（二进制平台化、自启、路径）
- [ ] 双入口（桌面 + headless serve）
- [ ] 配置化端口与网关密钥
- [ ] 订阅拉取/导入/自动拉取
- [ ] 健康巡检/自动重启
- [ ] 报表导出
- [ ] 日志过滤/聚合
- [ ] systemd 部署

- [ ] **Step 6: 最终提交**

```bash
git add -A
git commit -m "chore: M1-M4 回归收尾"  # 或按实际剩余变更拆分提交
```

---

## 自审清单（Self-Review Checklist）

- [ ] **计划完整性**：24 个任务覆盖规格书全部功能点；M1-M4 依赖顺序正确（先 core 抽取 → 后 HTTP 层 → 再功能增强 → 最后部署）
- [ ] **无占位符残留**：除 Task 16/17 中明确标注的「实施时填充/参考既有实现」外，无 `TODO`/`FIXME`/虚构 API
- [ ] **文件路径可验证**：所有 Modify 文件基于本仓库实际调研结果（config.rs/gateway.rs/instance.rs/embed.rs/commands.rs/lib.rs 均已读过代码确认）
- [ ] **兼容性**：Config 新增字段全部 `#[serde(default)]`；前端 api.ts 方法签名不变；桌面行为不回退
- [ ] **测试策略**：M1 每个 Task 均含 `cargo test` 验证；Task 24 为端到端回归
- [ ] **风险清单**：
  - `todo!()` 占位（Task 17 的 clash_yaml/v2ray 解析）必须在 Task 17 内完成，禁止残留
  - Linux 二进制资源获取方式（Task 16/14）依赖实际发布渠道，需实施时确认
  - 健康探测端点路径（Task 18）依赖实例管理 API 实际端点，用 TCP connect 兜底
  - chrono 依赖若未引入则用 SystemTime 替代
  - batch 路由（Task 9 Step 2）需在 Task 7/9 中补齐 server 路由
- [ ] **YAGNI 边界**：不做多用户、不做数据库持久化、不做移动端、不做集群调度

## Milestones

- **M1（Task 1-11）**：架构重构完成，桌面 + headless 双入口可用，前端走 HTTP
- **M2（Task 12-16）**：配置化 + Linux 适配 + CI，可在 Linux 构建发布
- **M3（Task 17-21）**：订阅拉取、健康巡检、日志过滤、报表导出、网关密钥
- **M4（Task 22-24）**：订阅自动拉取、systemd 部署文档、端到端回归
