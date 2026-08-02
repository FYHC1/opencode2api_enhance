# 阶段 1：管理器精简重构 + macOS 风格 UI + 自包含安装包

> **状态：** 计划已就绪
> **For agentic workers:** 按 Task 顺序执行；每 Task 测完再进下一 Task。
> **元规范:** `docs/ai-framework/phased-plan-driven.md`

**Goal:** 把 Rust 管理器做成「单文件自包含 exe + 可选安装目录的安装包」，UI 改为 macOS 风格，以「扫描勾选 → 批量添加」为核心流程，实例管理支持单个/批量操作。
**Architecture:** 后端 `src/web.rs` 保留全部现有 API 并新增批量操作 API；前端 `static/index.html` 重构为 Tab 布局 + macOS 风格；`include_bytes!` 嵌入两个子程序（opencode2api.exe、sing-box.exe）并在启动时释放；Inno Setup 制作安装器。
**Tech Stack:** Rust (axum/tokio) + 原生 HTML/CSS/JS + Inno Setup 6

---

## 前置阅读（必须）

| 优先级 | 文件 |
|--------|------|
| P0 | `docs/ai-framework/phased-plan-driven.md` |
| P0 | 本文件 |
| P1 | `AGENTS.md`、`CODE_REVIEW.md` |
| P1 | `src/web.rs`、`src/instance.rs`、`src/main.rs`、`static/index.html` |

**仓库路径：** `D:\Projects\opencode2api`
**基线分支：** `feat/phase-1-manager-lite`

---

## Global Constraints（冲突时以本节为准）

1. 最小修改原则：不动 Go 代理（main.go）与现有后端逻辑，只做增量修改。
2. 密钥/凭证不进 git。
3. **明确不做（本阶段）**
   - ❌ 不动 `main.go`（Go 代理核心）
   - ❌ 不删现有 API 端点（扫描/批量添加是核心功能，保留）
   - ❌ 不做自动更新、开机自启（后续阶段）
4. **Git：** 小步 commit；**默认不 push**。
5. **YAGNI：** 不加配置项、不加未要求的功能。

---

## 与前后阶段

| 阶段 | 状态 | 交付 |
|------|------|------|
| 上阶段 | ✅ | Rust 管理器 Web UI 初版（当前基线） |
| **本阶段** | ⬜ | 精简 UI + macOS 风格 + 自包含 exe + 安装包 |
| 下阶段 | | 开机自启/托盘/自动更新（勿塞进本阶段） |

---

## File Structure（预期变更）

| 文件 | 动作 | 职责 |
|------|------|------|
| `static/index.html` | 重写 | macOS 风格 + Tab 布局 + 批量操作 |
| `src/web.rs` | 修改 | 新增批量 API（start/stop/delete） |
| `src/embed.rs` | 新建 | 嵌入并释放 opencode2api.exe / sing-box.exe |
| `src/instance.rs` | 修改 | binary_dir 改为可执行文件所在目录/bin |
| `src/main.rs` | 修改 | 启动时先释放内嵌二进制；serve 命令 |
| `Cargo.toml` | 修改 | release 优化配置（strip/lto） |
| `installer/manager-installer.iss` | 新建 | Inno Setup 安装脚本 |
| `Makefile` | 修改 | 新增 release/installer 目标 |
| `docs/ai-framework/plans/phase-1-manager-lite-installer.md` | 新建 | 本计划 |

---

## 配置或 API 契约（如有）

### 新增批量操作 API

```
POST /api/instances/batch/start  { "names": ["a","b"] }
POST /api/instances/batch/stop   { "names": ["a","b"] }
POST /api/instances/batch/delete { "names": ["a","b"] }
```

响应：`{ "ok": true, "data": { "success": ["a"], "errors": { "b": "原因" } } }`
（批量 delete 仅允许非运行状态；运行中的实例需要先 stop）

### 内嵌资源释放契约

