# 阶段 W2：opencode 免费档迁移到 /inference/v1 并复刻 v2 客户端指纹

> **状态：** 已完成（2026-10-15）
> **For agentic workers:** 按 Task 顺序执行；每 Task 验证后再进下一 Task。
> **元规范:** `docs/ai-framework/phased-plan-driven.md`

**Goal:** 让本仓库的 opencode 免费通道（`Authorization: Bearer public`）重新拿到 200，方式是按上游 2026-10-14 的新门禁契约对齐端点、身份头与工具指纹。

**Architecture:** 上游把 keyless 免费推理从 `/zen/v1/*` 迁到 `/inference/v1/*`，并对请求体工具声明加严（必须带真实 `shell/glob/grep/read` 定义）。改动全部落在 `vendors/opencode` 包内：端点常量按 tier 分支、身份头集中到一个函数、body 门禁（gate.go）改注入真实工具定义。

**实施档位：** 全能（默认）
**子代理：** 启用（独立审查 + 验收）

---

## 前置阅读（必须）

| 优先级 | 文件 |
|--------|------|
| P0 | `docs/issue-log/2026-10-15.md`（本阶段取证） |
| P0 | `docs/issue-log/2026-10-13.md`（上一轮 403 排查，身份串时间语义） |
| P0 | `vendors/opencode/gate.go`、`chat.go`、`responses.go`、`opencode.go` |
| P1 | `AGENTS.md`、`docs/ai-framework/phased-plan-driven.md` |

**仓库路径：** `D:\AI_Projects\opencode2api_guide\opencode2api_enhance`
**基线分支：** 从 `main` 拉 `fix/phase-w2-opencode-inference-v2`

---

## Global Constraints

1. 只改 `vendors/opencode`（+ 测试、+ 文档）；不碰网关/前端/契约层。
2. 密钥不进 git；`*_test.go` 仍受 `.gitignore:52` 影响，测试改动**不自动入库**（登记为遗留）。
3. **明确不做（本阶段）**
   - ❌ 跑真实 `opencode serve` 客户端进程方案（实测不需要，见取证）
   - ❌ 改付费 `zen`/`go` 通道端点（上游仅对 keyless 免费档下线 `/zen/v1`）
   - ❌ 模型目录端点迁移（`/zen/v1/models` 与 `/inference/v1/models` 均实测 200，保持不动）
4. **Git：** 小步 commit；**默认不 push**。
5. **YAGNI：** 不引入配置开关矩阵；仅保留既有 `client_version` 覆盖项。

---

## 阶段开头：上阶段遗留

| 上阶段遗留项 | 来源 | 未通过原因 | 状态 |
|-------------|------|-----------|------|
| opencode 403 FreeTierError 未根治（登记为「上游侧」） | 2026-10-13 验收 | 本轮定位为**上游端点迁移 + 工具契约变更**，非纯上游风控 | 本阶段修复 |
| `refreshOCSession` 里 `v.ocOnce = sync.Once{}` 复用（数据竞争） | 2026-10-13 遗留 | 未改 | ⬜ 不在本阶段范围（登记） |
| `*_test.go` 被 gitignore，CI 不跑 Go 测试 | 2026-10-13 待决策 | 仓库策略未定 | ⬜ 不在本阶段范围 |

---

## 取证结论（本阶段的事实基础）

实测（`.scratch/oc-probe.mjs` / `oc-ablation.mjs`，2026-10-15，同出口、各 1 次）：

| 变体 | 端点 | 身份头 | 工具 schema | 结果 |
|------|------|--------|-------------|------|
| old | `/inference/v1/chat/completions` | 现状（v1 UA、`x-opencode-request`、随机 project） | `bash/glob/grep/read` 空 schema | **403** |
| stubs | `/inference/v1/chat/completions` | v2（4 镜像会话头 + `x-opencode-request-id` + `global`） | `shell/glob/grep/read` 空 schema | **403** |
| full | `/inference/v1/chat/completions` | v2 | `shell/glob/grep/read` 真实 schema + `strict:false` | **200** |

结论：端点、身份头、工具真实 schema **三者缺一不可**；纯改头或纯改端点都不通。
社区来源：`decolua/9router#4842`、`#4845` 评论（letinhminh）、`letinhminh/9router-fix`。
`GET /zen/v1/models` 与 `GET /inference/v1/models` 均 200（目录端点不迁）。

---

## File Structure（预期变更）

| 文件 | 动作 | 职责 |
|------|------|------|
| `vendors/opencode/gate.go` | 修改 | 门禁工具集改 `shell/glob/grep/read` + 真实定义 + `strict:false`；去掉 `tool_choice:none` |
| `vendors/opencode/chat.go` | 修改 | 免费档端点走 `/inference/v1`；身份头集中构造；`prompt_cache_key` |
| `vendors/opencode/responses.go` | 修改 | responses 端点与身份头同上 |
| `vendors/opencode/opencode.go` | 修改 | UA v2 形态 `opencode/latest/<ver>/cli`；版本兜底抬到 2.x |
| `vendors/opencode/*_test.go` | 修改 | 端点/头/工具/prompt_cache_key 断言更新 |
| `docs/issue-log/2026-10-15.md` | 新建 | 当日问题日志 |
| `docs/issue-log/OPEN.md` | 修改 | 索引同步 |

---

## Task 1：端点迁移（免费档 → /inference/v1）

**Files:** `chat.go`、`responses.go`

**行为:** `authPublic`（tier=free）时 chat 用 `https://opencode.ai/inference/v1/chat/completions`、responses 用 `.../inference/v1/responses`；付费 `zen`/`go` 通道端点不变。

**Steps:**

