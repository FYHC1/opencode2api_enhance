# opencode2api 桌面化改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 opencode2api 仓库改造成纯桌面 exe 应用「opencode2api 管理器」——完整迁移 opencode2api_enhance 的 Rust 多实例管理器功能，前端改为 React + Tailwind 并参照 Windsurf Account Manager 的浅色官网风格，删除 Docker/CI 等发布设施。

**Architecture:** Tauri 2 桌面应用。Rust 后端从 `opencode2api_enhance` 迁移 clash_yaml / config / opencode_cfg / singbox / instance / probe / embed 七个模块（axum Web API 全部改为 Tauri command，删除 axum 依赖），子进程管理 opencode2api.exe（Go 核心）+ sing-box.exe（通过 include_bytes 内嵌、运行时释放到 exe 旁 bin/ 目录）。前端为 React 19 + Vite 8 + Tailwind 4，单窗口 + 侧边栏三页（实例 / 节点扫描 / 设置）+ 自定义标题栏 + 托盘常驻。配置与运行时数据存 `%APPDATA%/opencode2api-manager/`。

**Tech Stack:** Rust（tauri 2 / tokio / serde / serde_json / anyhow / clap / sysinfo / dirs / serde_yaml）、TypeScript 6、React 19、Vite 8、Tailwind CSS 4、lucide-react、clsx、@tauri-apps/api 2。

## Global Constraints

- 目标仓库：`opencode2api/`（本仓库），**禁止修改** `windsurf-account-manager/source/` 与 `opencode2api_enhance/` 两个仓库。
- git：`origin` 远程改为 `upstream`（https://github.com/6Kmfi6HP/opencode2api.git，只拉取不推送）；新代码提交到当前仓库。
- 前端界面全中文；视觉风格必须参照 `windsurf-account-manager/source/src/index.css` 的设计 token（浅色 `#f4f4f5` 底、白卡片、teal `#0f766e` accent、Plus Jakarta Sans 字体、16px/12px 圆角）。
- 纯桌面：**不启动任何 HTTP 监听端口**（不保留 axum、不监听 9099）；子进程 opencode2api.exe 自身端口是代理服务端口，属正常功能。
- 二进制内嵌：`bin/opencode2api.exe`（9.9MB，从 enhance 复制）与 `bin/sing-box.exe`（43.3MB，从 enhance 复制）通过 `include_bytes!` 内嵌，运行时由 `embed::ensure_binaries` 释放到主程序旁的 `bin/` 目录。
- 实例模型：singbox_port = port + 10000；数据存 `%APPDATA%/opencode2api-manager/instances.json`；运行时目录 `%APPDATA%/opencode2api-manager/runtime/`。
- 删除文件：`.github/`、`deploy/`、`docker/`、`Dockerfile`、`.dockerignore`；保留 Go 源码（main.go 及各 _test.go、protocol go 文件、go.mod），仅作为被调用的子进程源码留档。
- 测试门槛：Rust 侧 `cargo test` 全部通过（迁移 enhance 的 15 个测试）；前端 `npm run build`（tsc -b && vite build）通过；端到端实测走通「扫节点 → 建实例 → 启动 → /v1/models → 对话」。
- 打包：`tauri build` 产出 exe；额外脚本产出便携 zip（manager.exe + README.txt）。

---

## 模块映射（从 enhance 迁移到本仓库）

| enhance 文件 | 迁移目标 | 改动 |
|---|---|---|
| `src/clash_yaml.rs` | `src-tauri/src/clash_yaml.rs` | 原样复制（含测试） |
| `src/config.rs` | `src-tauri/src/config.rs` | 原样复制（含测试） |
| `src/opencode_cfg.rs` | `src-tauri/src/opencode_cfg.rs` | 原样复制（含测试） |
| `src/singbox.rs` | `src-tauri/src/singbox.rs` | 原样复制（含测试） |
| `src/instance.rs` | `src-tauri/src/instance.rs` | 原样复制（含测试） |
| `src/probe.rs` | `src-tauri/src/probe.rs` | 原样复制（含测试） |
| `src/embed.rs` | `src-tauri/src/embed.rs` | 原样复制（`../bin/...` 路径不变） |
| `src/main.rs`（CLI+Tauri） | `src-tauri/src/main.rs` | **重写**：删除 axum serve；保留 instance/node/config CLI；桌面模式改纯 Tauri command |
| `src/web.rs`（axum handler） | `src-tauri/src/commands.rs` | **重写**：每个 handler → `#[tauri::command]`，共享 `AppState` |
| `static/index.html`（原生 JS UI） | `src/`（React 前端） | **重写**：AM 风格，`invoke` 替代 `fetch` |
| `bin/opencode2api.exe`、`bin/sing-box.exe` | `bin/` | 复制文件（embed 内嵌源） |

---

### Task 1: 仓库准备与清理

**Files:**
- Modify: `opencode2api/.git/config`（远程改名）
- Delete: `opencode2api/.github/`、`opencode2api/deploy/`、`opencode2api/docker/`、`opencode2api/Dockerfile`、`opencode2api/.dockerignore`
- Modify: `opencode2api/README.md`（后置到 Task 11 统一改，本任务只保证工程可构建）
- Create: `opencode2api/bin/.gitkeep`（bin 目录留痕，两个 exe 由 Task 10 放入）

**Interfaces:**
- Consumes: 无
- Produces: 干净的仓库基线（无 Docker/CI）；`bin/` 目录存在

- [ ] **Step 1: 重命名 git 远程 origin → upstream**

```bash
cd /d/AI_projects/opencode2api
git remote rename origin upstream
git remote -v
```

Expected: `upstream https://github.com/6Kmfi6HP/opencode2api.git (fetch/push)`

- [ ] **Step 2: 删除 Docker/CI 相关文件**

用 delete_file 工具删除：`.github/`、`deploy/`、`docker/`、`Dockerfile`、`.dockerignore`（不要用 rm 命令）。

- [ ] **Step 3: 创建 bin 目录占位**