- 启动时检查 `<exe所在目录>/bin/opencode2api.exe` 与 `bin/sing-box.exe`
- 不存在或大小不匹配（嵌入资源与本地文件不同）→ 覆盖写入
- 失败仅告警，不阻断启动（后续启动实例时会再报错）

---

## Task 1：内嵌二进制释放模块

**Files:** `src/embed.rs`（新建）、`src/main.rs`、`Cargo.toml`、`src/instance.rs`

**行为:** 启动时把内嵌的 `opencode2api.exe` 与 `sing-box.exe` 释放到 exe 旁 `bin/` 目录；`InstanceManager` 的 `binary_dir` 改为以 `current_exe` 所在目录为准。

**Steps:**

1. 新建 `src/embed.rs`：
   - `include_bytes!("../bin/opencode2api.exe")`、`include_bytes!("../bin/sing-box.exe")`
   - `pub fn ensure_binaries(bin_dir: &Path) -> Result<()>`：不存在或文件大小不同则写入，返回写入与否
2. `src/main.rs`：`mod embed;` 在 `main()` 开头调用 `embed::ensure_binaries(...)`（目录 = exe 旁 `bin`），失败仅 eprintln 警告
3. `src/instance.rs`：`make_manager` / `create_manager` 中 `binary_dir` 用 `std::env::current_exe()` 的父目录 join `bin`
4. 跑：`cargo build` 期望：exit 0；`cargo test` 期望：全部通过
5. Commit：`✨feat(manager): 内嵌并自动释放 opencode2api 与 sing-box 二进制`

---

## Task 2：批量操作后端 API

**Files:** `src/web.rs`

**行为:** 新增 `/api/instances/batch/start|stop|delete` 三个端点，逐个操作并汇总成功/失败。

**Steps:**

1. 定义 `BatchOpBody { names: Vec<String> }`
2. `api_batch_start/stop/delete`：遍历 names，调用 `mgr.start_instance`（带默认密码）/`stop_instance`/`remove_instance`，收集 `{success, errors}`
3. 挂路由；`delete` 前先检查 Running 则报「请先停止」
4. 跑：`cargo build` + `cargo test` 期望：exit 0
5. Commit：`✨feat(manager): 实例批量启动/停止/删除 API`

---

## Task 3：macOS 风格 Tab UI 重写

**Files:** `static/index.html`

**行为:** 重写为：
- macOS 窗口栏：红黄绿交通灯 + 居中标题
- 深浅色跟随系统（`prefers-color-scheme`）
- Tab 1「扫描添加」：加载节点 → 一键扫描 → 结果表勾选 → 批量添加（起始端口）；保留扫描进度
- Tab 2「实例管理」：实例表 + 表头全选框 + 单个/批量操作（启动/停止/删除/测试）
- 设置卡片：管理密码
- 保留全部现有 JS 功能（api 封装、toast、扫描轮询）

**Steps:**

1. 重写 `static/index.html`（约 600-700 行，内联 CSS/JS）
2. 手动检查：无未使用元素 id；Tab 切换逻辑正确
3. 跑：`cargo build`（include_str! 编译嵌入）期望：exit 0
4. Commit：`🎨style(manager): 重构为 macOS 风格 Tab 界面`

---

## Task 4：release 优化与自包含验证

**Files:** `Cargo.toml`、`Makefile`

**行为:** release 构建开启 strip + lto，产出单文件 exe；验证脱离 bin 目录可自释放。

**Steps:**

1. `Cargo.toml` 添加 `[profile.release] strip = true, lto = "thin", codegen-units = 1, panic = "abort"`
2. `Makefile` 添加 `release-win`、`installer` 目标
3. 跑：`cargo build --release` 期望：exit 0；`target/release/opencode2api-manager.exe` 存在
4. 验证：临时把 exe 拷到空目录运行，确认自动生成 `bin/` 且两文件大小与 `bin/` 下一致
5. Commit：`🔧chore(manager): release 优化与构建目标`

---

## Task 5：Inno Setup 安装包

**Files:** `installer/manager-installer.iss`（新建）、`Makefile`

