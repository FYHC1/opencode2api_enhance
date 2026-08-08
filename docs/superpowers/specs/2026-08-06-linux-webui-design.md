# 设计：Linux 适配 + WebUI（桌面 + Headless）+ 功能完善

> 日期：2026-08-06
> 分支：feature/linux-webui（基于 main @ a9e8a2a v1.1.0）

## 背景与目标

opencode2api 管理器当前是 **Windows 优先**的 Tauri 2 桌面应用：React 前端 → Tauri `invoke` → Rust command（instance/probe/gateway/clash_yaml/singbox 等，共 6167 行）→ 拉起 Go 核心（opencode2api）+ sing-box 子进程。

本次改造目标（经与用户澄清确认）：

1. **Linux 适配**：应用能在 Linux 上运行
2. **WebUI**：**两者都要** —— 桌面模式（Linux GUI）+ 纯浏览器 headless 模式（同一套前端两种载体）
3. **部署形态**：以**无头服务器（headless）为主**，需支持远程浏览器访问 + systemd 守护
4. **功能完善**（用户勾选 + 补充）：
   - 订阅链接拉取节点
   - 节点自动健康巡检
   - 统计报表与导出
   - 日志检索与过滤
   - 支持自定义统一网关密钥
   - 支持通过配置文件配置 opencode2api 自定义运行端口、统一网关密钥及其他配置

## 技术方案

采用**方案 A：Rust 单二进制双模式**（用户已确认方向）。

- 复用全部 6167 行已验证的 Rust 管理逻辑，不重写
- 单二进制同时支持桌面与 headless，跨平台一致
- 前端统一走 HTTP，桌面与 headless 共用一套前端代码

---

## 第 1 节：整体架构（Rust core 抽取 + 双入口）

### 现状

React 前端 → Tauri `invoke` → Rust commands（instance/probe/gateway/clash_yaml/singbox 共 6167 行）→ 拉起 Go 核心 + sing-box 子进程。所有管理逻辑在 Rust 侧，且全部耦合 Tauri（command 层直接操作 AppState）。

### 目标架构

```
                     ┌─────────────────────────────────┐
                     │  React 前端（一套，api.ts 统一）   │
                     └───────────────┬─────────────────┘
                                     │ HTTP fetch (统一)
                     ┌───────────────▼─────────────────┐
                     │  Rust core（纯逻辑，无 Tauri 依赖）│
                     │  instance · probe · gateway ·    │
                     │  singbox · clash_yaml · subscribe│
                     │  + axum HTTP API + 静态文件托管   │
                     └───────────────┬─────────────────┘
              ┌──────────────────────┼──────────────────────┐
   ┌──────────▼──────────┐  ┌────────▼─────────┐
   │ 桌面入口（Tauri 壳）  │  │ headless 入口      │
   │ 窗口 + 托盘 + 自启    │  │ serve --port 8080 │
   │ = 本地 HTTP 的浏览器壳│  │ 绑定地址可配置      │
   └─────────────────────┘  └──────────────────┘
```

### 关键决策：前端统一走 HTTP

- 桌面模式 = Tauri 窗口加载 `http://127.0.0.1:{管理端口}`，Tauri 退化为纯壳（窗口/托盘/开机自启）
- 优点：一套前端代码、一套 API、桌面与 headless 行为完全一致、测试面减半
- 开发热更：桌面模式指向 vite dev server，行为不变
- 安全：管理服务默认绑定 `127.0.0.1`（防暴露）；headless 可配 `0.0.0.0` + 登录认证（`manage_auth`）

### core 抽取边界

- **抽为纯逻辑模块**（无 tauri 依赖）：`instance`、`probe`、`gateway`、`singbox`、`clash_yaml`、`call_log`、`config`、新增 `subscribe`
- **留在桌面入口**（Tauri 特有）：窗口管理、托盘、开机自启、`embed::ensure_binaries`
- HTTP API 层（axum）调用 core 逻辑，与现有 Tauri command 逻辑等价

### 模块清单与迁移

