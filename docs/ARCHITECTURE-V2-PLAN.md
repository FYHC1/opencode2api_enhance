# 架构 V2 改造计划：多模型厂商 + 多端（本仓库唯一事实来源）

> **本计划书是本次改造的"唯一事实来源"**。后续交接（人 / AI）一律按本文档逐阶段、逐验收项推进，并按「五、验收计划表」实时更新完成状态。
>
> - 状态：**实施中**（当前 P0）
> - 分支：`feat/architecture-v2`（已从 `feature/debug-tooling` 分出，2026-08-08）
> - 基线日期：2026-08-08
> - 上游参考：本项目根目录；第二厂商原型 `D:\AI_Projects\windsurf-account-manager\source`

---

## 〇、背景与原始需求（为什么做这件事）

### 0.1 起因（用户原话整理）

> 随着社区的朋友不断地对本项目添砖加瓦，感觉项目的地基还是要打大，不然容易倒塌。需要对整个项目架构做梳理，核心目标：把内容拆分成各个独立的模块，兼容可扩展性，兼容后续各个不同的终端、平台，兼容后续增加其他的 API 模型。
> 比如我们现在是 opencode，明天要是有个智谱的（后改为 windsurf 账号池）也来做模型底层，我们需要一个上层的 models 接口返回 opencode 和智谱（windsurf）发出的全部可用的免费模型；上层请求时，中间层做分发。

### 0.2 两个改造维度

| 维度 | 需求 | 落点 |
|---|---|---|
| A·多模型厂商 | 上层 `/v1/models` 聚合所有厂商免费模型；请求由中间层分发（模型→厂商解析、厂商级 failover） | `vendors/` + `core/`（P2/P3） |
| B·多端多平台 | 未来出现 Web、Linux、macOS 等版本；一套代码、一个平台一个产物 | `ui/` + 打包矩阵（P4/P5） |

### 0.3 第二个厂商定为"账号池型"（重要）

第二个后端不是 API Key 型，而是 **Devin/Windsurf 账号池**（原型：`windsurf-account-manager`）。特征：

- 账号动态注册（临时邮箱 → 注册 → 换 session token，走 Devin/Windsurf 上游）；
- 请求时按健康度挑号，额度低自动换号；旧号 24h 冷却防无限注册；
- **用户全程无感**（核心诉求，见「三、3.3 无感对话动作」）。

### 0.4 目标一句话

> **加新模型厂商 = 在 `vendors/` 下加一个子文件夹 + 实现契约 + 配置登记一行，core 零改动；上层（UI）永远只跟 core 打交道。**

---

## 一、现状分析摘要（2026-08-08 基线快照）

### 1.1 三层架构现状

```
React 前端（6 页） ──invoke──> Rust 管理器（35 command） ──子进程──> Go 代理核心
src/                    src-tauri/src/                     main.go 等（根目录 .go）
```

### 1.2 Go 核心（改造主战场）

- `main.go` 单文件 **5320 行**：HTTP 服务、配置、鉴权、SOCKS5 代理池、上游调用、模型目录、三种协议 handler、管理面板、统计。零第三方依赖（纯 stdlib）。9 个测试文件，全部 mock/httptest、不触网。
- 路由：`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/models`、`/api/config`、`/api/stats`、`/api/reload`、`/api/node-status`、`/health`、`/`（内嵌管理面板）。
- **协议层已解耦**（最有价值资产）：三种入站协议 → 统一 OpenAI 格式（`convertRequest` main.go:1511）→ `buildUpstreamBody` 发出 → 响应转回各协议。新厂商接入点就在这个"统一出口"。
- **上游强耦合（硬编码，无抽象）**：
  - 5 个硬编码 URL：`opencode.ai/zen/v1/models`、`/zen/go/v1/models`、`/zen/v1/chat/completions`、`/zen/go/v1/chat/completions`、`registry.npmjs.org/opencode-ai/latest`（main.go:448/502/528/2008/2010）
  - 专属头 `x-opencode-client/project/session/request` + UA（main.go:2018-2022）
  - 鉴权语义 `go:`/`zen:` 前缀、`sk-` key 校验（main.go:1921-1958）
  - 免费判定 `-free` 后缀 + 魔数 `big-pickle`（main.go:1990-1993）
- **已具备可复用能力**：SOCKS5 代理池 3 模式（smart/failover/round_robin）+ 健康/坏池；流内超时 + 断点续写切换（gateway_timeout.go）；配置热更新（不重启不打断 SSE）；token/节点统计；调用日志。
- 入口 flags：`-port`（默认 8000）、`-config`、`-password`（默认 123456）、`-gateway`、`-log-level`、`-log-file`、`-version`（main.go:5229-5238）。

### 1.3 Rust 管理器（实质 Windows-only）

- 35 个 tauri command：实例/节点/扫描/统计/日志/设置全流程。
- Windows 专属点：内嵌 `bin/opencode2api.exe` + `bin/sing-box.exe`（embed.rs include_bytes!）；`taskkill`；`netstat -ano` 端口清理（非 Windows 空实现）；注册表开机自启；Clash Verge profiles 目录（`#[cfg(windows)]`）；NSIS 打包；`%APPDATA%`。
- 编译层面可跨平台（Cargo.toml 无 Windows-only crate），硬阻塞全在运行时。

### 1.4 windsurf-account-manager 原型盘点（P3 移植源）

