# 阶段 B1：buddyhub 每日任务自驱动（对齐 loomy 语义，不动宿主）

> **状态：** 实施中（Task 1 已完成 / Task 4 已取消 / Task 2、3、5-7 待做）  
> **For agentic workers:** 按 Task 顺序执行；每 Task 测完再进下一 Task。  
> **交接提示词**见文末「给接手 AI 的完整提示词」。  
> **元规范:** `docs/ai-framework/phased-plan-driven.md`

**Goal:** 让 buddyhub 插件「启动即补跑 + 低频巡检 + 跨实例日锁」，不再因宿主 10:09 重启而错过 9:00 签到班次。  
**Architecture:** 保留 `*_hours` 准点语义（不删现有排程），在其下叠加「按天 claim 文件锁」层：任务派发前先抢锁，抢到才打上游；锁按 `uid+task+自然日` 粒度落在 `providerDir/data/`。activity 因无上游幂等，改由日锁保证一天一次。  
**Tech Stack:** Go 1.26、`os.OpenFile(O_CREATE|O_EXCL)` 原子建锁、既有 `scheduler` / `auth.Realm()` 体系。  
**实施档位：** 全能（默认）  
**子代理：** 启用（代码审查 + 独立验收）

---

## 前置阅读（必须）

| 优先级 | 文件 |
|--------|------|
| P0 | `docs/ai-framework/phased-plan-driven.md` |
| P0 | 本文件 |
| P0 | `D:\AI_Projects\buddyhub\项目说明.md`（5 类任务与双区口径） |
| P1 | `AGENTS.md`、`CODE_REVIEW.md` |

**仓库路径：** `D:\AI_Projects\buddyhub`（**工作区之外**，写文件需审批）  
**基线分支：** 从 `main` 拉 `feat/phase-b1-buddyhub-selfdriven`

---

## Global Constraints（冲突时以本节为准）

1. **不动宿主**：`opencode2api_enhance` 仓库零改动（用户明确要求）。宿主 `pluginprovider/manager.go` 只负责 spawn/退避重启，本阶段不接入任何调度。
2. **不改排程语义**：`*_hours` 与 `*_on` 配置项行为保持向后兼容，老 `provider.json` 直接可用。
3. **上游频率纪律**：余额/任务查询是带用户凭据的第三方调用。`RefreshCredits` 并发上限 3 的口径不得放宽；新增巡检周期 **不得低于 15 分钟**；activity 每号每天仍为 5 条。
4. **D4 门控不得松动**：global 账号继续跳过 checkin/streak/school/travel/blackcat（`scheduler.go:396`、`streak.go:29`、`school.go:36`、`travel.go:82`、`blackcat.go:28`）。
5. **密钥/凭证不进 git**：`data/auths/workbuddy-*.json`、`state.json` 一律不入库、不粘贴进日志。
6. **Git：** ⚠️ **实测 `D:\AI_Projects\buddyhub` 不是 git 仓库**（`git rev-parse` → fatal，父级亦无 `.git`），本阶段各 Task 的 commit 步骤无法执行、无回滚点。应对：每 Task 完成即跑全量 `go test ./...` 作回归闸门；是否先 `git init` 由用户决定。默认**不 push**（无远程）。
7. **明确不做（本阶段）**
   - ❌ 不改 `pool`/`redisstore` 的跨进程一致性（5 进程共享 `data/state.json` 是既有风险，另立阶段）。
   - ❌ 不做插件自动更新/分发脚本。
   - ❌ 不新增独立 HTTP 面板页。
8. **YAGNI**：只补「锁 + 首发 + 巡检 + 积分端点」，不重写调度器。

---

## 阶段开头：上阶段遗留（必填小节）

| 上阶段遗留项 | 来源 | 未通过原因 | 状态 |
|-------------|------|-----------|------|
| 无（buddyhub 未启用 issue-log 框架；宿主 OPEN.md 中 12 项与本阶段无关） | — | — | ✅ 无 |

---

## 跳过项（因档位未做，**非缺陷**，待补做）

| 跳过项 | 原因 | 待补做 |
|--------|------|--------|
| 端到端：真机跑满一个自然日验证 5 实例只打卡 1 次 | 需部署机 + 等到点，会话内不可得 | ⬜ 待用户确认补做 |