| 现有文件 | 行数 | 处置 |
|---|---|---|
| `commands.rs` | 1891 | 拆解：逻辑下沉 core，command 壳瘦身（桌面入口） |
| `instance.rs` | 1178 | 下沉 core，平台适配（见第 5 节） |
| `probe.rs` | 1070 | 下沉 core |
| `clash_yaml.rs` | 464 | 下沉 core |
| `gateway.rs` | 375 | 下沉 core，端口/密钥配置化 |
| `singbox.rs` | 407 | 下沉 core |
| `config.rs` | 222 | 下沉 core，新增字段 |
| `call_log.rs` | 145 | 下沉 core，新增查询参数 |
| `embed.rs` | 32 | 留在桌面入口（或 core 平台分支） |
| `lib.rs` / `main.rs` | 210 | 桌面入口：Tauri 壳 |
| 新增 `server.rs` | - | headless 入口：axum + 静态文件 |
| 新增 `subscribe.rs` | - | 订阅拉取解析 |

---

## 第 2 节：配置体系（配置文件驱动）

### 现状问题

- `UNIFIED_GATEWAY_PORT=18080`、`UNIFIED_GATEWAY_KEY` 硬编码在 `gateway.rs`
- `config.json` 无管理端口/网关密钥字段
- 用户明确要求：支持通过配置文件配置 opencode2api 自定义运行端口、统一网关密钥及其他配置

### 设计：config.json 扩展字段（全部向后兼容，serde `#[serde(default)]`）

| 字段 | 说明 | 默认值 |
|---|---|---|
| `gateway_key` | 统一网关密钥（替代硬编码 UNIFIED_GATEWAY_KEY） | 保持现状生成逻辑 |
| `gateway_port` | 统一网关端口（替代硬编码 18080） | 18080 |
| `manage_port` | 管理 HTTP 服务端口 | 19090 |
| `manage_bind` | 管理服务绑定地址 | `127.0.0.1` |
| `manage_auth` | headless 远程登录口令（桌面模式忽略；空 = 禁止远程绑定非回环地址） | 空 |
| `subscribe_urls` | 订阅链接列表 | 空 |
| `health_check_interval_s` | 自动健康巡检间隔（秒；0 = 关闭） | 300 |

### 读取优先级

CLI 参数 > 环境变量（`OPCODE2API_*`）> config.json > 默认值。

headless 支持 `--config <path>` 指定配置文件路径；`--port`、`--bind` 覆盖配置。

### 网关密钥兼容

`gateway_key` 未设置时回退到现状（`UNIFIED_GATEWAY_KEY` 生成逻辑），保证旧配置/旧实例无缝升级。

---

## 第 3 节：功能点设计

### ① 订阅链接拉取节点（新增 `subscribe.rs`）

- 支持两类订阅：
  - **Clash 订阅**：YAML 的 `proxies` 段
  - **V2Ray 订阅**：base64 编码的 `vmess://` `vless://` `trojan://` `ss://` 行
- 解析结果与 `clash_yaml.rs` 现有节点结构对齐，复用 probe 扫描流程
- 设置页新增「订阅管理」：URL 列表 + 一键拉取；结果缓存到 `runtime/subscribe_cache.json`
- HTTP 拉取超时、失败重试、错误提示（与现有 clash 拉取一致）

### ② 节点自动健康巡检

- tokio 后台定时任务（间隔 `health_check_interval_s`，默认 5min，0 = 关闭）
- 对运行中实例的出口节点做轻量探测（复用 probe 逻辑）
- 故障节点自动标记，配合网关 failover（Go 侧已有故障切换）形成管理面闭环
- 巡检记录落 `runtime/health.json`，前端节点页展示最近状态与历史
- 巡检不自动剔除实例（保守策略），只标记状态供用户决策；自动停用逻辑交给网关路由层

### ③ 统计报表与导出

- 数据源：`call_log.jsonl` + `stats.json`（Go 已落盘）
- 新增聚合接口：按 天/周/月 聚合；按 节点/模型 维度下钻
- 导出：CSV / JSON（Rust 侧生成，`text/csv` / `application/json` 响应）
- 前端 StatsPage 增加图表与导出按钮（复用现有数据接口 + 新聚合接口）