| 模块（Rust） | 作用 | 与用户诉求对照 |
|---|---|---|
| devin_auth.rs | 注册链路（app.devin.ai + codeium.com） | ✅ 有 |
| devin_connect.rs | Connect-RPC protobuf 聊天（server.codeium.com） | ✅ 有 |
| proto_min.rs | 手写最小 protobuf 编解码 | ✅ 有 |
| tmaily.rs | 临时邮箱收码 | ✅ 有 |
| health.rs | 健康分（额度分 + 滚动窗口 trouble） | ✅ 有 |
| store.rs | SQLite 账号库 accounts.db | ✅ 有 |
| usage.rs / windsurf_api.rs | 用量查询 GetUserStatus | ✅ 有 |
| proxy_server.rs | 本地 OpenAI 兼容反代（127.0.0.1:3003，swe-1-6-slow） | ✅ 有 |
| **24h 冷却** | — | ❌ **没有，需新建** |
| **额度 ≤20% 预注册** | — | ❌ **没有，需新建**（只有 5% 干旱/0% 耗尽） |
| **中途无感换号** | — | 🔶 只有流建立前换号（MAX_HOPS=3），**流中不换**，需新建 |

> 迁移决策（已拍板）：三件缺失能力在 `vendors/windsurf/`（Go 移植版）内新建，不依赖原 Rust 项目。

---

## 二、已拍板决策（用户逐条确认，2026-08-08）

| # | 议题 | 决策 |
|---|---|---|
| 1 | 第二厂商形态 | **A**：账号管理逻辑整体移植进 `vendors/windsurf/`（Rust→Go 重写，vendor 自包含；无号即注册 / 额度≤20% 预注册 / 24h 冷却在 vendor 内实现） |
| 2 | 管理逻辑是否并入 core（P4 大决策） | **并入**：Rust→Go 移植，一份实现服务所有端（Web/桌面/服务器） |
| 3 | 同名模型冲突 | **前缀区分**：`opencode/x`、`windsurf/x` |
| 4 | Web 版定位 | **单用户/内网**（沿用现有密码鉴权；GitHub 已有人提多用户 PR，后续再调整，本次不做） |
| 5 | 新分支基线 | **从 `feature/debug-tooling` 分出** `feat/architecture-v2` |
| 6 | 兼容性红线 | 厂商特有信息（鉴权头、错误码表、会话标识、免费判定规则等）一律进厂商实现或配置，**不写死在 core**；有不确定处先问再定 |
| 7 | 客户端界面统一（客户新要求，2026-08-08） | **所有客户端（Win exe / mac / Linux / Web）一律复用同一套 `src/` 界面**：独享、实例池、节点池、统计、日志、设置六个页面，外观与交互保持一致；**桌面 exe 不设登录页**（壳启动 core 时 `-password ""` 关闭鉴权，与旧 exe 行为一致）；Web 版沿用密码鉴权 |
| 8 | 环境数据目录隔离（2026-08-09） | **每个运行环境独立配置空间**（`%APPDATA%\opencode2api-manager*`，经 `OPCODE2API_DATA_DIR` 注入，Go core 侧 `DefaultDataDir()` 读取）：`opencode2api-manager`（正式 release）／`-dev`（tauri dev）／`-test`（便携测试包 portable.txt）／`-web-dev`（web 开发）。实例池/配置/runtime 互不干扰；端口段亦按环境隔离（正式 18000+ / dev 30000+ / 便携 50000+）。新增环境一律按此命名约定追加 |
| 9 | 内嵌二进制更新（2026-08-09） | 壳释放内嵌 core/sing-box 时按**内容哈希**校验（非仅文件大小），避免不同构建恰好同长导致旧版残留 |
| 10 | 六页 UI 全平台唯一界面（2026-08-09） | **六页 UI（独享/实例池/节点池/统计/日志/设置）是全平台唯一事实界面**。任何终端（Win exe / Web / 未来 macOS / Linux）与任何技术栈实现的客户端，界面都必须与 exe 一致；复用 `src/` 或按该 UI 对应实现，**禁止另起一套界面**。历史 `feature/web-self-service` 分支的简单页面已废弃。新增页面/菜单须与六页对齐 |
| 11 | 账号池"非阻塞自动补齐" + opencode 恒无 key（2026-08-09） | **windsurf 池型厂商**：`min_available` 默认 **3**（1 在用 + 备用换号余量）。`EnsureReady` 非阻塞——可用 ≥1 即放行用户请求，差额由**后台 goroutine 并行补齐**（single-flight 防风暴）；仅池空（可用=0）才同步注册 1 个恢复服务，其余后台补。**opencode 上游恒无 key**：客户端任何 key 一律剥离不转发（免费档），模型解析恒优先 `-free` 变体。**windsurf 用账号自带 session token**（厂商自己的"key"）。`/v1/models` **恒返回免费模型**（不分鉴权），付费模型不展示，非 opencode 厂商模型不混入基础缓存（防错误前缀/重复） |

---

## 三、目标架构

### 3.1 文件夹结构

```
opencode2api_enhance_main/
├── core/                        ← 核心层（中间层，跨平台、可独立部署）
│   ├── contract/               ← 厂商契约：数据线束规格（接口 + 数据结构）
│   ├── protocol/               ← 协议转换：OpenAI / Anthropic / Responses ↔ 统一格式
│   ├── router/                 ← 分发：请求→选厂商；厂商内部→选节点代理
│   ├── aggregator/             ← 模型聚合：/v1/models 合并全部厂商模型
│   ├── gateway/                ← 横切：鉴权、统计、日志、配置热更新、超时续写
│   └── server/                 ← HTTP 路由 + 启动入口
├── vendors/                    # 厂商层（底层，一厂商 = 一子文件夹）
│   ├── opencode/               # 第一厂商（现有硬编码收拢处）
│   │   └── contract.go         # 实现 core/contract
│   └── windsurf/               # 第二厂商（账号池型，Rust→Go 移植 + 三能力新建）
│       ├── contract.go         # 实现 core/contract（含 PoolVendor）
│       ├── devin_auth.go       # 注册链路
│       ├── devin_connect.go    # Connect-RPC 聊天
│       ├── proto_min.go        # 最小 protobuf
│       ├── tmaily.go           # 临时邮箱
│       ├── health.go           # 健康分
│       ├── store.go            # 账号库（SQLite→Go）
│       ├── usage.go            # 用量查询
│       ├── cooldown.go         # ★新建：24h 冷却
│       └── autoreg.go          # ★新建：额度阈值预注册
├── ui/                         # 界面层（只做界面）
│   ├── web/                    # React 前端（复用现有 src/，浏览器可用）
│   └── desktop/                # Tauri 薄壳（窗口/托盘/内嵌二进制）
└── main.go                     # 入口：只做装配（读配置→注册厂商→启动 core）
```