---

## 与前后阶段

| 阶段 | 状态 | 交付 |
|------|------|------|
| 上阶段 | ✅ | 根因取证（宿主 issue-log 2026-10-08 第 3 条） |
| **本阶段** | ⬜ | 自驱动签到/做任务/积分刷新 |
| 下阶段 | | 5 进程共享 state 的并发写治理（勿塞进本阶段） |

---

## File Structure（预期变更）

| 文件 | 动作 | 职责 |
|------|------|------|
| `internal/scheduler/dayclaim.go` | 新建 | 跨实例按天 claim 锁（O_EXCL 原子建文件 + 当日判定 + 失败释放） |
| `internal/scheduler/dayclaim_test.go` | 新建 | 锁的单测：首发抢到/重复跳过/失败可重试/跨日重置 |
| `internal/scheduler/scheduler.go` | 修改 | `Config` 增 `DataDir`/`CatchUpInterval`；`Run` 启动即 `runDueOnce`；派发前过 claim |
| `internal/scheduler/scheduler.go` | 修改 | `runActivity` 每号过日锁（无上游幂等，靠锁保证一天一次） |
| `internal/scheduler/travel.go` | 修改 | `adoptTried` 内存态 → 复用 dayclaim 落盘（重启不丢当日防抖） |
| `internal/pluginbuddy`（`cmd/pluginbuddy/main.go`） | 修改 | 把 `dataDir` 注入 `scheduler.Config` |
| `internal/pluginhub/handler.go` | 修改 | 新增 `POST /api/credits/refresh`；`Engine` 补 `Upstream` 字段 |
| `internal/pluginhub/panel.html` | 修改 | 「刷新积分」按钮 + 30s 轮询（对齐 loomy `panel.go:326`） |
| `internal/scheduler/*_test.go` | 修改 | 覆盖新路径 |

---

## 配置或 API 契约（如有）

```jsonc
// provider.json 新增（全部可选，缺省即向后兼容）
{
  "provider_private_configs": {
    "catchup_on": true,          // 启动补跑 + 低频巡检总开关（默认 true）
    "catchup_interval_min": 20   // 巡检周期，下限 15 分钟
  }
}
```

```
POST /api/credits/refresh   →  {"ok":N,"fail":M}     // 复用 Engine.RefreshCredits
```
---

## Task 1：跨实例按天 claim 锁（地基，必须先做）

**Files:** `internal/scheduler/dayclaim.go`（新建）、`internal/scheduler/dayclaim_test.go`（新建）

**行为:**
- `dayClaim{dir, mu}`，锁文件命名 `<dir>/claims/<task>-<uid>-<YYYY-MM-DD>.lock`，内容记首次抢到时间。
- `Try(task, uid) (release func(), got bool)`：`os.OpenFile(..., O_CREATE|O_EXCL|O_WRONLY)` 原子抢；已存在 → `got=false`（今日已有人做过）。
- 任务**失败**时调 `release()` 删锁文件，允许下一轮巡检重试；**成功**保留锁。
- 日期键用 `travelDay()` 同款自然日口径（CST），跨日自动重置（新文件名）。
- 目录不存在自动 `MkdirAll`。

**Steps:**
1. 写实现 + 单测（首发抢到、二次跳过、release 后可再抢、跨日不冲突）。
2. 跑：`go test ./internal/scheduler/ -run TestDayClaim -race`  
   期望：`PASS`，无 race。
3. ~~Commit~~（非 git 仓库，见 Global Constraints 6）→ 改跑 `go test ./...` 全量回归。**状态：已完成**（10 个 DayClaim 用例全绿）

---

## Task 2：启动即补跑 + 低频巡检

**Files:** `internal/scheduler/scheduler.go`、`cmd/pluginbuddy/main.go`

**行为:**
- `Config` 增 `DataDir string`、`CatchUpOn bool`、`CatchUpInterval time.Duration`（`New` 内归一化：interval<15min → 20min）。
- `Run(ctx)` 开头：`CatchUpOn` 为真则先 `s.runDueOnce(ctx)` —— 遍历 5 类任务，**每类先过 Task 1 日锁**，抢到才 `dispatch`。这一步让 10:09 起来的进程当场补上 9:00 的签到，同时被锁挡掉另外 4 个实例的重复打卡。
- 主循环 `select` 增一路 `catchup` ticker：到点做与 `runDueOnce` 相同的「未做则补」判定；**准点班次逻辑不动**（`nextWake`/`nextFire` 保留）。
- `cmd/pluginbuddy/main.go`：构造 `scheduler.Config` 时传 `DataDir: dataDir`。