```bash
cd /d/AI_projects/opencode2api
mkdir -p bin
echo "# 此目录存放内嵌二进制（opencode2api.exe / sing-box.exe），由 Task 10 复制" > bin/.gitkeep
```

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "chore: 移除 Docker/CI 设施，准备桌面化改造（bin 目录占位）"
```

---

### Task 2: Rust 基础模块迁移（clash_yaml / config / opencode_cfg / singbox）

**Files:**
- Create: `opencode2api/src-tauri/src/clash_yaml.rs`
- Create: `opencode2api/src-tauri/src/config.rs`
- Create: `opencode2api/src-tauri/src/opencode_cfg.rs`
- Create: `opencode2api/src-tauri/src/singbox.rs`
- Modify: `opencode2api/src-tauri/src/lib.rs`（声明 mod；main.rs 保持 194 字节壳，Task 4 再改）

**Interfaces:**
- Consumes: 无（纯逻辑模块）
- Produces:
  - `clash_yaml::ClashNode`（`name/server/port/node_type/password/uuid/cipher/sni/servername/tls/skip_cert_verify/network/ws_opts/client_fingerprint/group`，serde Deserialize）
  - `clash_yaml::list_nodes_with_group() -> Result<Vec<ClashNode>>`（外部控制 API 优先 + 本地 Clash Verge profiles 补充，过滤垃圾节点、带分组）
  - `clash_yaml::list_local_nodes() -> Result<Vec<ClashNode>>`、`parse_clash_yaml(&str) -> Result<Vec<ClashNode>>`、`parse_clash_yaml_file(&Path) -> Result<Vec<ClashNode>>`
  - `config::Config { base_url, default_password, clash_external_url, clash_auth_token }`，`Config::config_dir()/config_path()/load()/save()/get(&str)/set(&str,&str)`
  - `opencode_cfg::build_opencode_config(singbox_port: u16) -> Result<String>`
  - `singbox::build_singbox_config(&ClashNode, listen_port: u16) -> Result<String>`

- [ ] **Step 1: 从 enhance 复制四个模块文件**

```bash
cd /d/AI_projects
cp opencode2api_enhance/src/clash_yaml.rs opencode2api/src-tauri/src/clash_yaml.rs
cp opencode2api_enhance/src/config.rs opencode2api/src-tauri/src/config.rs
cp opencode2api_enhance/src/opencode_cfg.rs opencode2api/src-tauri/src/opencode_cfg.rs
cp opencode2api_enhance/src/singbox.rs opencode2api/src-tauri/src/singbox.rs
```

- [ ] **Step 2: 在 lib.rs 声明模块**

`opencode2api/src-tauri/src/lib.rs` 顶部追加：

```rust
pub mod clash_yaml;
pub mod config;
pub mod opencode_cfg;
pub mod singbox;
```

- [ ] **Step 3: 运行测试确认失败（缺依赖/编译错误）**

```bash
cd /d/AI_projects/opencode2api/src-tauri
cargo test 2>&1 | head -40
```

Expected: 编译错误（`serde_yaml`/`dirs`/`anyhow` 已在 Cargo.toml 中存在，但 `lib.rs` 尚未声明模块 / `main.rs` 引用了不存在的 `tauri_builder` crate）——这是预期，下一步修复。

- [ ] **Step 4: 确认 Cargo.toml 依赖齐全**

`opencode2api/src-tauri/Cargo.toml` 现状已含：`serde/serde_json/tokio(full)/serde_yaml/anyhow/sysinfo/dirs`。核对即可，无需追加（`dirs` 版本为 "5"，enhance 用 "6"，功能等价可继续用 5；如需统一可改 "6" 后 cargo build 验证）。**暂不删除 axum/tower-http/reqwest/tauri-plugin-shell**（Task 4 统一处理）。

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /d/AI_projects/opencode2api/src-tauri
cargo test
```

Expected: clash_yaml / config / opencode_cfg / singbox 四模块的测试全部 PASS（约 13 个测试：clash_yaml 8 + config 3 + opencode_cfg 2 + singbox 3 = 16，以实际为准，全部绿色）。

- [ ] **Step 6: 提交**

```bash
cd /d/AI_projects/opencode2api
git add src-tauri/
git commit -m "feat: 迁移 enhance 基础模块（clash_yaml/config/opencode_cfg/singbox）"
```

---

### Task 3: 实例与探针模块迁移（instance / probe）

**Files:**
- Create: `opencode2api/src-tauri/src/instance.rs`
- Create: `opencode2api/src-tauri/src/probe.rs`
- Modify: `opencode2api/src-tauri/src/lib.rs`（追加 `pub mod instance; pub mod probe;`）

**Interfaces:**
- Consumes: Task 2 的 `clash_yaml` / `config` / `opencode_cfg` / `singbox`
- Produces:
  - `instance::Instance { name, port, node, singbox_port, pid, singbox_pid, status }`（serde Serialize/Deserialize）
  - `instance::InstanceStatus`（`Stopped/Starting/Running/Stopping/Error(String)`）
  - `instance::InstanceManager::new(instances_path: PathBuf, binary_dir: PathBuf, runtime_dir: PathBuf)` / `load()` / `add_instance(name,port,node)` / `remove_instance(name)` / `start_instance(name, password)` / `stop_instance(name)` / `list_instances()` / `prepare_test(name) -> Result<u16>` / `test_instance(name) -> Result<TestResult>`
  - `instance::TestResult { name, port, ok, status_code, model_count, message, latency_ms }`
  - `instance::probe_models(name: &str, port: u16) -> TestResult`（可在 spawn_blocking 中调用）
  - `instance::http_get_json(port: u16, path: &str, timeout: Duration) -> Result<(u16, String)>`
  - `instance::wait_for_port(port: u16, timeout: Duration) -> bool`
  - `instance::kill_process(pid: u32) -> Result<()>`
  - `probe::ScanStatus`（Idle/Running/Stopping/Done/Error，serde snake_case）
  - `probe::ProbeResult { node, node_type, server, port, ok, category, status_code, model_count, message, latency_ms }`
  - `probe::ScanProgress { status, total, current, current_node, results, error, api_port, socks_port, started_ms, finished_ms }`
  - `probe::ScanController::new()` / `start_scan(binary_dir, runtime_dir, password, api_port, socks_port, node_filter: Option<Vec<String>>, per_node_timeout_secs) -> Result<()>` / `progress_snapshot() -> ScanProgress` / `request_stop()`
  - `probe::DEFAULT_PROBE_API_PORT: u16 = 19090`、`probe::DEFAULT_PROBE_SOCKS_PORT: u16 = 29090`
  - `probe::scan_nodes_sync(...) -> Result<Vec<ProbeResult>>`（CLI 用）