### 3.2 厂商契约（core/contract）

**必给三件**：

| # | 契约 | 说明 |
|---|---|---|
| 1 | `Chat()` / `ChatStream()` | 聊天调用：非流式 + 流式（SSE） |
| 2 | `ListModels()` + `IsFree()` | 模型目录（动态抓取 + 静态兜底） + 免费判定 |
| 3 | `Auth()` | 鉴权：认证头构造、key 合法性判断 |

**建议三件**：模型能力元 `Model.Caps`（tools/thinking/vision/上下文）；错误语义 `ErrSemantics`（可重试/可切厂商/进坏账状态码，按厂商差异化）；健康状态 `Health()`。

**可选两件**：会话/身份头生命周期；版本探测/UA。

**账号池扩展接口（core 用类型断言发现，厂商可选实现）**：

```go
type PoolVendor interface {
    EnsureReady(ctx) error          // 请求前保证可用账号（必要时自动注册/换号）
    PoolStatus() PoolStatus         // 可用数 / 冷却数 / 干旱 / 全池最低额度%
    Acquire() (AcctID, error)       // 借号（受冷却与健康约束）
    Release(id)                     // 还号（进入 24h 冷却）
}
```

### 3.3 用户无感对话的完整动作（P3 验收核心）

```
请求进来 → 厂商是账号池型？是 → EnsureReady()（无可用号可认自动注册新号）
       → 挑健康号 → 发请求 → 成功：后台刷新该号用量
       → 该号额度 ≤ 20% → 立即预注册新号（后台，不阻塞用户）
       → 该号被限/报错 → 自动换另一个健康号续写（沿用现有断点续写机制）
       → 旧号 24h 冷却到期前不参与挑选（防无限注册）
```

### 3.4 模型聚合 + 分发（用户核心诉求）

- **聚合**：`/v1/models` 遍历所有已注册厂商目录 → 合并 → 每条带 `provider` 字段 → 免费过滤（opencode 免费模型 + windsurf `swe-1-6-slow` 同列表）。
- **分发**：`model=X` → 查"模型→厂商映射表"（如 `swe-1-6-slow → windsurf`）→ 未命中则遍历厂商目录谁提供 X → 兜底默认厂商 → 选中后走现有节点代理池路由。
- **厂商级 failover**：连续失败（5xx/429）→ 切到同样提供该模型的下一个厂商。

### 3.5 跨平台与打包（维度 B 的答案）

- **代码一份**：core + vendors 纯 Go 天然跨平台（现状已实践 9 目标交叉编译）；ui/web 浏览器通用；ui/desktop（Tauri）三平台壳。
- **一平台一包**（CI 矩阵一次出全，每个产物内嵌对应平台的 core + sing-box + 同一套 UI）：

| 平台 | 产物 | 用户拿到 |
|---|---|---|
| Windows | NSIS 安装 exe（现状）+ 便携 zip | 1 个安装包 |
| macOS | `.dmg`（内含 `.app`） | 1 个安装包 |
| Linux | `.deb`/`.rpm` + **AppImage**（单文件） | 1 个安装包或单文件 |
| Web/服务器 | 不打包：同一管理二进制启动即服务；公网部署放 Linux 或 Docker | 无安装 |

> 概念纠正：mac 惯例 .dmg/.app、Linux 惯例 .deb/.rpm（AppImage 才是"一个文件跑"），但**每个平台用户拿到的都是恰好 1 个完整产物**，与现状 Windows"1 个 exe"心智一致。

---

### P3-B 子计划（上游协议移植，抄作业源：`D:\AI_Projects\windsurf-account-manager\source\src-tauri\src\*.rs`）

> 目标：把 `vendors/windsurf/` 的三个接缝（Chatter / Mailbox / Registrar）+ 用量回写落到实处，打通"池型厂商全链路"。每个子步骤结束 `go test -count=1 ./...` 全绿。

| 子步骤 | 内容 | 对照（Rust） | 状态 / 备注 |
|---|---|---|---|
| P3-B1 | Connect-RPC 基础：最小 protobuf 编解码（varint / wire type 0/1/2/5）+ GetUserStatus 用量解析 | `proto_min.rs` | ✅ 2026-08-08：`vendors/windsurf/connect/proto.go` + golden 单测；commit `4a85882` |
| P3-B2 | Connect-RPC 客户端：request builders（clientMetadata/completionConfig/modelConfig/指纹）/ 帧解析（gzip/end）/ DoChat + OpenAI-SSE 流 | `devin_connect.rs` | ✅ 2026-08-08：commit `4a85882`；端到端 fake-HTTP 单测（帧解析 / SSE / DONE） |
| P3-B3 | 工具仿真：客户端 tools → XML `<tool_call>` 注入 system prompt，输出刮取转回 OpenAI tool_calls | `tool_emulation.rs` | ⬜ YAGNI 可跳过（免费档上游不支持原生 tools；无则先不接） |
| P3-B4 | Mailbox：TMaily（domains / generate / emails 轮询） | `tmaily.rs` | ✅ 2026-08-08：`vendors/windsurf/tmaily.go` + httptest 单测；commit `55ff34b` |
| P3-B5 | Registrar：注册链 connections → email_start → WaitCode → complete → post-auth/bootstrap → windsurf/continue → ExchangeDevinCode 换 session | `devin_auth.rs` | ✅ 2026-08-08：`devin_auth.go` + 全链 httptest；commit `55ff34b` |
| P3-B6 | 用量回写：GetUserStatus → SetPoolUsage；Chat 成功后异步刷新 + 周期刷新 | `usage.rs` / `windsurf_api.rs` | ✅ 2026-08-08：`usage.go` + 单测；commit `55ff34b` |
| P3-B7 | 流中无感换号：Chatter 流内错误 → 换号重发（与 core/gateway 断点续写衔接） | `devin_connect.rs`（原 Rust 仅流前 MAX_HOPS=3 重试） | ✅ 2026-08-08：`vendors/windsurf/midstream.go`；流内 error 事件 / 无 [DONE] 的 EOF → 已吐内容回卷 → 换号续接（与 `buildResumeBody` 同文）；commit `00ae506` |