**Steps:**
1. 实现后跑：`go build ./... && go test ./internal/scheduler/ -race`  
   期望：exit 0，全绿。
2. 关键用例（新增测试）：`Run` 启动路径调用一次补跑；同 uid 二次启动不重复 dispatch（断言 dispatch 计数=1）。
3. 手动冒烟：临时把 `checkin_hours` 配成已过点的时点 + `CatchUpOn=true`，起进程，日志应出现一次 `checkin done:` 且第二次起进程不再出现。
4. Commit：`✨feat(scheduler): 启动补跑与低频巡检`

---

## Task 3：activity 按天幂等（唯一无上游判重的任务）

**Files:** `internal/scheduler/scheduler.go`（`runActivity`）

**行为:**
- `runActivity` 每号循环内先 `Try("activity", uid)`；未抢到 → `continue`（当日已上报，不再发 5 条）。
- 5 条上报有失败（`ok < count`）→ `release()`，下轮巡检可重试；全成功则保留锁。
- `checkActivityStreak`/`travelAdoptForce` 仅在抢到且发满后执行（现有位置不变）。
- **注意**：不得把 activity 纳入「每次巡检都跑」——它靠日锁保证一天一次，频率上限仍是每天 5 条/号。

**Steps:**
1. 跑：`go test ./internal/scheduler/ -run Activity -race`  
   期望：重复调用 `RunActivityNow` 两次，`ReportChatActivity` 计数为 5（不是 10）。
2. Commit：`✨feat(scheduler): 活跃上报按天幂等`

---

## Task 4：~~travel 当日防抖落盘~~（**已取消**）

> **取消原因（2026-10-08 实施时核实）**：`scheduler.go:64-65` 注释明确写着「进程重启即清零（**无需持久化**）」——这是原作者的刻意决策，不是缺陷。改落盘等于推翻既有设计，收益仅是省掉「重启当天可能多试一次领养」，代价是引入新的持久化失效模式。计划阶段误判为缺陷，此处更正，**不执行**。


---

## Task 5：插件模式积分刷新端点

**Files:** `internal/pluginhub/config.go`(Engine struct)、`handler.go`、`cmd/pluginbuddy/main.go`

**行为:**
- `Engine` 补 `Upstream *upstream.Client`（当前仅 `Pool/Scheduler/AuthDir/DataDir`，拿不到 Upstream 是插件模式无法刷积分的根因）。
- 新增 `POST /api/credits/refresh` → 调 `Engine.RefreshCredits` 等价逻辑（并发上限 3，`engine.go:427` 口径），返回 `{"ok":N,"fail":M}`；沿用 `auth(…)` Bearer 鉴权。
- 启动时（`startEngine` 里 `go sched.Run` 之后）异步调一次，覆盖「global 号不签到 → 积分永远 0」。
- 刷新走 `Pool.SetCreditsDetailed` + `ReenableIfCredits`，与签到同口径，避免两套语义。

**Steps:**
1. 跑：`go test ./internal/pluginhub/ -race`（无测试则补一个：假 Upstream 断言 ok/fail 计数与 credits 回填）。
2. 冒烟：`go build -o providers\buddyhub\buddyhub-provider.exe .\cmd\pluginbuddy\`，起进程，`curl -X POST -H "Authorization: Bearer <token>" http://127.0.0.1:<port>/api/credits/refresh` 返回 JSON 且 `/api/accounts` 的 `credits` 非 0。
3. Commit：`✨feat(pluginhub): 插件模式积分刷新端点`

---

## Task 6：面板按钮 + 周期刷新

**Files:** `internal/pluginhub/panel.html`

**行为:**
- 账号表头加「刷新积分」按钮 → `POST /api/credits/refresh` → 成功后 `loadAccounts()` + `loadDash()`，并 toast 显示 `成功 N / 失败 M`。
- `setInterval(loadAccounts, 30000)` 对齐 loomy 30s 口径。
- 任务区补显示「今日已补跑：签到 ✓/—」状态（读 claim 锁目录，只读展示）。