### ④ 日志检索与过滤

- Rust 侧 `read_call_log` 增加查询参数：关键字、节点、模型、状态（成功/失败/有异常）、时间范围、分页
- 前端 LogsPage 增加搜索框与过滤条件、详情展开
- 大文件场景：按需读取 + 行过滤，避免全量载入

### ⑤ 自定义统一网关密钥 + 配置文件

见第 2 节（`gateway_key`/`gateway_port`/`manage_port` 等字段）。

---

## 第 4 节：数据层

- 现有文件全部保留、格式不变：`config.json`、`instances.json`、`runtime/`、`stats.json`、`call_log.jsonl`、`node_stats.json`
- 新增：
  - `runtime/health.json` — 巡检记录
  - `runtime/subscribe_cache.json` — 订阅拉取缓存
- 配置新增字段全部走 serde `#[serde(default)]`，旧 config.json 直接兼容
- 数据目录逻辑（`config.rs::config_dir`）已支持 `OPCODE2API_DATA_DIR` 环境变量 + `dirs::config_dir()`，跨平台复用

---

## 第 5 节：Linux 适配点（明确清单）

| 位置 | 现状（Windows 硬编码） | 改造 |
|---|---|---|
| `instance.rs` | `sing-box.exe` / `opencode2api.exe` 硬编码 | `cfg!(windows)` 选择扩展名（Linux 无 `.exe`） |
| `instance.rs` | `no_window()`（CREATE_NO_WINDOW） | cfg 门控，Linux 空操作 |
| `instance.rs` | `taskkill` 杀进程 | Linux 用 `sysinfo` kill（已有注释但需验证实现完整性） |
| `embed.rs` | `include_bytes!` 内嵌 exe | 按平台内嵌 Linux 版二进制（CI 双平台构建） |
| `gateway.rs` | 端口/密钥硬编码 | 配置化（第 2 节） |
| `config.rs` | `dirs::config_dir()` | 已跨平台（Linux = `~/.config/`）✅ |
| `lib.rs` | 托盘 | Tauri 2 tray-icon 已支持 Linux（appindicator）✅ |
| 开机自启 | Windows 注册表 | Linux 写 `~/.config/autostart/*.desktop` |
| CI | windows-latest 构建 | 增加 ubuntu-latest 目标 + Linux sing-box 下载 |
| systemd | 无 | 新增 headless 部署示例 unit 文件（docs） |

### 依赖补充

- axum（HTTP API 层）+ tokio（已有）
- 静态文件服务：`tower-http` 或 axum `ServeDir`

---

## 第 6 节：验证策略

- **Go 侧**：已有 `main_test.go` 等测试全量回归，确保不破坏网关核心
- **Rust 侧**：`cargo test` + 新增 headless API 集成测试（启动 serve → 调 API → 断言）
- **跨平台**：CI 双平台构建（windows + ubuntu）；WSL 本地跑通 headless
- **前端**：`vite build` + 桌面/浏览器双载体人工验证
- **协议回归**：`protocol_regression_test.go` 等确保 API 兼容

---

## 范围边界（YAGNI）

- 不做多机分布式部署（本期）
- 不做多用户/配额管理（本期）
- 不重写 Go 核心（保留 Rust 管理 + Go 网关的双进程架构）
- 巡检不自动剔除实例（只标记，自动停用留给网关路由层）
- 桌面模式保留现有 Tauri 窗口体验（无边框、托盘、最小化到托盘）

## 里程碑划分（建议）

1. **M1 架构重构**：core 抽取 + axum HTTP API + 前端 api.ts 改 HTTP —— 桌面/headless 双模式跑通
2. **M2 Linux 适配**：平台分支代码、CI 双平台、systemd 示例
3. **M3 功能完善**：订阅拉取、健康巡检、报表导出、日志过滤、配置化网关密钥/端口
4. **M4 打磨验证**：跨平台回归、文档、打包