> 完成后顺序验证：单号正常 / 429 自动换号 / 额度≤20% 预注册 / 第二天旧号解冻复用 / 中途断流换号续写。真实环境冒烟由用户提供（需 Devin 站点可达 + 临时邮箱服务可用）。

---

## 四、实施阶段（每阶段：目标 / 改动 / 验收；P0 已完成项打勾）

### P0 基线（当前）
- **目标**：干净起点，行为快照。
- **改动**：建分支 `feat/architecture-v2`；跑 `go test ./...` 全绿；记录路由/配置/`/v1/models` 现状。
- **验收**：测试全绿；行为快照记录在案（可回溯）。

### P1 拆 core（纯重构，零行为变化）
- **目标**：把 `main.go`（5320 行）按 6 个模块拆包。
- **改动**：
  - `core/contract/`：契约接口 + 数据结构（先定义，厂商层 P2 实现）。
  - `core/protocol/`：三种协议请求/响应类型 + 双向转换（迁出 main.go 相关函数：convertRequest、claudeStreamHandler、responsesStreamHandler、convertMessagesForUpstream 等）。
  - `core/router/`：上游请求构造与调用（暂不抽象，先整体搬入：callOpenCodeAPI(Stream)、buildOCRequestWithEndpoint、buildUpstreamBody）。
  - `core/aggregator/`：模型目录缓存、别名、免费判定、listModelsHandler（fetchModels、fetchGoModels、resolveModel、startModelRefresh）。
  - `core/gateway/`：鉴权/统计/日志/配置热更新/超时续写（apiKeyAuth、recordTokenUsage、startConfigWatcher、streamWithResume 等，gateway_timeout.go 整体迁入）。
  - `core/server/`：路由注册 + main() 入口（main.go 保留为薄入口，只做装配）。
  - 顺带清理：过时文档（DEPLOYMENT.md/RELEASE.md）、config.example.json 补全可选字段、版本号双轨说明。
- **验收**：每拆一个包 `go test ./...` 全绿；行为与基线一致（路由表、配置项、/v1/models 输出不变）。

> **排序决策（用户已拍板，2026-08-08）**：**P2 优先于 P1.2b~f**。理由：契约（P1.2a）已就绪，先落地多厂商能力；剩余包化（protocol/gateway/aggregator/router/server）在 P2/P3 改同一批代码时同步进行（P2 将新建 `core/aggregator` 与 `core/router`），避免二次搬运。计划表仍按原 P1.2 子计划追踪。

### P1.2 子计划（包化 core/*）

> 目标：把 package main 中的领域文件提升为独立包，强制单向依赖，为 P2 厂商接入铺路。每子步骤结束必须 `go test -count=1 ./...` 全绿才提交。

| 子步骤 | 内容 | 依赖方向 | 备注 |
|---|---|---|---|
| P1.2a | `core/contract`：Vendor / PoolVendor / Model / Caps / ErrRules / Health / PoolStatus（纯新增，零行为影响） | 无依赖 | 是 P2 的契约雏形 |
| P1.2b | `core/protocol`：协议类型 + 转换（types_*、convert.go、chat/anthropic/responses_protocol.go）迁入 | 无 core 内依赖 | 符号全部导出，引用方加包前缀 |
| P1.2c | `core/gateway`：socks5 池、http client、鉴权、统计、配置热更新、超时续写迁入 | → contract | 全局状态收敛为该包内状态 |
| P1.2d | `core/aggregator`：模型目录/别名/免费判定迁入 | → gateway（http client） | |
| P1.2e | `core/router`：上游调用/分发迁入 | → protocol、aggregator、gateway | P2 在此改为走契约 |
| P1.2f | `core/server`：handler + 路由 + main 装配 | → 以上全部 | main.go 收口为薄入口 |

> 依赖方向：`contract ← protocol/gateway/aggregator ← router ← server`；禁止反向。厂商实现 contract，被 router/aggregator 调用。

### P2 收厂商（第一个厂商：opencode）
- **目标**：定义契约并验证"契约可被实现"。
- **改动**：
  - `core/contract/` 定稿（必给 3 + 建议 3 + 可选 2 + PoolVendor）。
  - 新建 `vendors/opencode/`：把 P1 里 router/aggregator 中的 opencode 硬编码收拢为 `OpenCodeVendor`（URL、鉴权、专属头、免费判定、目录抓取、错误语义全部进厂商）。
  - `core/router/` 改为通过契约调用厂商；配置新增 `providers` 数组 + `routing`（model_provider_map、default_provider）。
  - 现有测试改为对 OpenCodeVendor 的 mock 断言（硬编码 URL 断言同步改写）。
- **验收**：单厂商配置下 `/v1/models` 输出与基线一致；分发单测覆盖映射/兜底/失败切换。