**Steps:**
1. `go build ./...` 期望 exit 0。
2. Commit：`✨feat(panel): 刷新积分按钮与周期刷新`

---

## Task 7：文档与回归

**Files:** `项目说明.md`、`providers/buddyhub/provider.json`（补 catchup 键与 _hint）、仓库根 README（若有）

**行为:**
- `项目说明.md` 的「每日任务」表补一行口径：排程为准点触发，另叠加启动补跑与巡检；跨实例由日锁去重。
- `provider.json` 补 `catchup_on`/`catchup_interval_min`。
- 全量回归：`go build ./... && go test ./... -race`。

**Steps:**
1. 跑全量，记录真实输出（禁止「应该能过」）。
2. 替换部署二进制 `D:\Program Files\opencode2api\bin\providers\buddyhub\buddyhub-provider.exe`（**工作区外，需用户审批**）。
3. Commit：`📝docs: 自驱动任务口径与配置项`
---

## 代码审查（阶段级环节，验收前；档位=全能，**执行**）

**审查方：** 独立角色（非本阶段实现者）

**审查面：** 代码风格 / 测试完整性 / 依赖合理性 / 架构红线（不动宿主、不松 D4 门控）/ 安全（凭据不落日志、锁文件不含 token）/ API 契约（新端点鉴权与 loomy 一致）

| 审查项 | 结论（✅/⚠️/❌） | 问题清单 |
|--------|------------------|----------|
| 风格（对齐 coding-standards.md） | | |
| 测试完整性（-race 覆盖并发抢锁） | | |
| 依赖与架构红线（宿主零改动） | | |
| 安全（凭据/锁文件/新端点鉴权） | | |
| API 契约（provider.json 向后兼容） | | |

**结论：** ✅ 通过 / ⚠️ 有条件通过（问题进验收表）/ ❌ 不通过（阻塞）

---

## 验收标准总表

| # | 标准 | 通过条件 | 验证责任人 |
|---|------|----------|-----------|
| 1 | 构建 | `go build ./...` exit 0 | 自动化（执行方） |
| 2 | 单测全绿 | `go test ./... -race` exit 0，含 dayclaim/activity 幂等/travel 落盘新用例 | 自动化（执行方） |
| 3 | **核心：不再错过班次** | 在 >09:00 的时刻启动进程，日志出现一次 `checkin done:`，无需等到 21:00 | 自动化 + 人工 |
| 4 | **核心：跨实例去重** | 5 实例同机启动，上游签到请求计数 = 1（非 5）；日志中 `checkin done:` 仅 1 次、其余为 claim 跳过 | 端到端（需部署机） |
| 5 | activity 不放大 | 单号 24h 内 `ReportChatActivity` 调用 ≤5 次 | 自动化（执行方） |
| 6 | 巡检频率下限 | `catchup_interval_min < 15` 被归一化为 20，不更密 | 自动化（执行方） |
| 7 | D4 门控未松动 | global 号（`fe26e8d7…`）不出现 checkin/streak/school/travel/blackcat 的上游调用 | 自动化 + 人工 |
| 8 | 积分可见 | 插件面板账号 `credits` 非 0；`POST /api/credits/refresh` 返回 `{ok,fail}` | 人工（GUI） |
| 9 | 向后兼容 | 旧 `provider.json`（无 catchup 键）行为正确、缺省开启补跑 | 自动化（执行方） |
| 10 | 红线：宿主零改动 | 宿主仓库 `git status` 无 buddyhub 相关改动 | 自动化（执行方） |
| 11 | 密钥 | `git ls-files` 不含 `data/auths/*`、`state.json`；日志无 token | 自动化（执行方） |
| 12 | 代码审查 | 结论 ✅ 或 ⚠️（问题已登记）；❌ 下放下阶段 | 独立角色 |

---

## 风险与降级

