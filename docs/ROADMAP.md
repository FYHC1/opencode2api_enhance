# 开发路线图：多端 + 模型矩阵 + 供应商配置化

> 后续开发总路线，遵循 AGENTS.md 纪律（阶段化，每阶段 = 功能 + 测试 + 验证）。
> M 系列同时是 `docs/ARCHITECTURE-V2-PLAN.md` P5「多平台」的实施明细（P5-1~P5-4 → M1~M4）。

## 〇、优先级

| 方向 | 状态 | 说明 |
|---|---|---|
| **M 系列：多端 + Docker** | 🔥 **紧急，先做** | 有用户着急使用 Linux / 服务器版本 |
| 模型矩阵 + auto 路由 | 🕓 迭代方向（延后） | 10 节点 × 10 模型的口子选择、上下文规范、偏好约束 |
| 供应商配置化 + 插件 | 🕓 迭代方向（延后） | 供应商从"每次编码"走向"配置菜单 + 契约 + 可选插件" |

启动顺序：**M 系列（M1→M5）→ 模型矩阵（Q 系列）→ 供应商配置化（R 系列）**。Q / R 系列保持方向与阶段定义，不占当前排期。

---

## 一、M 系列：多端支持 + Docker（紧急）

### 现状盘点（已核实代码）

- `core/` 与 `vendors/` 纯 stdlib，编译层已跨平台；但 **Linux/macOS 的 `go build` 尚未验证**（ARCHITECTURE-V2-PLAN 表 17 ⬜）。
- `process_other.go`：非 Windows 已有 SIGKILL / signal-0 探活 ✅。
- `netstat_other.go`：**空桩**（返回 nil，非 Windows 端口清理不可用），需 lsof / procfs 替换。
- `autostart.go`：非 Windows 仅 GET 返回「未启用」、SET 报错——需 Linux `.desktop` / macOS LaunchAgent。
- `embed.rs`：`include_bytes!` 内嵌 `bin/opencode2api.exe` + `bin/sing-box.exe`——需按平台选择。
- `bin/`：当前仅 Windows 二进制。
- headless 已可跑：`opencode2api -port 40000 -password ... -config config.json` 即完整 Web 服务。

### 阶段 M1：三平台编译验证 + 系统调用替换（✅ 已完成，2026-08-12，提交 48912e3）

> **开发环境**：开发者本机为 Windows。跨平台部分依赖 Go 交叉编译与纯函数单测，
> 真机运行验证交给 CI（M4）与有对应环境的用户；Linux 部分可选在 WSL2 内复核。

**功能开发**
1. **编译矩阵验证**：Windows 上 `GOOS=linux` / `GOOS=darwin` / `GOOS=windows` 三种 `go build ./...` 全绿（core/vendors/main）。
2. **端口清理 `netstat_other.go`**：非 Windows 用 `lsof -iTCP:<port> -sTCP:LISTEN -t` 拿占用 PID（macOS/Linux 通用）；解析器做成纯函数（lsof 样例行 → pid 列表），Windows 上单测注入假行即可验证。
3. **开机自启 `autostart.go`**：纯文件逻辑，单测可验证生成内容：
   - Linux：`~/.config/autostart/opencode2api-<env>.desktop`（Type=Application / Exec=当前可执行文件）；
   - macOS：`~/Library/LaunchAgents/` plist（Label / ProgramArguments / RunAtLoad）；
   - GET 读文件存在性、SET 写/删，键名跟随数据目录（沿用 Windows 环境隔离约定）。
4. **托盘**：确认 tauri 托盘能力不引入平台专用 crate（Windows 可先本地验证托盘逻辑）。

**测试**：lsof 行解析 / .desktop / plist 生成的纯函数单测；`go build` 三平台矩阵（Windows 本机即时跑）。

**验证**：三平台编译全绿；单测覆盖非 Windows 路径；可选 WSL2 内跑 Linux core 复核实例启停与端口清理。