### P3 加厂商（第二家·账号池型）
- **目标**：双厂商并存，池型厂商全能力落地。
- **改动**：
  - 新建 `vendors/windsurf/`：Rust→Go 移植 devin_auth / devin_connect / proto_min / tmaily / health / store / usage / proxy 逻辑。
  - ★新建三能力：`cooldown.go`（24h 冷却）、`autoreg.go`（额度≤20% 预注册）、流中无感换号（沿用 core/gateway 断点续写 + 账号切换）。
  - 聚合：`/v1/models` 返回 opencode 免费模型 + `swe-1-6-slow`（前缀 `opencode/`、`windsurf/`）。
- **验收**：双厂商并存；池型"无号自动注册 / 额度≤20% 预注册 / 24h 冷却 / 中途无感换号"全链路通过（单测 + 冒烟）。

### P4 统一 UI + Web 版（★需先制定详细子计划再动工）——✅ 已完成（2026-08-09，win exe 交付）
- **目标**：管理功能并入 core（HTTP API），一份实现服务所有端。
- **执行策略（2026-08-08 定案：先 exe 后 Web，按大步验证）**：
  1. **大步 1 接线**（=P4-5b 核心）：`src/lib/api.ts` 的 `invoke` 全部换成 `fetch('/api/admin/*')`，TS 类型与 6 个页面零改动；同时把 Go 侧漏出的字段补齐（如 `GatewayStatus.free_models_loading`）。**验证**：`go test ./...` 全绿 + `npm run build` 通过 + 浏览器能打开页面。
  2. **大步 2 浏览器快速验收**（=P4-6 缩小版）：core 起在 `localhost:<port>`，浏览器把 6 页全链路走一遍（登录→节点→加实例→启动→测试→入网关池→网关状态→统计/日志），问题集中修。
  3. **大步 3 exe 出口**：Tauri 壳瘦身（窗口/托盘/自启/二进制路径）、壳拉起 core 管理器（`OPCODE2API_DATA_DIR`）、`cargo check` + NSIS → 交付 exe。**验证 `npm run tauri:build` 产出 exe + 桌面走查**。
  - 验证节奏：3 大步各一次完整，不做小步校验（用户拍板）；大步内保持正常提交。
- **子计划（细化版 2026-08-08，已开工；行为规格来自 `src-tauri/src/*.rs` 全量工读）**：

> **核心决策**：新增 Go 包 `core/manager` 承载管理域（实例生命周期/端口/clash 解析/sing-box 配置/probe 扫描/网关/统计/日志/应用配置），HTTP 层为 `/api/admin/*`（鉴权复用现有 `requireAuth` 会话 + `apiKeyAuth`）。实例模型保持"子进程"（与现状一致，最小侵入）：同一 core 二进制既可单独运行（单实例网关，现状），也可作为管理器 spawn 自身子进程当实例。壳专属命令（hide_to_tray/quit/toggle_maximize·窗口操作）不并入 core，保留 Tauri invoke；autostart/binaries 双轨（core 提供 HTTP 供 Web 用）。Windows 专属系统调用先行真实现，非 Windows 空桩（P5 替换）。