- [ ] **Step 1: 从 enhance 复制两个模块文件**

```bash
cd /d/AI_projects
cp opencode2api_enhance/src/instance.rs opencode2api/src-tauri/src/instance.rs
cp opencode2api_enhance/src/probe.rs opencode2api/src-tauri/src/probe.rs
```

- [ ] **Step 2: lib.rs 追加模块声明**

```rust
pub mod instance;
pub mod probe;
```

- [ ] **Step 3: 补齐依赖并编译**

`Cargo.toml` 的 `[dependencies]` 追加（按编译报错逐个补齐，预期缺 `sysinfo`、`serde_json`、`tokio` 的 `process`/`io` feature 等）：

```toml
sysinfo = "0.30"
```

先跑 `cargo build 2>&1 | grep -E "^error" | head -30` 按报错补齐缺的依赖，直到 `cargo build` 通过。

- [ ] **Step 4: 运行全部测试**

```bash
cd /d/AI_projects/opencode2api/src-tauri
cargo test
```

Expected: 全部 PASS（Task 2 的 16 个 + instance 5 个 + probe 若干，总计 ≥ 24 个）。probe 的测试无需真实节点（只测辅助函数）。

- [ ] **Step 5: 提交**

```bash
cd /d/AI_projects/opencode2api
git add src-tauri/
git commit -m "feat: 迁移实例管理与节点探针模块（instance/probe）"
```

---

### Task 4: Tauri command 层（替代 axum Web API）+ 桌面入口 + 托盘

**Files:**
- Create: `opencode2api/src-tauri/src/commands.rs`（所有原 web.rs handler 转为 `#[tauri::command]`）
- Create: `opencode2api/src-tauri/src/lib.rs`（当前**不存在**；新建 AppState、invoke_handler、托盘、窗口事件、CLI 入口）
- Rewrite: `opencode2api/src-tauri/src/main.rs`（当前内容无效——引用了不存在的 `tauri_builder` crate，直接覆盖为薄入口）
- Modify: `opencode2api/src-tauri/Cargo.toml`（**移除** axum/tower-http/reqwest/tauri-plugin-shell，**添加** clap）

**Interfaces:**
- Consumes: Task 2/3 全部模块
- Produces（前端 Task 6 依赖的命令签名，Tauri command 参数为 camelCase）：
  - `list_nodes() -> Vec<NodeView>`（NodeView: name/node_type/server/port/has_cred/group）
  - `list_instances() -> Vec<Instance>`
  - `add_instance(name: String, port: u16, node: String) -> Instance`（name 空则自动「实例N」）
  - `batch_add(nodes: Vec<BatchAddItem>, basePort: Option<u16>, useNodeName: Option<bool>, namePrefix: Option<String>) -> BatchAddResult`（BatchAddItem: node/name/port；BatchAddResult: added/errors/added_count/error_count）
  - `start_instance(name: String)` / `stop_instance(name: String)` / `remove_instance(name: String)`（remove 前必须已停止）
  - `batch_start(names: Vec<String>)` / `batch_stop(names: Vec<String>)` / `batch_delete(names: Vec<String>) -> BatchOpResult`（success/errors/success_count/error_count）
  - `test_instance(name: String) -> TestResult`
  - `port_suggest() -> u16` / `port_check(port: u16) -> PortCheckResult`（available/reason）
  - `scan_start(nodes: Option<Vec<String>>, apiPort: Option<u16>, socksPort: Option<u16>, timeout: Option<u64>) -> ScanProgress`
  - `scan_status() -> ScanProgress` / `scan_stop() -> ScanProgress`
  - `config_get() -> ConfigView`（base_url/default_password(掩码 ***)/has_password/clash_external_url/has_clash_token）
  - `config_set(key: String, value: String)`（key ∈ base_url/default_password/clash_external_url/clash_auth_token）
  - `autostart_get() -> bool` / `autostart_set(enabled: bool)`
  - `get_binaries_info() -> BinariesInfo`（bin_dir/oc_exists/sb_exists）
  - `hide_to_tray()` / `toggle_maximize()` / `quit_app()`
  - `AppState { manager: Arc<Mutex<InstanceManager>>, scan: Arc<ScanController> }` 通过 `tauri::State<'_, AppState>` 注入

- [ ] **Step 1: 编写 commands.rs**

从 `opencode2api_enhance/src/web.rs` 逐 handler 改写为 `#[tauri::command] async fn xxx(state: tauri::State<'_, AppState>, ...)`：
- 纯读命令（list_nodes / list_instances / scan_status / config_get / autostart_get / port_check）直接锁内执行。
- 阻塞命令（start/stop/remove/test/batch_*/scan_start）用 `tauri::async_runtime::spawn_blocking` 包裹同步锁操作，避免阻塞 UI 线程。
- 删除 `index()`、`run_server()`、`spawn_server()`、`build_app()`、`CorsLayer`、`Router`——不再有 HTTP 层。
- 保留 `manager_paths()`（config_dir + 实例文件 + binary_dir=exe旁bin + runtime_dir）与 `default_password()`。
- 复用 `autostart_status()/autostart_set()`（Windows 注册表 HKCU Run）。

- [ ] **Step 2: 重写 lib.rs 集成**