1. 新增常量 `inferenceChatURL` / `inferenceResponsesURL`，在 `buildRequest` / `buildResponsesRequest` 按 tier 选择。
2. 跑：`go build ./...` 期望 exit 0。

## Task 2：身份头对齐 v2 客户端

**Files:** `chat.go`、`responses.go`、`opencode.go`

**行为:** 统一 `applyIdentityHeaders(req, v, sessionID)`：
- `User-Agent: opencode/latest/<ver>/cli`
- `x-opencode-client: cli`、`x-opencode-project: global`
- 四镜像：`x-opencode-session`、`x-opencode-session-id`、`x-session-affinity`、`x-session-id` 同值
- `x-opencode-request-id: msg_...`；**不再发** `x-opencode-request`
- `Accept: text/event-stream`（免费档强制流式）

**Steps:**

1. 版本兜底 `versionDef` 抬到 `2.0.29`；探测值 < 2.0.0 时用兜底。
2. 跑：`go test -count=1 ./vendors/opencode/` 期望 ok。

## Task 3：body 门禁改真实工具指纹

**Files:** `gate.go`、`chat.go`、`responses.go`

**行为:**
- 门禁工具名 `["shell","glob","grep","read"]`，每个带真实 `parameters`（`properties` 非空、`required`、`additionalProperties:false`）与官方描述，`strict:false`。
- 客户端已有同名工具且 schema 非空 → 保留客户端定义；缺失或空 schema → 用真实定义覆盖。
- **删除** `tool_choice:"none"` 兜底（上游拒绝 `none`）；改为注入一条 system 说明，声明这些是占位工具、请使用调用方自己的工具。
- `prompt_cache_key` 设为当前会话 id。

**Steps:**

1. 改 `gate.go`，同步 `agent_gate_test.go` 断言。
2. 跑：`go test -count=1 ./vendors/opencode/` 期望 ok。

## Task 4：真实上游端到端复验（走本仓库代码路径）

**Files:** `.scratch/oc-e2e/main.go`（临时探针，不入库）

**Steps:**

1. 用 `contract.Message` + `Extra[_oc_raw_body]` 调 `Vendor.Chat`，模型 `big-pickle` 与 `cat-jam-free`。
2. 期望：非流式 200 且正文非空；同时用修复前代码（`git stash`）对照 403。
3. 跑：`go run ./.scratch/oc-e2e` 期望 exit 0。

## Task 5：文档与索引

**Files:** `docs/issue-log/2026-10-15.md`、`docs/issue-log/OPEN.md`

**Steps:**

1. 按 README 约定写当日日志（描述/分析/修改结果/状态），附验证证据。
2. 更新 OPEN.md：关闭 2026-10-13 的 403 条目（或标注由本阶段接管），登记本阶段遗留。

---

## 代码审查（验收前）

**审查方：** 独立子代理（非实现者）

| 审查项 | 结论 | 问题清单 |
|--------|------|----------|
| 风格 | ⬜ | |
| 测试完整性 | ⬜ | |
| 依赖与架构红线 | ⬜ | |
| 安全（密钥/越权） | ⬜ | |
| API 契约一致性 | ⬜ | |

---

## 验收标准总表

| # | 标准 | 通过条件 | 责任人 |
|---|------|----------|--------|
| 1 | 构建 | `go build ./...` exit 0 | 自动化 |
| 2 | 单测 | `go test -count=1 ./vendors/opencode/` ok | 自动化 |
| 3 | 全量回归 | `go test -count=1 ./...` 全 ok（既有 flaky 需标注） | 自动化 |
| 4 | 端到端 | 走本仓库代码路径对真实上游拿到 200 且正文非空 | 自动化（探针） |
| 5 | 代码审查 | 独立子代理结论 ✅ 或 ⚠️（问题登记） | 独立角色 |
| 6 | 红线 | 无密钥入库；未 push | 自动化 |

---

## 风险与降级

| 风险 | 缓解 |
|------|------|
| 上游再次收紧契约 | 身份头/工具定义集中在一处，便于单点更新；`client_version` 可配 |
| 注入工具被模型调用，客户端收到未知 tool_call | system 占位说明；必要时下阶段加响应侧过滤 |
| 探测触发出口风控 | 探测低频（≤6 次/轮，间隔 ≥3s），不在同一分钟重复 |
| `*_test.go` 不入库 | 登记为遗留，随仓库策略决策统一处理 |

---

## 给接手 AI 的完整提示词

---

你是负责 **opencode2api_enhance** 的实现代理。请完整执行本阶段，不要只写方案。

### 基线
- 目录：`D:\AI_Projects\opencode2api_guide\opencode2api_enhance`
- 唯一实施计划：`docs/ai-framework/plans/2026-10-15-opencode-free-tier-inference-v2.md`
- 必读：本计划「取证结论」、`docs/issue-log/2026-10-13.md`、`AGENTS.md`

### 做
1. 按 Task 1–3 改 `vendors/opencode`（端点 / 身份头 / 工具指纹 / prompt_cache_key）。
2. Task 4 用临时 Go 探针走本仓库代码路径对真实上游复验（**不要**重启用户正在跑的网关进程）。
3. Task 5 写 `docs/issue-log/2026-10-15.md` 并同步 `OPEN.md`。

### 不做
- 不改付费 zen/go 通道、不改模型目录端点、不引入真实 CLI 进程方案
- 不提交密钥；未授权不 push；不重启生产网关

### 交卷
分支名、提交列表、验收表自评、命令与退出码、残留风险。

---

## 残留手工验收清单

1. 用户在面板里对 opencode 免费模型（如 `cat-jam-free`）发一条真实对话，确认端到端可用（需先部署新构建）。