| 风险 | 缓解 |
|------|------|
| **重启风暴放大打卡**：宿主有崩溃退避重启（历史 loomy 78 分钟 2670 次），若「启动首发」先于日锁落地=打卡风暴 | Task 1 必须先于 Task 2 合并；Task 2 的补跑入口只能经 `dayclaim.Try` 派发 |
| 巡检打密上游触发风控 | 周期下限 15min（归一化 20）；`RefreshCredits` 并发上限 3 不动；activity 由日锁保证每天一次 |
| 5 进程共享 `data/claims` 的文件竞争 | `O_CREATE\|O_EXCL` 原子；单测 `-race` 覆盖并发抢锁 |
| 锁文件残留导致「今天再也不跑」 | 任务失败即 `release()`；文件名含自然日，跨日自动失效；面板显示当日锁状态便于排障 |
| `Engine` 加 `Upstream` 字段影响既有装配 | 只在 `startEngine` 注入，桌面版 `engine.go` 路径不动；`go build ./...` 兜住 |
| global 号积分仍为 0 被误读成 bug | Task 5 启动刷新覆盖；文档明确「global 不参与 CN 任务体系」 |
| 需重编并替换工作区外部署二进制 | 执行前向用户申请审批；失败则回滚 `.old`（目录已存在 `buddyhub-provider.exe.20260925-104926.old` 惯例） |

**降级方案：** 若上游对高频查询明显收紧，可把 `catchup_on` 设为 `false` 回到纯准点排程（保留 Task 1 锁与 Task 5 端点，二者独立可留）。

---

## 给接手 AI 的完整提示词

将下面整段粘贴给执行 AI 即可开工：

---

你是负责 **buddyhub 自驱动每日任务** 的实现代理。请**完整执行本阶段**，不要只写方案。

### 基线
- 目录：`D:\AI_Projects\buddyhub`（在本工作区之外，写文件需用户审批）
- 从 `main` 创建并切换：`feat/phase-b1-buddyhub-selfdriven`
- 已完成：根因取证（宿主 `D:\AI_Projects\opencode2api_guide\opencode2api_enhance\docs\issue-log\2026-10-08.md` 第 3 条）——**不要重做诊断**
- 唯一实施计划：`docs/ai-framework/plans/2026-10-08-buddyhub-selfdriven-daily-tasks.md`
- 必读：`docs/ai-framework/phased-plan-driven.md`、`AGENTS.md`、`D:\AI_Projects\buddyhub\项目说明.md`

### 做
1. 先跑 `go build ./...` 与 `go test ./... -race` 确认基线干净（记录真实输出）。
2. **严格按 Task 1→7 顺序**；每 Task 测试通过后 commit。Task 1（日锁）未完成前不得实现 Task 2（启动首发）。
3. 每个验收项都要有真实命令输出，禁止「应该能过」。

### 不做
- **不改宿主仓库 `opencode2api_enhance` 的任何代码**（用户明确要求）。
- 不放宽 D4 门控（global 号不得参与 CN 任务）。
- 不把巡检周期调到 15 分钟以下；不放宽 `RefreshCredits` 并发上限 3。
- 不动 `pool`/`redisstore` 的跨进程一致性（下阶段）。
- 不提交 `data/auths/*`、`state.json`、任何 token；不做未授权 `git push`。

### 工作方式
1. 按全能档位执行；子代理用于代码审查与独立验收。  
2. 证据优先：完成前重跑计划中的验证命令。  
3. 用简体中文回复进度；代码标识符保持原样。  
4. 同步维护宿主按天日志 `docs/issue-log/2026-10-08.md` 第 4 条的状态（待处理→修复中→已修复待验证→已关闭），并同步 `OPEN.md`。

### 交卷
分支名、提交列表、验收表自评（12 项逐条附证据）、测试/构建结果、残留风险。

现在开始：读完本阶段计划，从 Task 1 执行到最后。

---

## 残留手工验收清单

（自动化之外的 GUI / 真机项）

1. 面板账号列表可见「国内版/国际版」标签，CN 号 `credits` 非 0，点「刷新积分」有 toast 且数值更新。
2. 部署机观察一个完整自然日：5 个 `buddyhub-provider` 进程仅产生 1 次 `checkin done:` 日志。
3. 手动停掉宿主再起，确认当天已签到时**不会**重复打卡（锁生效）。
4. global 号（`fe26e8d7…`，domain `www.codebuddy.ai`）在全天日志中不出现 CN 任务调用。