```rust
pub mod clash_yaml;
pub mod commands;
pub mod config;
pub mod embed;
pub mod instance;
pub mod opencode_cfg;
pub mod probe;
pub mod singbox;

pub struct AppState {
    pub manager: std::sync::Arc<std::sync::Mutex<instance::InstanceManager>>,
    pub scan: std::sync::Arc<probe::ScanController>,
}

pub fn run() {
    // 启动前释放内嵌二进制到 exe 旁 bin/
    let bin_dir = std::env::current_exe().ok().and_then(|p| p.parent().map(|d| d.to_path_buf())).unwrap_or_else(|| std::path::PathBuf::from(".")).join("bin");
    let _ = embed::ensure_binaries(&bin_dir);

    let (instances_path, _, runtime_dir) = commands::manager_paths();
    let mut manager = instance::InstanceManager::new(instances_path, bin_dir.clone(), runtime_dir);
    let _ = manager.load();

    tauri::Builder::default()
        .manage(AppState {
            manager: std::sync::Arc::new(std::sync::Mutex::new(manager)),
            scan: std::sync::Arc::new(probe::ScanController::new()),
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_nodes, commands::list_instances, commands::add_instance,
            commands::batch_add, commands::start_instance, commands::stop_instance,
            commands::remove_instance, commands::batch_start, commands::batch_stop,
            commands::batch_delete, commands::test_instance, commands::port_suggest,
            commands::port_check, commands::scan_start, commands::scan_status,
            commands::scan_stop, commands::config_get, commands::config_set,
            commands::autostart_get, commands::autostart_set, commands::get_binaries_info,
            commands::hide_to_tray, commands::toggle_maximize, commands::quit_app
        ])
        .setup(|app| {
            // 托盘：显示主窗口 / 退出；左键点击显示
            // 参照 enhance 的 desktop_main 托盘实现（TrayIconBuilder + on_menu_event + on_tray_icon_event）
            Ok(())
        })
        .on_window_event(|window, event| {
            // CloseRequested → prevent_close + hide（常驻托盘）
        })
        .run(tauri::generate_context!("tauri.conf.json"))
        .expect("桌面启动失败");
}
```

- [ ] **Step 3: 调整 Cargo.toml（移除 Web 依赖，加 clap）**

`opencode2api/src-tauri/Cargo.toml` `[dependencies]` 中**删除**：`axum`、`tower-http`、`reqwest`、`tauri-plugin-shell`（纯桌面不再需要 HTTP 服务与 shell 插件，子进程用 `std::process::Command` 直接拉起）；**添加**：

```toml
clap = { version = "4", features = ["derive"] }
```

若 `src-tauri/src/main.rs` 的 `tauri_plugin_shell::init()` 不再被引用，同时删除 `.plugin(tauri_plugin_shell::init())`（Step 4 重写 main.rs 时一并处理）。

- [ ] **Step 4: 重写 main.rs（CLI + 桌面双入口）**

`main.rs` 覆盖为（lib crate 名为 `opencode2api`，见 Cargo.toml package name）：

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(name = "opencode2api-manager", about = "opencode2api 多实例桌面管理器", version)]
struct Cli {
    #[command(subcommand)]
    command: Option<Commands>,
}