| 子步骤 | 内容 | 验收（全绿 `go test -count=1 ./...`） |
|---|---|---|
| P4-1 | `core/manager` 骨架：数据目录解析（`OPCODE2API_DATA_DIR` → `%APPDATA%/opencode2api-manager`）、应用配置（config.json / ConfigView / effective_default_password，字段与 config.rs 一致）、调用日志读取（`runtime/_unified-gateway/call_log.jsonl` → CallLogRecord）、统计聚合（runtime/*/stats.json → StatsSummary，`_unified-gateway` 名"统一网关"+node_stats）、`/api/admin/*` HTTP 路由骨架（鉴权复用） | manager 单测：配置 get/set、日志环形截断、stats 聚合（构造目录树）；route 注册 |
| P4-2 | 实例生命周期移植：Instance 契约（含外部标签状态 `Stopped|Starting|Running|Stopping|{Error:[msg]}`）；实例注册表 instances.json 持久化；add/remove/start/stop/test；spawn（`sing-box run -c` + `opencode2api.exe -port -config -password`，`-gateway` 子进程同）；pid 追踪、状态机、reconcile；端口工具（suggest LCG/check/is_free/wait）；Windows `netstat -ano` 端口清理 + `taskkill`（封装 executor 接口，测试注入 fake exec） | 单测：增删/启动停止（fake Runner）/端口冲突/状态机/reconcile/port 解析（windows 样例行）；`go build` 通过 |
| P4-3 | 节点/探针移植：clash.go（%APPDATA%/io.github.clash-verge-rev…/profiles `*.yaml` + profiles.yaml 名映射 + 外部 API `/configs` + junk 过滤 + name→group 前缀）→ ClashNode 契约；singbox.go 按类型（trojan/vless/vmess/ss/hysteria2/anytls + ws/http transport + reality/utls）生成 sing-box 配置；probe.go 扫描控制器（并发 8、进度状态、每节点 起 sing-box+opencode 探针 → `/v1/models` 免费模型挑选 → POST chat → ok/category/latency） | clash 样例 YAML golden、singbox 每类型字段断言、probe 控制器并发/取消/进度单测 |
| P4-4 | 网关移植：route_mode（smart/failover/round_robin）语义、join_gateway 成员、free_models 抓取节流、spawn `-gateway` 子进程（cwd=runtime/_unified-gateway）；batch 批量（add/start/stop/delete，重名端口+1 语义）；restart_pool；data_clean 三级；stats/reset（HTTP DELETE 语义）；call-log clear | 路由映射/端口分配/batch 去重/restart 顺序单测（fake） |
| P4-5 | 前端改走 HTTP + Tauri 薄壳化：`src/lib/api.ts` invoke→fetch('/api/admin/…')，类型不动；6 页逐页验证；React 构建产物由 core `/` 托管（Web 浏览器全功能）；Tauri invoke_handler 缩减为壳命令（窗口/托盘/自启/二进制路径），commands.rs 管理域删除 | `npm run build` 通过；`cargo check` 通过；浏览器 localhost:<port> 全功能走查记录 |
| P4-6 | 联动联调：同二进制三态（单实例网关 / 管理器+子实例 / Web 直连）；实例→扫描→批量→启停→网关→统计→日志全链路走查；桌面与 Web 等价 | 走查记录 + 验收 15/16 打勾 |

- **决策备注（自主授权，记录在案）**：① 管理包放 `core/manager`，依赖 contract 与既有网关设施，禁止反向；② `Error` 状态用外部标签数组形式 `{"Error":["msg"]}` 与 Rust serde 对齐；③ 探针每次换节点重启 sing-box（防模型目录缓存污染）；④ probe 使用裸 TCP HTTP 客户端（与 Rust http_get_json 语义一致，供 /api/reset-stats 复用）；⑤ Web 态默认开管理 API（受 requireAuth 保护），桌面态由 Tauri 壳设 `OPCODE2API_DATA_DIR` 隔离。
- **验收**：浏览器打开 `localhost:<port>/` 可用全部管理功能；桌面版功能与现状等价（实例启停/扫描/统计/日志全链路）。

**P4 进度日志**：

| 子步骤 | 状态 | 日期 | commit / 备注 |
|---|---|---|---|
| P4-1 管理 API 层 | ✅ | 2026-08-08 | `2cca241`：core/manager（config/calllog/stats/registry/tcp/netstat）+ `/api/admin/*`HTTP（config/stats/reset/call-log/binaries/instances），单测全绿 |
| P4-2 实例生命周期 | ✅ | 2026-08-08 | `797e2af`：Runner 抽象（CREATE_NO_WINDOW/taskkill/SIGKILL/pidAlive）、短锁启动（sing-box→opencode→等口）、stop/remove/reconcile/refresh、端口 LCG suggest/check/force-free、Content-Length 精确读；fake runner+占口单测 |
| P4-3 节点扫描探针 | ✅ | 2026-08-08 | `c7cb80f`：yaml.go 最小 YAML（零依赖）、clash_parse.go（本地 profiles+外部 API+junk/group 过滤）、singbox.go 逐类型配置、opencodecfg.go 实例/网关配置、probe.go 并发控制器+逐节点免费模型测试；main 装配 SeamFuncs；单测全绿 |
| P4-4 网关/批量/restart/data_clean | ✅ | 2026-08-08 | `0e7d958`：gateway.go（-gateway 子进程、成员=Running&&join_gateway、空即停/配置变重启/自动拉起、免费模型节流抓取）、batch.go（按节点去重+自动命名+端口+1、并行 4/8 worker）、restart_pool.go（停网关→全停→强清端口→并启成员→网关收尾）、data_clean.go（三级别含 .bak）；单测：gateway 启停/去重/data_clean 三级/restart 顺序 |
| P4-5a 管理 API 全表面 + SPA 托管 | ✅ | 2026-08-08 | `905af85`：/api/admin 操作面齐全（节点/实例 CRUD·启停·刷新·测试/批量/端口/扫描/网关/入池/重启池/清数据/自启壳独占）；frontend dist 存在即托管 SPA；main SetDeps；HTTP 冒烟单测全绿 |
| P4-5b 前端 api.ts 改走 fetch + Tauri invoke_handler 缩减 | ✅ | 2026-08-09 | 大步1 ✅ `e0196c3`；大步2 自动化 ✅ `ae281b6`（E2E 21 项 PASS + 修 requireMethod 反写 bug）；大步3 ✅：壳瘦身 `c79312f` + cargo check 绿 + `tauri build --no-bundle` 产出 exe（1m50s）；**已交付 win exe 供客户使用** |
| P4-6 联动联调 + 用户验收驱动修复 | ✅ | 2026-08-09 | 用户真机验收驱动 12 个修复提交：端口隔离 `e6d7164`/sanitize `82f044f`/Job Object `dcb566e`/文案对齐 `d456715`+`6e1aab2`/autostart 移 core `8703913`/节点池按钮+行点击 `54d6875`+`799747b` 等；**win exe 可交付**（NSIS 安装包仍可选未做） |

### P5 多平台（Linux/macOS）
- **目标**：壳层跨平台 + 打包矩阵。
- **子计划（开工前细化）**：P5-1 端壳系统调用替换（端口清理→lsof /proc；开机自启→.desktop/LaunchAgent，可考虑 auto-launch crate；clash 目录按平台给配置）→ P5-2 embed.rs 按 `cfg!(target_os)` 选平台二进制 + CI 矩阵下载 sing-box → P5-3 tauri.conf 加 deb/rpm/AppImage（Linux）、dmg（macOS）→ P5-4 CI 三平台产物联验。
- **改动**：内嵌二进制按平台（cfg!(target_os) + CI 矩阵）；替换 Windows 系统调用（端口清理/开机自启/进程终止/Clash 目录）；tauri.conf.json 加 deb/rpm/AppImage、dmg；CI 平台矩阵。
- **验收**：Linux/macOS 上跑通 实例启停 + 节点扫描 + 网关 + 托盘；CI 每平台产出完整包。

---

## 五、验收计划表（完成一项勾一项，逐步更新）

> 维护规则：每完成一个验收项，把该行 `状态` 改为 ✅（注明完成日期与验证命令/证据）。每阶段完成时更新一次"阶段状态"。

| # | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 / 备注 |
|---|---|---|---|---|---|
| 1 | P0 | 分支 `feat/architecture-v2` 建立（自 feature/debug-tooling） | ✅ | 2026-08-08 | `git branch --show-current` |
| 2 | P0 | `go test ./...` 全绿 | ✅ | 2026-08-08 | `go -C <proj> test -count=1 ./...`（全绿）+ `go vet ./...` |
| 3 | P0 | 行为快照记录（路由表/配置/`/v1/models` 输出） | ✅ | 2026-08-08 | 见「一、现状分析摘要」 |
| 4 | P1.1 | 文件拆分：main.go(5320行) → 21 个同包领域文件，main.go 仅留入口 | ✅ | 2026-08-08 | commit `dcb217b`；`go test -count=1 ./...` 全绿 |
| 4b | P1.2 | 包化：contract/aggregator/router/manager/protocol 五包完成；gateway/server 状态收敛未做 | 🔄 | 2026-08-09 | **P1.2a~b 完成**：contract（`de91054`）、protocol（类型+纯转换全下沉 `fe942b2`，含 claude/anthropic/responses 转换 + 类型别名桥）、aggregator/router（P2 期间）、manager（P4 期间）。**地基加固（2026-08-09，未提交）：** ① 模型目录双轨消灭——`models.go` 直连拉取（fetchModels/fetchGoModels 硬编码 URL）删除，`/v1/models` 与 reload 全走聚合器+厂商；② 全局 OpenCode 会话（ocSessionID/initOCSession 等 4 个全局）删除，会话收拢进 `vendors/opencode` 实例；③ 适配层复用聚合器已注册实例，消灭双实例双会话；④ 契约净化：`contract.Message.Extra` 厂商私有区，`Options` 保留通用键；⑤ 契约律己：`ErrSemantics().Retryable` 成为 chat 重试唯一状态码来源；⑥ Chat/ChatStream 合并为 `call()`；⑦ 聚合器倒排索引 `ProvidersOf`，路由 O(1)；⑧ convert.go Anthropic 死代码（约 190 行）删除。**P1.2c/f（core/gateway 状态收敛 + core/server handler 依赖注入）仍待决策**：剩余全局约 35 个（config 组/socks 池/超时续写组，均已按组加锁），收敛需重写 handler 与 13 个测试约 60 处 save/restore，估 8-14 天；win exe 已交付前提下纯内务收益低，建议作为独立专项排期 |
| 5 | P1 | 拆分过程每阶段测试全绿，行为与基线一致 | ⬜ | | 每步 `go test -count=1 ./...`；P4 大量改动后仍全绿 |
| 6 | P1 | 过时文档清理 + config.example.json 补全 + 版本号口径说明 | ⬜ | | 随 P1.2 收尾处理 |
| 7 | P2 | `core/contract` 定稿（基础 + PoolVendor + Tier/Transport/Stream/Meta） | ✅ | 2026-08-08 | 见 `core/contract/contract.go`；commit `de91054` |
| 7b | P2 | `core/aggregator` 聚合层（合并/刷新/隔离） + 单测 | ✅ | 2026-08-08 | commit `de91054` |
| 8 | P2 | `vendors/opencode/` 实现（会话/目录/免费/错误语义） | ✅ | 2026-08-08 | commit `95bbc82`（包）`36be5df`（目录装配）`63344ec`（Chat/ChatStream+测试） |
| 8b | P2 | P2-B1：目录经聚合器装配（main 启动 + startModelRefresh 换源） | ✅ | 2026-08-08 | 单厂商行为与基线一致，全部测试绿 |
| 8c | P2 | P2-B2：vendor Chat/ChatStream 完整实现 + 4 项单测（zen/go 端点、429 重试、Anthropic 转换） | ✅ | 2026-08-08 | commit `63344ec` |
| 8d | P2 | P2-B3：main handler 切流（chat 经 vendor），旧 upstream 实现移除，测试迁移 | ✅ | 2026-08-08 | commit `e5c391c`；旧重试/go端点/4xx用例经适配层全绿 |
| 9 | P2 | 硬编码 URL 断言测试改写为对厂商 mock | ✅ | 2026-08-08 | buildOCRequest 用例删除，路由语义迁至 `vendors/opencode/chat_test.go` |
| 9c | P2 | P2-C1：providers[] + routing 配置（AppConfig/applyConfig/config.example.json） | ✅ | 2026-08-08 | `7009327` + `65138bf` |
| 9d | P2 | P2-C2：`core/router`（模型→厂商解析，failover 序）+ 单测 | ✅ | 2026-08-08 | `7ab4d82` |
| 9e | P2 | P2-C3：适配层经路由器分发 + 厂商级 failover（Switchable/5xx/传输错；非可换即停）+ 单测 | ✅ | 2026-08-08 | `a8ed459`；429→切换通过、403→停 |
| 9f | P2 | P2-C4：`/v1/models` 多厂商聚合（同名加厂商前缀；单厂商零变化）+ 单测 | ✅ | 2026-08-08 | `b045d94` |

> **P2-B3 续接注记（已清）**：convert.go 中 Anthropic 转换重复代码（死代码）已于 2026-08-09 删除（约 190 行），核心链路全部经 `contract` 与 `vendors/`。
| 10 | P3 | `vendors/windsurf/` 池型厂商（池/冷却/健康/自动注册/借号换号 + PoolVendor 契约） | ✅ | 2026-08-08 | 池层+契约 ✅ `edde8b8`；接缝（Chatter/TMaily/Registrar/用量/流中换号）已全部落地 P3-B1~B7 |
| 10b | P3 | P3-B：Chatter（Connect-RPC 协议移植）/ Mailbox（TMaily）/ Registrar（devin_auth 注册链）真实实现 | ✅ | 2026-08-08 | B1~B6 `4a85882`/`55ff34b`；B7 流中无感换号 `00ae506`；全链路单测绿，待真机冒烟 |
| 11 | P3 | ★24h 冷却 / ★额度≤20% 预注册 / ★中途无感换号 三能力完成 | ✅ | 2026-08-08 | 冷却+预注册 ✅（池层 `edde8b8`）；流中无感换号 ✅（`00ae506`，回卷续写与网关断点续写同文衔接） |
280→| 12 | P3 | `/v1/models` 双厂商聚合（前缀区分），分发与厂商级 failover 通过 | ✅ | 2026-08-08 | 聚合/前缀/分发/failover 已由 P2-C 落地（待 windsurf 真接） |
| 13 | P3 | 池型全链路冒烟：无号自动注册→对话→额度低预注册→换号续写 | ✅ | 2026-08-09 | **真机通过**：`SMOKE_REAL=1 go test -run TestVendorChat` —— TMaily 真实邮箱 → Devin 真实注册链 → 真实对话 swe-1-6-slow 返回 "OK"（16.6s）；外部服务（tmaily.com/devin.ai/codeium.com）当前可用；冒烟测试门控保留在 `vendors/windsurf/smoke_vendor_test.go` |
| 14 | P4 | P4 详细子计划制定（P4-1~P4-6） | ✅ | 2026-08-08 | 「四、P4」细化版：核心决策+验收点+决策备注；行为来源 `src-tauri/*.rs` 工读 |
| 15 | P4 | 管理功能并入 core（HTTP API），浏览器全功能可用 | ✅ | 2026-08-09 | P4-1~P4-5 全落地（commit `2cca241`/`797e2af`/`c7cb80f`/`0e7d958`/`905af85`）；E2E HTTP 21 项 PASS |
| 16 | P4 | 桌面版功能与现状等价；Tauri 薄壳化 | ✅ | 2026-08-09 | 大步1~3 完成：api.ts 走 HTTP（`e0196c3`）→ 壳薄壳化（`c79312f`）→ exe 交付（`7022b95`）；随后 12 个修复提交收敛到可用 |
| 16b | P4 | 大步3 之后系统性修复（用户验收驱动） | ✅ | 2026-08-09 | 端口隔离（`e6d7164`）、sanitize 实例名（`82f044f`）、Job Object 防孤儿（`dcb566e`）、文案逐字对齐（`d456715`/`6e1aab2`）、autostart 移 core（`8703913`）、节点池入池/独享+行点击（`54d6875`/`799747b`）；**win exe 已可交付客户使用** |
| 17 | P5 | core/vendors 在 Linux/macOS 编译通过 | ⬜ | | |
| 18 | P5 | 壳层 Windows 系统调用替换 + 内嵌二进制按平台 | ⬜ | | |
| 19 | P5 | CI 打包矩阵出全（Win NSIS / mac dmg / Linux deb+rpm+AppImage） | ⬜ | | |

### 阶段状态汇总

| 阶段 | 状态 |
|---|---|
| P0 基线 | ✅ 已完成（分支已建，测试全绿，行为快照已记录） |
| P1 拆 core | ✅ 核心完成（P1.1 文件拆分；P1.2a/b/d/e + P4 期间 manager 包：contract/protocol/aggregator/router/manager 五包就位；仅 core/gateway 状态收敛 + core/server handler 依赖注入待决策——纯内务重构，协议层已独立复用） |
| P2 收厂商（opencode 收拢） | ✅ 完成（契约/聚合/分发/failover 全绿；单厂商行为与基线一致） |
| P3 池型厂商（windsurf） | ✅ 池层+全接缝完成（P3-A 池层 `edde8b8`；P3-B1~B7 上游协议移植 `4a85882`/`55ff34b`/`00ae506`；三能力：冷却/预注册/流中无感换号全部落地） |
| P3 加厂商 | ✅ **已完成**（池型全链路真机冒烟通过 2026-08-09：无号自动注册→真实对话 swe-1-6-slow；后续额度预注册/流中换号单测覆盖，可随用随验） |
| P4 统一 UI | ✅ **已完成并可交付**（管理功能并入 core HTTP API → 前端走 HTTP → 壳薄壳化 → exe 打包 → 系统性验收修复；win exe 已能让客户使用） |
| P5 多平台 | ⬜ 未开始 |

---

## 六、开发纪律（延续本项目已有 "阶段回滚" 心法）

1. **阶段化**：P0→P5 分阶段；复杂处（P4、P3 的 Go 移植）预先拆分**子开发计划**，每个子阶段走"实现→测试→验证"闭环。
2. **验证不过顺延**：阶段末验证未通过的项，**并入下一阶段计划"上阶段遗留问题"节**修复，不静默带过。
3. **每阶段一提交**：每阶段一个 commit（含测试），测绿才推进；真行为不变阶段（P1）与新增抽象阶段（P2 后）分开提交。
4. **兼容性红线**：厂商特有信息一律进厂商层/配置，不进 core 写死；拿不准先问用户。

---

## 七、风险与遗留问题

1. P2 抽厂商时硬编码 URL 断言需同步改写（测试=行为契约）。
2. P4 Rust 移植量大：逐命令对照验证，按子计划分步落地。
3. windsurf 原项目依赖 TMaily 公共 API（`tmaily.com`）——第三方可用性是外部风险；移植时保留其重试/兜底逻辑。
4. 本地环境：`cargo test` 受 WinLibs 工具链限制（`STATUS_ENTRYPOINT_NOT_FOUND`，CI 无此问题）——Rust 侧验证以 `cargo check` + CI 为准。
5. 过时文档与版本号双轨（CHANGELOG v1.x vs package.json 0.1.1）随 P1 清理。

---

## 八、参考附录

- 本项目关键代码位置：main.go（路由 5292-5313 / 上游调用 2046-2184 / 模型 500-644 / 鉴权 1921-1958 / 配置 1136-1248）；gateway_timeout.go（流内超时续写）。
- 第二厂商原型：`D:\AI_Projects\windsurf-account-manager\source\src-tauri\src\*.rs`（模块映射见「一、1.4」）。
- 会话计划快照：本文件即为唯一事实来源，无需旁挂。