**行为:** 用 Inno Setup 6 制作 exe 安装器：可选安装目录、桌面/开始菜单快捷方式。

**Steps:**

1. 安装 Inno Setup：`winget install --id JRSoftware.InnoSetup -e`
2. 编写 `installer/manager-installer.iss`：
   - `DefaultDirName={autopf}\opencode2api-manager`（用户可选目录）
   - `[Files]` 打包 `target/release/opencode2api-manager.exe`
   - `[Icons]` 桌面 + 开始菜单快捷方式
   - `[Run]` 可选安装后启动
3. 跑：`iscc installer\manager-installer.iss` 期望：生成 `Output\opencode2api-manager-setup.exe`
4. Commit：`🔧chore(installer): Inno Setup 安装脚本`

---

## 验收标准总表

| # | 标准 | 通过条件 |
|---|------|----------|
| 1 | 构建 | `cargo build` / `cargo build --release` exit 0 |
| 2 | 单测 | `cargo test` 全部通过 |
| 3 | 自释放 | 空目录运行 exe，自动生成 bin/ 且文件大小一致 |
| 4 | 批量 API | 手动 curl 批量 start/stop/delete 返回成功汇总 |
| 5 | 安装包 | iscc 编译产出 setup.exe |
| 6 | 红线 | 无禁止项；密钥未入库；`git ls-files` 无 bin/*.exe |
| 7 | UI | 深浅色跟随系统；Tab 切换正常；批量勾选操作可用 |

---

## 风险与降级

| 风险 | 缓解 |
|------|------|
| 嵌入 sing-box 45MB 导致编译慢/体积大 | 可接受（~60MB exe）；若无法接受可退化为 bin 目录模式 |
| winget 安装 Inno Setup 失败 | 手动下载安装包或改用 NSIS |
| include_bytes 在无 bin/ 目录时编译失败 | 提供脚本先 `go build` + 下载 sing-box 至 bin/；文档说明 |

---

## 给接手 AI 的完整提示词

将下面整段粘贴给执行 AI 即可开工：

---

你是负责 **opencode2api** 的实现代理。请**完整执行本阶段**，不要只写方案。

### 基线
- 目录：`D:\Projects\opencode2api`
- 从 `main` 创建并切换：`feat/phase-1-manager-lite`
- 已完成：Rust 管理器 Web UI 初版（含扫描/批量添加/实例管理）
- 唯一实施计划：`docs/ai-framework/plans/phase-1-manager-lite-installer.md`
- 必读：`docs/ai-framework/phased-plan-driven.md`、`AGENTS.md`

### 做
1. Task 1：`src/embed.rs` 内嵌并释放两个子程序；`binary_dir` 改为 exe 旁 `bin/`
2. Task 2：`src/web.rs` 新增批量 start/stop/delete API
3. Task 3：重写 `static/index.html`（macOS 风格 + Tab + 批量操作 + 深浅色跟随系统）
4. Task 4：release 优化 + 自包含验证
5. Task 5：Inno Setup 安装包

### 不做
- 不动 `main.go`（Go 代理）
- 不删现有 API 端点
- 提交密钥或 bin/*.exe 入库；未授权的 `git push`

### 工作方式
1. 先跑 `cargo test` 确认基线干净。
2. 严格按计划 Task 顺序；每 Task 构建测试后 commit。
3. 证据优先：完成前必须重跑计划中的验证命令。
4. 用简体中文回复进度；代码标识符保持原样。

### 交卷
全部完成后给出：分支名、提交列表、验收表自评、测试/构建结果、残留风险。

现在开始：读完本阶段计划，从 Task 1 执行到最后。

---

## 残留手工验收清单

（自动化之外的 GUI / 真机项）

1. 双击 `target/release/opencode2api-manager.exe`，浏览器打开界面确认 macOS 风格渲染正常
2. 安装 setup.exe，选择自定义目录，确认快捷方式可启动、bin/ 自动生成
3. 扫描 → 勾选 → 批量添加 → 批量启动 → 客户端调用 `/v1/models` 验证