#[derive(Subcommand)]
enum Commands {
    /// 管理实例
    Instance { #[command(subcommand)] action: InstanceAction },
    /// 配置
    Config { #[command(subcommand)] action: ConfigAction },
    /// 代理节点
    Node { #[command(subcommand)] action: NodeAction },
}

#[derive(Subcommand)]
enum InstanceAction {
    Add { #[arg(long)] name: String, #[arg(long)] port: u16, #[arg(long)] node: String },
    Start { #[arg(long)] name: String },
    Stop { #[arg(long)] name: String },
    Remove { #[arg(long)] name: String },
    Test { #[arg(long)] name: String },
    List,
}

#[derive(Subcommand)]
enum ConfigAction {
    Set { key: String, value: String },
    Get { key: String },
}

#[derive(Subcommand)]
enum NodeAction {
    List,
    Scan {
        #[arg(long)] node: Vec<String>,
        #[arg(long, default_value_t = 19090)] api_port: u16,
        #[arg(long, default_value_t = 29090)] socks_port: u16,
        #[arg(long, default_value_t = 12)] timeout: u64,
    },
}

fn main() {
    match Cli::try_parse() {
        Ok(cli) => opencode2api::run_cli(cli),
        Err(_) => opencode2api::run(),
    }
}
```

CLI 的**实现逻辑**放在 `lib.rs` 的 `pub fn run_cli(cli: Cli)` 中：`Instance/Config/Node` 各子命令分支照抄 enhance `cli_main` 的对应段（add/start/stop/remove/test/list、config set/get、node list/scan），**删除 Serve 子命令**（axum 已移除）。注意 `Cli/Commands/InstanceAction/...` 结构体定义放在 main.rs，`run_cli` 签名要能接收它——若类型定义需在 lib 与 bin 间共享，则将类型也移入 lib.rs（`pub struct Cli`），main.rs 仅 `use opencode2api::{run, run_cli, Cli}`。

- [ ] **Step 5: 编译并跑单测**

```bash
cd /d/AI_projects/opencode2api/src-tauri
cargo build
cargo test
```

Expected: 编译通过，全部测试 PASS。若 `tauri::generate_context!` 报缺前端 dist，先建空 `src/main.tsx` + `index.html` 占位（Task 5 填充），或配置 `devUrl` 跳过。

- [ ] **Step 6: 更新 capabilities（确保 shell 执行权限）**

`opencode2api/src-tauri/capabilities/default.json` 确认已含窗口控制权限；由于不再用 shell 插件，可移除 `shell:allow-execute`/`shell:allow-open`（子进程由 Rust `std::process::Command` 直接拉起，不经前端）；补 `core:tray:default`（托盘）。

- [ ] **Step 7: 提交**

```bash
cd /d/AI_projects/opencode2api
git add src-tauri/ package.json
git commit -m "feat: Tauri command 层替代 axum Web API（实例/扫描/配置/自启/托盘）"
```

---

### Task 5: 前端工程与设计系统（React + Tailwind + AM 风格骨架）

**Files:**
- Create: `opencode2api/index.html`
- Create: `opencode2api/tsconfig.app.json`
- Create: `opencode2api/src/main.tsx`
- Create: `opencode2api/src/index.css`（复制 AM 的设计 token 并调整品牌名）
- Create: `opencode2api/src/App.tsx`（侧边栏 + 页面切换 + toast + TitleBar）
- Create: `opencode2api/src/components/TitleBar.tsx`（参照 AM 的自定义标题栏）
- Create: `opencode2api/src/lib/api.ts`（invoke 封装，Task 6 填函数体）
- Create: `opencode2api/src/pages/InstancesPage.tsx`（Task 7）
- Create: `opencode2api/src/pages/NodesPage.tsx`（Task 8）
- Create: `opencode2api/src/pages/SettingsPage.tsx`（Task 9）

**Interfaces:**
- Consumes: Task 4 的 command 签名
- Produces: 可 `npm run tauri:dev` 启动的骨架（三页占位、可切换、AM 视觉）；`lib/api.ts` 类型与函数签名（Task 6 实现）

- [ ] **Step 1: 确认前端工程文件**

`opencode2api/package.json` 已含 react/react-dom/lucide-react/clsx/@tauri-apps/api 与 devDeps（tailwind/vite/typescript）。若 `tsconfig.app.json` 不存在，参照 `windsurf-account-manager/source/tsconfig.app.json` 创建（include: ["src"]，jsx: react-jsx）。

- [ ] **Step 2: 创建 index.html**

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>opencode2api 管理器</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: 创建设计系统 index.css**

从 `windsurf-account-manager/source/src/index.css` 复制全部设计 token（`--bg/--bg-card/--border/--text/--primary/--accent: #0f766e/--radius: 16px` 等、body 渐变 wash、滚动条、focus 样式），字体改用系统栈（`ui-sans-serif, system-ui, "PingFang SC", "Microsoft YaHei", sans-serif`，避免引入 Google Fonts 网络依赖），并保留 `@import "tailwindcss";`。

- [ ] **Step 4: 创建 TitleBar.tsx（AM 风格无边框标题栏）**

参照 AM TitleBar：`h-11` 白底毛玻璃、左侧图标 + 「opencode2api 管理器」标题（`data-tauri-drag-region`）、右侧最小化/最大化/关闭三按钮（lucide `Minus/Square/X`）。关闭按钮逻辑：直接 `getCurrentWindow().hide()`（后端已拦截 CloseRequested 常驻托盘，无需读设置）。双击标题栏切换最大化。

- [ ] **Step 5: 创建 App.tsx 骨架**

```tsx
import { useEffect, useState } from 'react'
import clsx from 'clsx'
import { Server, Radar, Settings } from 'lucide-react'
import { TitleBar } from './components/TitleBar'
import { InstancesPage } from './pages/InstancesPage'
import { NodesPage } from './pages/NodesPage'
import { SettingsPage } from './pages/SettingsPage'

type Tab = 'instances' | 'nodes' | 'settings'
const NAV: { id: Tab; label: string; icon: typeof Server }[] = [
  { id: 'instances', label: '实例', icon: Server },
  { id: 'nodes', label: '节点扫描', icon: Radar },
  { id: 'settings', label: '设置', icon: Settings },
]

export default function App() {
  const [tab, setTab] = useState<Tab>('instances')
  const [toast, setToast] = useState<string | null>(null)
  const showToast = (msg: string) => { setToast(msg); setTimeout(() => setToast(null), 3600) }

  return (
    <div className="h-full flex flex-col">
      <TitleBar />
      <div className="flex-1 flex min-h-0">
        <aside className="w-44 shrink-0 border-r border-zinc-200/80 bg-white/60 backdrop-blur flex flex-col py-4 px-2 gap-1">
          {NAV.map(({ id, label, icon: Icon }) => (
            <button key={id} type="button" onClick={() => setTab(id)}
              className={clsx('flex items-center gap-2.5 px-3 py-2 rounded-lg text-[13px] font-medium transition-colors',
                tab === id ? 'bg-zinc-900 text-white shadow-sm' : 'text-zinc-600 hover:bg-zinc-100')}>
              <Icon size={16} strokeWidth={2} /> {label}
            </button>
          ))}
        </aside>
        <main className="flex-1 min-w-0 overflow-y-auto">
          {tab === 'instances' && <InstancesPage toast={showToast} />}
          {tab === 'nodes' && <NodesPage toast={showToast} />}
          {tab === 'settings' && <SettingsPage toast={showToast} />}
        </main>
      </div>
      {toast && <div className="fixed bottom-5 left-1/2 -translate-x-1/2 z-50 px-4 py-2 rounded-lg bg-zinc-900 text-white text-[13px] shadow-lg">{toast}</div>}
    </div>
  )
}
```

- [ ] **Step 6: 创建三个占位页面组件**

每个页面 `export default function XxxPage({ toast }: { toast: (m: string) => void }) { return <div className="p-6 text-zinc-500">建设中…</div> }`，保证 `npm run build` 通过。

- [ ] **Step 7: 构建验证**

```bash
cd /d/AI_projects/opencode2api
npm run build
```

Expected: `tsc -b && vite build` 成功，产出 `dist/`。

- [ ] **Step 8: 提交**

```bash
git add index.html tsconfig.app.json src/ package.json
git commit -m "feat: 前端工程骨架（React+Tailwind，AM 风格设计系统与侧边栏布局）"
```

---

### Task 6: 前端 API 封装层（invoke）

**Files:**
- Modify: `opencode2api/src/lib/api.ts`

**Interfaces:**
- Consumes: Task 4 的 command 签名（见 Task 4 Interfaces）
- Produces: `api` 对象（供 Task 7/8/9 调用）：
  - `api.listNodes()`, `api.listInstances()`, `api.addInstance(name, port, node)`, `api.batchAdd(nodes, basePort?, useNodeName?, namePrefix?)`, `api.startInstance(name)`, `api.stopInstance(name)`, `api.removeInstance(name)`, `api.batchStart(names)`, `api.batchStop(names)`, `api.batchDelete(names)`, `api.testInstance(name)`, `api.portSuggest()`, `api.portCheck(port)`, `api.scanStart(opts?)`, `api.scanStatus()`, `api.scanStop()`, `api.configGet()`, `api.configSet(key, value)`, `api.autostartGet()`, `api.autostartSet(enabled)`, `api.getBinariesInfo()`
  - 类型：`NodeView`、`Instance`、`InstanceStatus`、`TestResult`、`BatchAddResult`、`BatchOpResult`、`PortCheckResult`、`ScanProgress`、`ProbeResult`、`ConfigView`、`BinariesInfo`、`BatchAddItem`

- [ ] **Step 1: 编写 api.ts**

```ts
import { invoke } from '@tauri-apps/api/core'

export type InstanceStatus = 'Stopped' | 'Starting' | 'Running' | 'Stopping' | { Error: string }
export type Instance = { name: string; port: number; node: string; singbox_port: number; pid: number | null; singbox_pid: number | null; status: InstanceStatus }
export type NodeView = { name: string; node_type: string; server: string; port: number; has_cred: boolean; group: string }
export type TestResult = { name: string; port: number; ok: boolean; status_code: number | null; model_count: number | null; message: string; latency_ms: number }
export type BatchAddItem = { node: string; name?: string | null; port?: number | null }
export type BatchAddResult = { added: { name: string; port: number; node: string }[]; errors: { node: string; error: string }[]; added_count: number; error_count: number }
export type BatchOpResult = { success: string[]; errors: Record<string, string>; success_count: number; error_count: number }
export type PortCheckResult = { available: boolean; reason: string }
export type ScanStatus = 'idle' | 'running' | 'stopping' | 'done' | 'error'
export type ProbeResult = { node: string; node_type: string; server: string; port: number; ok: boolean; category: string; status_code: number | null; model_count: number | null; message: string; latency_ms: number }
export type ScanProgress = { status: ScanStatus; total: number; current: number; current_node: string | null; results: ProbeResult[]; error: string | null; api_port: number; socks_port: number; started_ms: number | null; finished_ms: number | null }
export type ConfigView = { base_url: string; default_password: string; has_password: boolean; clash_external_url: string; has_clash_token: boolean }
export type BinariesInfo = { bin_dir: string; oc_exists: boolean; sb_exists: boolean }

export const api = {
  listNodes: () => invoke<NodeView[]>('list_nodes'),
  listInstances: () => invoke<Instance[]>('list_instances'),
  addInstance: (name: string, port: number, node: string) => invoke<Instance>('add_instance', { name, port, node }),
  batchAdd: (nodes: BatchAddItem[], basePort?: number, useNodeName?: boolean, namePrefix?: string) =>
    invoke<BatchAddResult>('batch_add', { nodes, basePort: basePort ?? null, useNodeName: useNodeName ?? null, namePrefix: namePrefix ?? null }),
  startInstance: (name: string) => invoke<void>('start_instance', { name }),
  stopInstance: (name: string) => invoke<void>('stop_instance', { name }),
  removeInstance: (name: string) => invoke<void>('remove_instance', { name }),
  batchStart: (names: string[]) => invoke<BatchOpResult>('batch_start', { names }),
  batchStop: (names: string[]) => invoke<BatchOpResult>('batch_stop', { names }),
  batchDelete: (names: string[]) => invoke<BatchOpResult>('batch_delete', { names }),
  testInstance: (name: string) => invoke<TestResult>('test_instance', { name }),
  portSuggest: () => invoke<number>('port_suggest'),
  portCheck: (port: number) => invoke<PortCheckResult>('port_check', { port }),
  scanStart: (opts?: { nodes?: string[]; apiPort?: number; socksPort?: number; timeout?: number }) =>
    invoke<ScanProgress>('scan_start', { nodes: opts?.nodes ?? null, apiPort: opts?.apiPort ?? null, socksPort: opts?.socksPort ?? null, timeout: opts?.timeout ?? null }),
  scanStatus: () => invoke<ScanProgress>('scan_status'),
  scanStop: () => invoke<ScanProgress>('scan_stop'),
  configGet: () => invoke<ConfigView>('config_get'),
  configSet: (key: string, value: string) => invoke<void>('config_set', { key, value }),
  autostartGet: () => invoke<boolean>('autostart_get'),
  autostartSet: (enabled: boolean) => invoke<void>('autostart_set', { enabled }),
  getBinariesInfo: () => invoke<BinariesInfo>('get_binaries_info'),
}
```

- [ ] **Step 2: 构建验证**

```bash
cd /d/AI_projects/opencode2api
npm run build
```

Expected: PASS（页面暂未使用 api，类型不冲突）。

- [ ] **Step 3: 提交**

```bash
git add src/lib/api.ts
git commit -m "feat: 前端 invoke 封装层（实例/扫描/配置/自启）"
```

---

### Task 7: 实例管理页面

**Files:**
- Modify: `opencode2api/src/pages/InstancesPage.tsx`

**Interfaces:**
- Consumes: `api.listInstances/addInstance/startInstance/stopInstance/removeInstance/testInstance/batchStart/batchStop/batchDelete/portSuggest/portCheck/listNodes`
- Produces: 完整的实例管理 UI（参照 enhance `static/index.html` 的实例页功能点 + AM 卡片/表格风格）

**页面元素**（对应 enhance 原功能）：
- 顶部工具条：实例计数、`刷新`、`添加实例`、`批量启动`、`批量停止`、`批量删除`（选中后可用）
- 实例表格列：勾选 | 名称 | 端口 | API 地址（`http://127.0.0.1:{port}/v1`，可复制） | 节点 | sing-box 端口 | 状态（徽章） | 操作（启动/停止/测试/删除）
- 添加实例：Modal 弹窗（名称、端口自动建议 `portSuggest()`、节点下拉从 `listNodes()` 取），端口输入后 `portCheck()` 实时校验
- 测试：`testInstance(name)`，结果 toast 展示（models 数 / 延迟 ms / 错误信息）
- 轮询：挂载后 `listInstances()` 每 2s 轮询刷新状态（轻量，仅读本地文件）
- 复制 API 地址：navigator.clipboard.writeText

- [ ] **Step 1: 实现页面**

参照 AM 的表格/徽章样式：卡片容器 `bg-white rounded-[16px] border border-zinc-200 shadow-sm p-5`；状态徽章：Running=绿 `--ok-soft`、Stopped=灰、Starting/Stopping=琥珀、Error=红；按钮主操作 `bg-zinc-900 text-white hover:bg-zinc-700 rounded-lg`。功能逻辑对照 enhance `static/index.html` 的 `loadInstances/renderInstances/onAction/batchOp/openAddModal/bindModalPortChecks` 逐条翻译为 React hooks。

- [ ] **Step 2: 构建验证**

```bash
cd /d/AI_projects/opencode2api
npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

```bash
git add src/pages/InstancesPage.tsx
git commit -m "feat: 实例管理页面（增删启停/批量操作/测试/端口校验）"
```

---

### Task 8: 节点扫描页面

**Files:**
- Modify: `opencode2api/src/pages/NodesPage.tsx`

**Interfaces:**
- Consumes: `api.listNodes/scanStart/scanStatus/scanStop/batchAdd/listInstances`
- Produces: 节点扫描 UI（对应 enhance 节点页）

**页面元素**：
- 顶部：`一键扫描全部`、`停止扫描`（扫描中显示）、节点计数、分组说明
- 进度条：`scanStatus()` 的 current/total → 百分比；扫描中每 800ms 轮询（原 enhance 用 startScanPoll 轮询 `/api/nodes/scan/status`，逻辑平移为 `api.scanStatus()`）
- 扫描元信息：状态 / 当前节点 / 已用时间 / 可用数
- 节点表（按 group 分组、可折叠、组内全选）：勾选 | 名称 | 类型 | 服务器:端口 | 分组 | 凭据（✓/✗）| 扫描结果列（状态徽章 ok/config/socks/tls/upstream/timeout/other + 延迟 ms + 说明） | 操作（单测 / 加入实例）
- 操作：勾选可用节点 → `添加为实例`（Modal：每节点一行 名称+端口，端口默认 base+i，可编辑并 `portCheck()`；确认后 `batchAdd()`）；`批量测试选中`；`批量添加选中`
- 扫描结果与节点列表合并展示（原 enhance `applyScanResults`：扫描结果按 node 名映射到节点行）

- [ ] **Step 1: 实现页面**

逻辑参照 enhance `static/index.html` 的 `loadNodes/renderNodes/toggleGroup/onGroupAction/onNodeAction/applyScanResults/renderScanMeta/startScanPoll/stopScan/startNodeTest/openAddModal/confirmAddModal`。样式沿用 AM 设计 token。

- [ ] **Step 2: 构建验证**

```bash
cd /d/AI_projects/opencode2api
npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

```bash
git add src/pages/NodesPage.tsx
git commit -m "feat: 节点扫描页面（分组/进度/结果徽章/批量添加实例）"
```

---

### Task 9: 设置页面

**Files:**
- Modify: `opencode2api/src/pages/SettingsPage.tsx`

**Interfaces:**
- Consumes: `api.configGet/configSet/autostartGet/autostartSet/getBinariesInfo`
- Produces: 设置页（对应 enhance 设置页 + 桌面信息）

**页面元素**：
- Clash 外部控制：URL（默认 `http://127.0.0.1:9097`）、Token（密码框，掩码提示 `has_clash_token`）、`保存`
- 默认实例密码：`default_password`（留空则启动实例用 `123456`）
- 开机自启：开关（`autostartGet/Set`，Windows 注册表）
- 关于：二进制目录路径（`getBinariesInfo().bin_dir`）、opencode2api.exe / sing-box.exe 是否存在（绿/红点）、版本号
- 配置保存后 `toast('已保存')`

- [ ] **Step 1: 实现页面**

- [ ] **Step 2: 构建验证**

```bash
cd /d/AI_projects/opencode2api
npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

```bash
git add src/pages/SettingsPage.tsx
git commit -m "feat: 设置页面（Clash 外部控制/默认密码/开机自启/二进制状态）"
```

---

### Task 10: 二进制嵌入与便携打包

**Files:**
- Create: `opencode2api/src-tauri/src/embed.rs`（从 enhance 复制）
- Copy: `opencode2api/bin/opencode2api.exe`（来自 enhance/bin）
- Copy: `opencode2api/bin/sing-box.exe`（来自 enhance/bin）
- Modify: `opencode2api/src-tauri/tauri.conf.json`（窗口尺寸 980×680、minWidth/minHeight、图标确认）
- Create: `opencode2api/scripts/make-portable.sh`（组装便携 zip）
- Create: `opencode2api/portable/README.txt`（使用说明）

**Interfaces:**
- Consumes: Task 2-4 的 embed 引用（`include_bytes!("../bin/opencode2api.exe")` 要求文件位于 src-tauri 上级的 bin/）
- Produces: `target/release/opencode2api-manager.exe`（内嵌两子程序）；`dist/opencode2api-manager-portable.zip`

- [ ] **Step 1: 复制 embed.rs 与二进制**

```bash
cd /d/AI_projects
cp opencode2api_enhance/src/embed.rs opencode2api/src-tauri/src/embed.rs
cp opencode2api_enhance/bin/opencode2api.exe opencode2api/bin/opencode2api.exe
cp opencode2api_enhance/bin/sing-box.exe opencode2api/bin/sing-box.exe
ls -la opencode2api/bin/
```

Expected: embed.rs 存在；bin 下两个 exe（9.9MB / 43.3MB）。

- [ ] **Step 2: lib.rs 声明 embed 模块**

`pub mod embed;`（若 Task 4 未加）。

- [ ] **Step 3: 调整 tauri.conf.json 窗口**

`app.windows[0]` 改为：`"width": 980, "height": 680, "minWidth": 820, "minHeight": 560`；其余（title「opencode2api 管理器」、decorations:false、center:true）保持不变。

- [ ] **Step 4: 写入便携包 README.txt**

内容：程序说明（opencode2api 多实例管理器）、使用方法（双击 exe 启动 → 设置页填 Clash 外部控制地址 → 节点扫描 → 添加实例 → 启动）、端口说明（每个实例的 API 地址 `http://127.0.0.1:{port}/v1`）、数据目录 `%APPDATA%/opencode2api-manager/`、免责声明（非官方项目，遵守上游条款）。

- [ ] **Step 5: 编写打包脚本 make-portable.sh**

```bash
#!/usr/bin/env bash
# 用法: bash scripts/make-portable.sh [version]
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.1.0}"
RELEASE="src-tauri/target/release/opencode2api-manager.exe"
[ -f "$RELEASE" ] || { echo "未找到 $RELEASE，请先 npm run tauri:build"; exit 1; }
mkdir -p dist
STAGE="dist/portable"
rm -rf "$STAGE"
mkdir -p "$STAGE"
cp "$RELEASE" "$STAGE/opencode2api-manager.exe"
cp portable/README.txt "$STAGE/README.txt"
# 二进制已内嵌于 exe，运行时自动释放；zip 只需 exe + 说明
(cd "$STAGE" && zip -r "../opencode2api-manager-${VERSION}-portable.zip" . -x ".*")
rm -rf "$STAGE"
echo "完成: dist/opencode2api-manager-${VERSION}-portable.zip"
```

- [ ] **Step 6: 完整构建 + 打包**

```bash
cd /d/AI_projects/opencode2api
npm run tauri:build
bash scripts/make-portable.sh
```

Expected: `src-tauri/target/release/opencode2api-manager.exe` 生成（体积约 55-65MB，含内嵌二进制）；`dist/opencode2api-manager-0.1.0-portable.zip` 生成。

- [ ] **Step 7: 提交**

```bash
git add src-tauri/ bin/ scripts/ portable/ package.json
git commit -m "build: 内嵌二进制、窗口调整、便携 zip 打包脚本"
```

---

### Task 11: 端到端验收与文档

**Files:**
- Modify: `opencode2api/README.md`（重写为桌面应用说明：功能、构建、打包、使用、数据目录、架构）
- Modify: `opencode2api/CHANGELOG.md`（追加桌面化 v0.1.0 条目）
- Delete: `opencode2api/architecture.md`（若内容仍与项目无关——本仓库 architecture.md 是否与 AM 仓库相同？检查后处理：无关则删除或改为本项目架构说明）

**Interfaces:**
- Consumes: 全部任务产物

- [ ] **Step 1: 全量测试**

```bash
cd /d/AI_projects/opencode2api/src-tauri
cargo test
cd /d/AI_projects/opencode2api
npm run build
```

Expected: Rust 测试全绿；前端构建通过。

- [ ] **Step 2: 端到端实测（需本机 Clash 外部控制 127.0.0.1:9097 运行中）**

```bash
cd /d/AI_projects/opencode2api
npm run tauri:dev
```

验收清单（逐项执行并记录结果）：
1. 应用启动 → 托盘出现图标；关闭窗口 → 应用隐藏到托盘，进程仍在
2. 设置页：填 Clash 外部控制 URL（http://127.0.0.1:9097）+ Token → 保存成功
3. 节点扫描页：`一键扫描全部` → 进度条推进 → 结果分类（期望至少若干 `ok` 节点）
4. 勾选一个 ok 节点 → `添加为实例` → 确认 → 实例页出现新实例
5. 实例页：启动实例 → 状态变 Running；`http://127.0.0.1:{port}/v1/models` 返回模型列表
6. 实例测试：点击测试 → 提示「models 正常，共 N 个模型」+ 延迟 ms
7. 对话验证：

```bash
curl http://127.0.0.1:{port}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer public" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],"stream":false}'
```

Expected: 返回正常 completion 响应（模型名经别名映射到免费模型）。
8. 停止实例 → 状态 Stopped；重新扫描 → 正常
9. 打开便携 zip → 解压后 exe 可独立运行，自动释放 bin/ 子程序

- [ ] **Step 3: 重写 README.md**

内容：项目定位（桌面多实例管理器）、功能清单（实例管理/节点扫描/设置/托盘）、快速开始（构建：`npm install && npm run tauri:dev`；打包：`npm run tauri:build && bash scripts/make-portable.sh`）、使用流程（Clash 外部控制 → 扫描 → 建实例 → 启动）、数据目录说明、架构说明（Tauri + 内嵌 Go 核心 + sing-box）、上游声明（功能迁移自 opencode2api_enhance，Go 核心源于 6Kmfi6HP/opencode2api）。

- [ ] **Step 4: 处理 architecture.md / 冗余文档**

若 `opencode2api/architecture.md` 内容与项目无关（参照 AM 仓库同名文件判断），删除或替换为桌面版架构说明。

- [ ] **Step 5: 最终提交**

```bash
git add -A
git commit -m "docs: 桌面化改造完成——README/CHANGELOG/架构说明"
```

- [ ] **Step 6: 验收汇报**

向用户汇报：交付物路径（exe / 便携 zip）、端到端实测结果（逐条）、已知限制（如多实例共享 stats.json 统计、扫描串行耗时）、后续建议（订阅解析/延迟显示可后续加）。

---

## Self-Review

**1. Spec coverage（对照用户确认的决策）：**
- ✅ 桌面 exe（Tauri 2）→ Task 1/4/5/10
- ✅ 页面风格参照 Windsurf Account Manager（浅色官网风、自定义标题栏、卡片/表格、teal accent）→ Task 5（index.css token + TitleBar + 侧边栏）
- ✅ 丢弃自身功能（Docker/CI 等）→ Task 1
- ✅ 完整迁移 enhance 功能（实例管理/节点扫描/批量添加/Clash 外部控制/自启/托盘）→ Task 2/3/4/7/8/9
- ✅ 纯桌面不开 9099 → Task 4（移除 axum；embed 释放仅写文件不监听）
- ✅ %APPDATA% 存储 → config.rs（dirs::config_dir()，Windows 即 %APPDATA%）
- ✅ 托盘常驻、关窗不退出 → Task 4 lib.rs on_window_event + 托盘
- ✅ 应用名「opencode2api 管理器」→ tauri.conf.json（已存在）
- ✅ 便携 zip（manager.exe + README）→ Task 10
- ✅ 端到端实测验收 → Task 11
- ✅ origin → upstream 只拉不推 → Task 1
- ✅ 全中文界面 → 各前端 Task
- ✅ superpowers 子代理编排 → 本计划的执行交接（下节）

**2. Placeholder scan:** 各步骤均含具体命令/代码/验收预期，无 TBD；页面样式步骤引用了 enhance 源文件的具体函数名作为翻译依据。

**3. Type consistency:** commands.rs（Task 4）与 api.ts（Task 6）签名一一对应（add_instance ↔ addInstance 的 camelCase 参数映射、scan_start 的 nodes/apiPort/socksPort/timeout）；InstanceManager/ScanController 方法名与 enhance 一致；ProbeResult/ScanProgress 字段与 enhance web.rs 一致。

---

## Execution Handoff

计划已保存。两种执行方式：

1. **Subagent-Driven（推荐）** - 每个 Task 派发独立子代理执行，任务间我做 review，快速迭代（Task 2/3 可并行，Task 5 可与 Task 2/3 并行，Task 7/8/9 依赖 Task 4/6 后并行）
2. **Inline Execution** - 当前会话内按 Task 顺序批量执行，检查点处暂停 review

请选择执行方式。