**验收**：三平台 `go build` 全绿；非 Windows 系统调用路径有单测覆盖（CI 在真实平台再跑真机验证）。

### 阶段 M2：内嵌二进制按平台（embed.rs + bin 准备）（✅ 已完成，2026-08-12，提交 92dca47）

**功能开发**
1. `embed.rs`：按 `cfg!(target_os)` 选平台二进制（windows→`.exe`，linux/darwin→无后缀），`bin/` 下放 `sing-box`（linux/darwin 版）与 `opencode2api`（本仓库 Go 构建，天然跨平台）。
2. 打包脚本/CI 下载 sing-box 各平台产物到 `bin/`（参考 `scripts/build-windows.sh` 现有模式，新增 `scripts/fetch-singbox.sh` 或 Makefile target）。
3. `binPath` 解析按平台后缀（`.exe` 仅 Windows）。

**测试**：cargo build 各平台通过；binPath 纯函数单测。

**验证**：Linux 上从 `bin/` 释放并启动 sing-box 实例成功。

**验收**：三平台 exe 均能释放对应 sing-box 并跑起实例。

### 阶段 M3/M4：打包配置 + CI 三平台产物（✅ 已完成配置，2026-08-12，提交 2f2b575；真机产物待 CI 触发验证）

> **打包策略**：本机（Windows）不再产出 mac/Linux 包。`tauri.conf.json` 的打包矩阵写好配置后，
> **三平台正式产物全部由 GitHub Actions 矩阵生成**（windows-latest / ubuntu-latest / macos-latest），
> 产物作为 release 附件。Windows 包仍可本机 `npm run tauri:build` 快速自测。

**功能开发**
1. `src-tauri/tauri.conf.json` 补 `bundle.linux`（deb/rpm/AppImage）与 `bundle.macOS`（dmg）；Windows NSIS/AppImage 配置保留。
2. GitHub Actions 新增三平台矩阵 job：拉依赖 → `go build` → `cargo tauri build` → 上传 artifacts，文件名 `opencode2api-<平台>-<版本>`。
3. 沿用仓库守卫：工作流仅对包含 `CI` 的提交信息触发（避免日常提交触发）。

**测试**：本地 `cargo tauri build`（Windows）冒烟；CI 首跑验证三平台产物齐全。

**验证**：三个产物可下载、可安装（真机由有对应环境的用户/维护者复核）。

**验收**：提交信息含 `CI` → 三平台各产 1 个完整包；日常提交不触发。

### 阶段 M5：Docker 支持（headless 容器化）（✅ 已完成，2026-08-12，提交 34fda77；docker build 待有 Docker 环境验证）

**功能开发**
1. `Dockerfile`（多阶段）：构建 Go core（含 dist 前端静态托管）→ 精简运行镜像（alpine/distroless）→ 拷贝 `opencode2api` 二进制 + `sing-box` + `dist/`。
2. 端口与数据隔离：容器内固定 `OPCODE2API_DATA_DIR=/data`、管理端口与网关端口经 env 注入（沿用三件套），`-listen 0.0.0.0`。
3. `docker-compose.yml` 示例：数据卷挂载 + 端口映射（管理面板 / 网关）+ 重启策略。
4. 文档：`docs/DEPLOYMENT.md` 补 Docker 小节（构建、run、升级、常见问题）。

**测试**：`docker build` 通过（Windows 用 Docker Desktop 即可构建 Linux 镜像）；容器内起管理器 → 浏览器访问面板 → 建实例/扫描/网关连通。

**验证**：服务端用户按文档 3 步跑起来；数据卷重启不丢。

**验收**：一条 `docker run`（或 compose up）起完整服务，headless 全功能可用。

### 阶段 M6：macOS 专项收尾（✅ 文档与代码就绪，2026-08-12，提交 44a0de3；真机验签/公证留待 CI 或 mac 环境）

**功能开发**：macOS 打包后真机验证——签名/公证策略（Gatekeeper 提示处理，可先未签名发布 + 右键打开说明）、`lsof` 端口清理确认、LaunchAgent 自启、托盘行为。

**验证**：macOS 上六页 UI + 实例生命周期 + 扫描 + 网关全通。

**验收**：macOS 交付 dmg；文档含 Gatekeeper 绕过说明。

---

## 二、Q 系列：模型矩阵 + auto 路由（迭代方向，延后）

**痛点（用户原话整理）**：10 个节点 × 每个节点约 10 个模型 = 约 100 个"对话口子"。能否测试每个口的延迟、选择调用哪个？不同模型上下文不同（有的 200k、有的 1M）怎么规范？用户只想用特定几种模型怎么约束？

**方向设计**
1. **模型 × 节点矩阵**：以「模型」为第一视角（而非现在的节点视角）。对每个（实例，模型）组合做轻量探测（复用 P1 探活通道，探测目标从 `/v1/models` 细化为逐模型最小请求），产出矩阵：口子可用性 / 延迟 / 质量分。
2. **上下文长度元数据**：模型清单（`vendors/opencode`、`vendors/windsurf` 等）补充 `context_window`（200k/1M…）；请求层校验超长输入、UI 展示。
3. **auto 模型路由**：`model=auto`（或默认路由）→ 按矩阵 + P2 质量分 + 用户偏好选择最优（模型，节点）组合；对用户偏好（"只用特定几种模型"）做模型白名单/权重配置。
4. **规范约束**：模型偏好配置（偏好列表 / 排除列表 / 上下文下限）进入 config + 设置页；`routing` 现有 `model_provider_map` 扩展为可配节点级约束。

**阶段**：Q1 模型元数据 + 上下文校验；Q2 矩阵探测与持久化；Q3 auto 路由 + 偏好配置；Q4 UI（矩阵视图 / 偏好设置）。

**承接点**：复用 `core/manager/poolquality.go`（探活通道）、`socks_perf.go`（质量路由）、`core/router`（模型→厂商路由）。

---

## 三、R 系列：供应商配置化 + 插件（迭代方向，延后）

**痛点（用户原话整理）**：供应商能不能做成配置菜单，不是每次都需要编码？确保约束好供应商应提供给上层的内容——简单的用配置实现，复杂的允许上传构建好的 Go 代码，写明模型接口调用方式、传入的 key 等。

**方向设计**
1. **供应商契约（Contract）**：明确每个供应商必须向 core 提供的最小集：`ID / 模型清单（含 context_window）/ 上游端点 / 鉴权（key 来源）/ 协议形态（OpenAI / Anthropic / Responses 兼容）/ 流式支持`。落到 `core/contract` 文档 + 接口约束。
2. **配置驱动供应商（简单）**：新增"通用 OpenAI 兼容 / Anthropic 兼容"供应商类型——纯配置（base_url + key + 模型列表 + 协议）即可接入，无需编码。
3. **插件供应商（复杂）**：用户上传构建好的 Go 代码（或指向模块），按契约实现接口；评估加载方式（子进程 RPC / go plugin / wasm），关键约束：**key 由用户侧传入、模型接口形态按契约固定**。
4. **UI**：设置页「供应商」菜单——列表 / 启用 / 配置表单（base_url、key、模型、协议）；插件上传入口。

**阶段**：R1 契约定义 + OpenAI/Anthropic 兼容配置供应商；R2 供应商管理 UI；R3 插件机制评估与落地。

---

## 附：分支与提交纪律

- 每个阶段一个分支、一阶段一提交；`go test -count=1 ./...` 全绿 + 对应平台构建通过才提交。
- M 系列完成后逐个合并回 main 并推送（沿用现有流程）。
- Q / R 系列启动前，本路线图更新为正式分阶段计划（仿 P 系列文档格式）。
