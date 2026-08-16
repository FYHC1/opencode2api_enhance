# 开发计划：实例池性能模式 + 退出交互改造 + 多端支持（feat/pool-performance）

> **本计划是本次改造的实施依据（阶段 / 验收 / 遗留声明）**，遵循仓库 AGENTS.md 纪律：
> 每阶段 = 功能开发 + 测试 + 验证；**验证不通过的部分下放到下一阶段开头声明并优先处理**。
> 开工前必读 `docs/AI-TESTING-GUIDE.md`（端口与环境隔离红线）与 `docs/ARCHITECTURE-V2-PLAN.md`（架构约束）。
>
> - 分支：`feat/pool-performance`（自 `main` 切出，2026-08-12）
> - 基线：`main@7e5dc2d`（本地领先 origin 2 提交，未推送）
> - 优先级：**P0 探针泄漏 ✅ → P1/P2 性能模式 → D1 退出改造 → M 系列多端**
> - 交接对象：后续由其他同事实施，本计划需精确到文件/函数/契约/测试用例

---

## 〇、背景与目标

### 0.1 方向一：实例池性能模式（P 系列，优先）

**痛点（用户原话整理）**：节点质量参差，部分节点"断断续续"——比如 10s 没响应，过 10 秒又恢复。说不能用吧还能用，说能用吧又时常不能用。**无法手动持续检查每个节点**。

**方案（2026-08-12 用户确认）**：**请求级竞速（hedged requests）+ 链路级质量评分闭环**：

```
┌─ 质量评分层（决策）─────────────────────┐
│ 高频探活 + 滑动窗口 → 每节点质量分/等级     │
│ healthy/degraded → 进入竞速候选池          │
│ flaky → 降权（只在候选不够时补位）          │
│ down → 熔断，不参与竞速                    │
└─────────────────────────────────────────┘
┌─ 竞速执行层（动作）──────────────────────┐
│ 请求到达 → 只向候选池（2~3 个健康节点）扇出 │
│ 谁先响应谁赢，其余断开                      │
└─────────────────────────────────────────┘
        ↑ 竞速结果回写评分窗口（闭环）↓
```

**现状缺陷（已核实代码）**：
| 现有机制 | 位置 | 缺陷 |
|---|---|---|
| 健康巡检 | `core/manager/health.go` | 仅 **TCP 端口探测**——端口通=进程活，**测不出链路抖动**（用户场景端口恒通，巡检恒健康） |
| 代理池冷却/坏池 | `socks.go` | **被动**——请求失败才记冷却（20s/45s/2min），坏状态码 3 次进坏池；**无主动探测** |
| 冷却恢复 | `socks.go` | 冷却到期靠**下一次请求碰运气**，无主动回归验证（半开） |
| 路由 | `socks.go` smart/failover/round_robin | 游标+冷却跳过，**无按质量加权**——抖动节点不在冷却期照样被选中 |
| 流内超时续写 | `gateway_timeout.go` | 已吐内容拼上下文重连（最多 3 次）——**保留资产**，竞速全败后兜底 |

### 0.2 方向二：桌面退出交互改造（D 系列）

**痛点**：托盘右键「退出」在 20 实例运行时卡顿数秒（串行 taskkill 40 次），无进度反馈。
**需求**：退出弹二次确认框，两种方式——① 退出不释放（实例保活，下次直接可用）；② 退出并释放（弹窗进度条 0/20 逐步完成再关闭）。

### 0.3 方向三：多端支持（M 系列，后做）

**现状**：Web 版已通（core 托管前端，浏览器全功能）；Linux headless 可跑；**Linux 桌面 / macOS 未实现**（V2 计划 P5 未动工）。

---

## 一、分支与提交纪律

- 本分支内**一阶段一提交**，每阶段完成 `go test -count=1 ./...` 全绿才提交。
- 两个方向独立分支（`feat/pool-performance` / `feat/multi-platform`），完成后逐个合并回 main。
- 提交信息遵循仓库风格（`feat(ui):` / `fix:` / `build:` / `docs:` 前缀）。
- Windows 文件 CRLF：新增文件保持仓库既有风格；git 会自动处理换行警告，可忽略。

---

## 二、阶段计划

> 每阶段格式：**目标 / 功能开发（文件级）/ 测试 / 验证** + 验收标准。
> **遗留声明规则**：上一阶段验证不通过项 → 本阶段「阶段开头：上阶段遗留」小节声明，优先处理。

---

### 阶段 P0：探针进程泄漏修复（✅ 已完成，2026-08-12）

**背景**：用户发现只开 2 个实例，但任务管理器出现 **96 个 opencode2api/sing-box 进程**。
经 psutil 全量进程审计 + 代码审查确认**非内存泄漏，是探针进程泄漏**：

- `core/manager/probe_node.go` 的 `probeNode()`：**仅失败路径 Kill 探针进程**，
  **成功路径（含 HTTP 错误/超时/分类返回）直接 `return base`，不清理** `sbPID`/`ocPID`。
- 每次扫描每探测 1 个节点残留 1 对进程（sing-box + opencode2api），8 worker × 多轮 → 数十个进程堆积。
- Windows 侧无 Job Object 兜底（`process_windows.go` 仅 taskkill 单进程）。

**修复（commit `b2eee5c`）**：`probeNode()` spawn 成功后加 `defer` 清理——所有返回路径（成功/失败/超时/分类）退出前必杀 `ocPID` + `sbPID`；幂等 Kill 无害。

**验收**：✅ `go test -count=1 ./...` 全绿；`TestProbeNodeOK` 断言 killed 含两个探针 pid。
**遗留**：用户机器存量 90+ 泄漏进程待一次性清理（识别 `runtime/_probe/worker-*` 归属进程，**须用户确认后执行**，AGENTS.md 红线）。

---

### 阶段 P1：链路级主动探活 + 滑动窗口质量评分（core 侧）

**目标**：建立"能发现链路抖动"的主动探活子系统与质量评分模型，为 P2 竞速提供候选池。

#### 功能开发（文件级）

**新文件 `core/manager/poolquality.go`**：

1. **数据结构**：
```go
// NodeQuality 单节点质量记录（内存 + 持久化 runtime/pool_quality.json）。
type NodeQuality struct {
    Name              string   `json:"name"`               // 实例名
    Score             int      `json:"score"`              // 质量分 0~100
    Level             string   `json:"level"`              // healthy/degraded/flaky/down
    SuccessRate       float64  `json:"success_rate"`       // 窗口内成功率 0~1
    AvgLatencyMS      int64    `json:"avg_latency_ms"`     // 窗口内平均延迟
    ConsecutiveFails  int      `json:"consecutive_fails"`  // 连续失败计数
    LastProbeTS       int64    `json:"last_probe_ts"`      // 最后探活时间戳
    LastProbeOK       bool     `json:"last_probe_ok"`      // 最后探活结果
    Breaker           string   `json:"breaker"`            // P2 用: closed/open/half_open
    HalfOpenUntilTS   int64    `json:"half_open_until_ts"` // P2 用: 半开探测截止
}
// QualitySummary 探活汇总（前端展示）。
type QualitySummary struct {
    Total     int            `json:"total"`
    Candidates int           `json:"candidates"` // healthy+degraded 数
    Records   []NodeQuality  `json:"records"`
    LastProbeTS int64        `json:"last_probe_ts"`
}
```

2. **滑动窗口评分**（内存环形缓冲，窗口默认 10min 可配）：
   - 每次探活/真实请求结果入窗口：`{ts, ok, latency_ms}`
   - 评分公式（建议，可调）：
     - 成功率权重 60 分：`successRate * 60`
     - 延迟权重 40 分：`≤1s→40；1~3s→25；3~6s→10；>6s→0`（线性插值）
     - 连续失败惩罚：`≥3` 次连续失败每多一次 -5（下限 0）
   - 等级阈值：`≥80 healthy / 60~79 degraded / 30~59 flaky / <30 down`

3. **链路级探活**：
   - 周期（默认 45s，可配 `pool_probe_interval_sec`）对 **Running 且 join_gateway** 实例发**真实 HTTP 探测**：
     经实例 sing-box SOCKS 通道发轻量请求（复用 `socks5Dial`，目标 `GET /v1/models` 或最小 chat）；
     配 3s 超时（可配 `pool_probe_timeout_sec`），记录延迟与成败。
   - 并发度默认 4（可配 `pool_probe_concurrency`），探测风暴防护。
   - 探活 goroutine 与 `StartHealthLoop` 并行互不干扰；TCP 端口探测保留（实例存活判据）。

4. **配置项**（`core/manager/config.go` + `config.example.json`）：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `pool_probe_enabled` | bool | true | 探活总开关 |
| `pool_probe_interval_sec` | int | 45 | 探活周期 |
| `pool_probe_timeout_sec` | int | 3 | 单次探活超时 |
| `pool_probe_concurrency` | int | 4 | 并发探活数 |
| `pool_quality_window_min` | int | 10 | 滑动窗口时长 |

5. **持久化**：`runtime/pool_quality.json`（MarshalIndent，损坏容错归零）。
6. **管理 API**（挂在 `/api/admin/*`，鉴权复用 requireAuth）：
   - `GET /api/admin/pool/quality` → `QualitySummary`
   - `POST /api/admin/pool/quality/probe` → 手动触发一轮立即探活

#### 测试（`core/manager/poolquality_test.go`，全部 mock/httptest 不触网）
- 滑动窗口评分：窗口滑出、成功率 0%/100%、延迟分档、连续失败惩罚、等级边界（79/80、59/60、29/30）
- 探活调度：fake Runner + fake HTTP 后端，成功/超时/连接拒绝三路径；并发度上限
- 持久化 roundtrip + 损坏文件容错
- 配置解析：默认值、非法值回退、`pool_probe_enabled=false` 时探活循环空转
- 手动触发 API：返回最新 Summary

#### 验证
- `go test -count=1 ./...` 全绿。
- 手动（隔离环境）：起 2~3 实例入池，模拟节点抖动（临时断 sing-box），确认质量分下降、恢复后回涨。
- **验收**：探活间隔/窗口/超时可配；质量分反映"10s 断断续续"场景；无探活风暴。

---

### 阶段 P2：竞速模式（hedged requests）+ 熔断/半开自动恢复（core 侧）

**目标**：路由层消费质量分，请求级竞速——一次请求同时扇出到候选池，先响应者胜，其余断开；坏节点自动熔断、恢复自动回归。

#### 阶段开头：P1 遗留声明
- （P1 验证不通过项在此逐条列出并优先修复，修复后回归验证。）

#### 功能开发（文件级）

**`core/manager/poolquality.go` 扩展**：
1. **熔断状态机**（每节点）：
   - `closed`：正常参与竞速；连续失败 ≥ `pool_breaker_threshold`（默认 3）→ `open`
   - `open`：不参与竞速；到期（`pool_halfopen_interval_sec` 默认 60s）→ `half_open`
   - `half_open`：放行 **1 个探活请求**；成功 → `closed`（回归）；失败 → `open`
2. **半开探测**：复用 P1 探活通道，只对 `half_open` 节点，串行低并发（1），成功即回归。

**`socks.go` 扩展（竞速核心）**：
3. **候选池选择**：`raceCandidates(need int) []Socks5Proxy`——从 `socks5Proxies` 按质量分排序
   取 `healthy+degraded`（各等级内按分降序），`flaky` 仅在 healthy+degraded 不足时补位，
   `down`/`open` 熔断节点剔除；全池不可用回退原 `pickHealthyProxy` 兜底。
4. **竞速执行**：新函数 `raceRequest(ctx, req, candidates, streaming) (resp, proxyAddr, err)`：
   - **非流式**：复制请求体 → 并行 N 个 goroutine（N=`pool_race_copies` 默认 2，可配 1~4）
     各自经不同候选节点发 `client.Do` → 第一个完整 2xx 胜出 → 其余 `cancel()` 断开。
   - **流式（SSE）**：并行建流 → **谁先吐出第一个 chunk 谁赢**（立即转独占转发）→ 其余 cancel。
     赢家流接入现有 `streamWithResume`（gateway_timeout.go）续写通道保留。
   - 全败 → 返回聚合错误，走原 failover 逻辑/`gateway_timeout.go` 续写兜底。
5. **结果回写**：竞速赢家 `markSocks5Result(addr, 200, nil)` + 评分窗口记成功；
   失败者记失败/冷却；**竞速即探活**（一次请求摸清多节点）。
6. **性能模式开关**：`pool_performance_mode`（默认 true）——关闭时行为与现状完全一致
   （游标+冷却，纯串行），已有测试快照兜底回归。

**配置项新增**：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `pool_performance_mode` | bool | true | 性能模式总开关 |
| `pool_race_copies` | int | 2 | 竞速副本数（1~4） |
| `pool_breaker_threshold` | int | 3 | 连续失败熔断阈值 |
| `pool_halfopen_interval_sec` | int | 60 | 半开探测间隔 |

#### 测试
- 单测（`core/manager/` + `socks_test.go` 扩展）：
  - 候选池排序/剔除/补位/全冷兜底
  - 熔断状态机：closed→open→half_open→closed 全转换、阈值边界（2/3 次）、失败维持 open
  - 半开：成功回归 / 失败维持 open
  - 竞速：两个 fak e后端一个快一个慢 → 快者胜、慢者 cancel 被调用；全败报错
  - 流式竞速：首 chunk 锁定后其余断开
  - 性能模式关闭：路由行为与基线一致（回归快照）
  - 回写：赢/输对评分窗口的影响
- 集成测试（fake）：模拟"断断续续"节点（交替成功/超时），断言请求自动切换、恢复后自动回归、调用日志含切换事件。

#### 验证
- `go test -count=1 ./...` 全绿。
- 手动（隔离环境）：真实坏节点（挂起代理）请求自动切走不报错；恢复后自动回归；用户无感。
- **验收**：竞速副本/熔断阈值/半开间隔可配；切换事件在调用日志可见；与续写机制协同。

---

### 阶段 P3：UI 可视化 + 全链路收尾（前端）

**目标**：六页 UI 暴露质量分与熔断状态，设置页可调参数，P 系列交付。

#### 阶段开头：P2 遗留声明
- （P2 验证不通过项在此逐条列出并优先修复。）

#### UI 草图（Markdown 文本结构，2026-08-12 用户确认方案）

**实例池页（性能模式启用时）布局：**

```
┌─ 统一网关 ────────────────────────────────────────────────┐
│ 运行中 · 8 池成员 · 免费模型 12 个    [⚡ 性能模式·竞速×2]   │
│                                    [● 探活 30s 余 12s]    │
│                                    [重启] [停止]           │
├─ 路由模式 ────────────────────────────────────────────────┤
│ smart │ failover │ round_robin │ [⚡ 性能模式(选中,teal)]   │
├─ 池成员 ──────────────────────────────────────────────────┤
│ ●  名称/节点IP   端口   健康状态   质量分   延迟(10min)   最近探活   操作 │
│ ●  HK 香港 A1   40101  健康(绿)   [92 绿]  ▓▓▓▓ 1.2s    ✅ 8s前   停止测试释放│
│ ●  美国 W1      40102  健康(绿)   [88 绿]  ▓▓▓░ 2.8s    ✅ 21s前  停止测试释放│
│ ◐  SG 新加坡 B2 40103  降级(黄)   [64 黄]  ▓▓░░ 4.1s    ⚠ 2次失败 停止测试释放│
│ ◐  JP 东京 C3   40104  抖动(橙)   [38 橙]  ▓░░░ 6.7s    ⚠ 超时×3  停止测试释放│
│ ✕  DE 德国 D4   40105  熔断(红)   [12 红]  ░░░░ —       ⏸ 半开55s 启动测试释放│
│ 图例: ●健康≥80竞速优先 ●降级60-79降权 ◐抖动30-59不竞速 ✕熔断<30剔除 │
│       竞速成功记+1 · 失败记冷却 · 恢复自动回归                │
├─ 说明条 ──────────────────────────────────────────────────┤
│ ⚡ 性能模式:请求同时发往 2 个健康节点,谁先响应用谁,其余断开;  │
│    响应速度 = 最快节点;单节点断断续续不影响你;结果自动回写评分 │
└───────────────────────────────────────────────────────────┘
```

**要点**：保持现有六页 UI 风格（Windsurf 浅色、zinc 边框、teal 强调、rounded-2xl 卡片）。
质量分配色：绿≥80 / 黄60-79 / 橙30-59 / 红<30。行首圆点=竞速候选资格。
完整渲染参考：`docs/images/pool_perf_sketch.png`（仅参考，非必需交付物）。

#### 功能开发
1. **实例池页**（`src/pages/PoolPage.tsx`）：表格加三列——质量分徽章、延迟条+平均延迟（**毫秒级 `1234ms` 格式，不换算秒**）、
   最近探活状态（✅/⚠/⏸）；行首竞速候选圆点；路由模式行加「⚡ 性能模式」按钮
   （teal 高亮，与 P2 开关联动）。
2. **节点池页**（`src/pages/NodesPage.tsx`）：节点行扩展质量分/延迟/抖动状态。
3. **设置页**（`src/pages/SettingsPage.tsx`）：探活间隔/窗口/熔断阈值/半开间隔/竞速副本/
   性能模式开关（走现有 config 热更新 API）。
4. **API 对接**（`src/lib/api.ts`）：`GET /api/admin/pool/quality` 类型 + 字段对齐。
5. 轻量化原则：纯 CSS 徽标/状态点，不引图表库。

#### 测试
- `npm run build` 通过；`src/lib/api.ts` TS 类型与 Go JSON 字段对齐。
- 组件逻辑（质量分级映射、状态徽章渲染）随现有测试口径。

#### 验证
- 浏览器走查（六页全链路）；与 Windows exe 行为一致（壳与 Web 等价）。
- **验收**：质量分/熔断状态可见、参数可调且热生效、六页 UI 一致。

---

### 阶段 D1：桌面退出交互改造（托盘「退出」二次确认 + 两种退出席位）

**背景（用户反馈，已核实代码）**：托盘「退出」串行 taskkill 40 次（20 实例 × 2 进程）无反馈。
代码位置：`src-tauri/src/lib.rs` quit 菜单 → `commands.rs stop_all_instances` → `stop_instance`。

#### 功能开发（方式二优先）

**1. 退出确认弹窗（前端 `src/App.tsx` / 全局悬浮层）**：
- 托盘「退出」→ 拦截默认退出 → 弹窗：
  - 「**退出并释放**」（主按钮，红/teal 强调）：走方式二流程
  - 「**退出但不释放**」（次按钮，灰）：方式一（见下，能力具备后开放）
  - 「取消」：关闭弹窗，应用继续
- 注意：退出拦截在 Tauri 侧 `on_window_event`/`RunEvent::ExitRequested` 用 `api.prevent_exit()`，
  前端确认后再 `invoke('quit_app')` 真退出。

**2. 并行释放（Rust `src-tauri/src/commands.rs`）**：
- `stop_all_instances` 改 **batch 并发 4**（复用 `instance.rs` 现有 stop 逻辑，goroutine 无、Rust 用
  `std::thread` 或 `tokio`——检查现有依赖，若无 tokio 用线程池/分块串行 4 个一批）。
- 20 实例释放耗时：40 次串行 → ~5 批。

**3. 进度上报**：
- 方案：前端在释放期间轮询 `GET /api/admin/instances` 剩余 Running 数，或 Rust `app.emit("exit-progress", {done,total,current})` 事件。
- 弹窗内进度条 `0/20 → N/20`（显示当前释放实例名），完成后 `app.exit(0)`。

**4. 方式一「退出不释放」技术预案（需用户拍板后实施，默认排 M 系列）**：
- 障碍：`src-tauri/src/job.rs` Windows Job Object——壳退出（含强杀）自动杀 core 及其全部子进程。
- 方案 A（保活）：退出时把 core 进程从 Job 摘除（`NtRemoveProcessFromJob` syscall），壳退出后
  core+实例继续跑；下次启动 **attach 已运行 core**（端口探测 + pid 文件）而非重新 spawn；
  `ReconcileStates` 已支持活 pid 保持 Running。风险：core 长驻内存、数据目录/端口占用、attach 一致性。
- 方案 B（近托盘）：退出改为「隐藏窗口+托盘常驻」，实例天然保活（=现状最小化行为），零架构风险，
  语义与"退出但不释放"略偏。
- **建议**：先交付方式二；方式一 M 系列评估 A/B 后定。

#### 测试
- Rust：`stop_all_instances` 并发化单测（fake manager、0/20 实例、批量顺序断言）；退出事件时序。
- 前端：弹窗组件（两按钮态、进度条渲染、取消分支）。
- `go test -count=1 ./...` + `cargo check` 全绿。

#### 验证
- 桌面真机：20 实例运行 → 托盘退出 → 弹窗 →「退出并释放」→ 进度 0/20 平滑推进 → 关闭；
  `tasklist` 确认 opencode2api/sing-box 全退、端口全释放。
- 方式一（若实施）：退出后进程存活 → 重开应用 → 实例 Running 且无需重启可直接请求。

#### 阶段开头：P3 遗留声明
- （P3 验证不通过项在此声明并优先处理。）

---

### 阶段 D2：一键测试未启动实例误报修复 + 延迟毫秒级显示（新增 2026-08-12）

**背景（用户反馈）**：实例池/独享页「一键测试」对**未启动的实例也发起测试**，
未启动实例返回 `OK:false` 被计入失败 → 10 个实例（7 正常 3 停止）报「成功 7 失败 3」
且**红色 toast**，用户误以为启动项有异常。

**根因（已核实代码）**：
- 前端 `src/pages/PoolPage.tsx`（同 `InstancesPage.tsx`）`doAll('test')`：
  对**全部池成员**（含未 Running 的）`Promise.allSettled(names.map(testInstance))`，
  `ok:false` 一律计入 `fail`（第 222~258 行）。
- 后端 `core/manager/admin_ops.go` `InstancesTestHandler`：未 Running 实例返回
  `TestResult{OK:false, Message:"实例 'x' 当前状态为 Stopped，请先启动后再测试"}`（已有友好文案，
  但前端错误归类）。**非运行实例本不该进入测试流程。**

#### 功能开发
1. **前端一键测试逻辑修正**（`PoolPage.tsx` + `InstancesPage.tsx` 的 `doAll('test')`）：
   - 先过滤：`names = members.filter(i => i.status === 'Running').map(i => i.name)`；
     **未启动（Stopped/Starting/Stopping/Error）不发起测试**。
   - 统计三类：`ok`（测试通过）/ `fail`（真测试失败，如超时/HTTP 错误）/ `skipped`（未测试的非 Running 实例数）。
   - toast 文案：`测试完成：正常 7 个，未启动 3 个，失败 0 个`——
     **仅 `fail > 0` 时红色**；`skipped > 0` 用中性色（zinc/amber），`ok` 用绿色。
   - 未启动实例**不显示测试徽章**（保持原有健康状态徽章），或显示中性提示（如「未测试 · 已停止」）。
2. **后端可加 `Skipped` 语义（可选，建议）**：`TestResult` 增加 `state` 字段
   （`running`/`stopped`/`starting`/`stopping`/`error`）供前端区分归类，避免前端猜状态；
   保持向后兼容（新增字段不影响既有前端）。
3. **延迟毫秒级显示（全局）**：
   - 测试徽章（`PoolPage.tsx` / `InstancesPage.tsx` `testBadge`）：已显示 `ms`，保持。
   - **P1/P2 探活 UI**（P3 阶段）：`AvgLatencyMS` **一律毫秒整数显示**（如 `1234ms`），
     不允许秒级小数（`1.2s`）——统一 `{n}ms` 格式（>1000ms 也不换算秒，满足"毫秒级"诉求）。
   - P3 草图延迟列样式同步改为毫秒（例：`▓▓▓▓ 1234ms`）。

#### 测试
- 前端：`doAll('test')` 过滤逻辑单测（混合状态集合 → ok/fail/skipped 计数）；
  toast 颜色分支（fail>0 红 / 仅 skipped 中性 / 全 ok 绿）。
- 后端（若加 `state` 字段）：`InstancesTestHandler` 单测——Running 返回 `state:"running"`、
  Stopped 返回 `state:"stopped"` 且不进 `freeCompletion`。
- `go test -count=1 ./...` + `npm run build` 全绿。

#### 验证
- 浏览器走查：10 实例（7 Running + 3 Stopped）一键测试 → toast「正常 7，未启动 3，失败 0」
  中性色；3 个未启动实例无红色徽章；再停 1 个后重测计数正确。
- **验收**：未启动实例不再误报失败；红色 toast 仅真失败出现；延迟全部毫秒级。

#### 阶段开头：P3 遗留声明
- （P3 验证不通过项在此声明并优先处理。）

---

### 阶段 D3：并发设置抽离到设置页「并发与性能」分组（新增 2026-08-12）

**背景（用户需求）**：并发与电脑性能/上游额度强相关，应抽到设置菜单由用户自定。
现有并发多为**硬编码**（扫描 8 写死、批量 start 4/stop 8、一键测试前端全量并行无上限），
弱机无法调低，强机无法拉高。

**并发与资源关系（决策依据）**：
| 并发项 | 资源开销 | 级别 |
|---|---|---|
| 节点扫描并发 | 每路**一对真实探针进程**（sing-box+opencode2api，各 15~30MB 内存 + 1 对端口） | 进程级·吃内存/CPU |
| 批量启停并发 | 同时起/杀进程，CPU 尖峰 | 进程级 |
| 一键测试并发 | 同时 HTTP 测试实例 | 进程+网络级 |
| 一键释放并发 | 同时杀进程 | 进程级 |
| 探活并发（P1） | 轻量 HTTP 探测，**吃上游配额** | HTTP 级 |
| 竞速副本（P2） | 每副本一个上游请求，**N 倍额度** | HTTP 级·吃额度 |

#### 功能开发
1. **配置项**（`core/manager/config.go` + `config.example.json`）：
| 配置键 | 类型 | 默认 | 现状 | 说明 |
|---|---|---|---|---|
| `scan_concurrency` | int | 8 | 硬编码 8（上限 8） | 节点扫描并发，进程级 |
| `batch_op_concurrency` | int | 4 | start 4 / stop 8 硬编码 | 批量启停统一并发 |
| `test_concurrency` | int | 4 | **无上限全并行** | 一键测试并发上限（补防打爆） |
| `release_concurrency` | int | 4 | D1 拟 4 | 一键释放并发（D1 引入） |
| `pool_probe_concurrency` | int | 4 | P1 拟 4 | 探活并发（P1 引入） |
| `pool_race_copies` | int | 2 | P2 拟 2（1~4） | 竞速副本=额度倍率（P2 引入） |
2. **设置页新增「并发与性能」分组**（`src/pages/SettingsPage.tsx`，卡片结构，草图如下）：

```
┌─ 并发与性能 ★新增 ──────────────────────────────────┐
│ 节点扫描并发        [ 8 ▼ ]  提示:同时探测几路节点，     │
│                            每路起一对探针进程(吃内存)   │
│ 批量启停并发        [ 4 ▼ ]  提示:同时启动/停止实例数，  │
│                            进程尖峰与CPU相关           │
│ 一键测试并发        [ 4 ▼ ]  提示:同时测试几个实例，     │
│                            过高会占满网络/CPU          │
│ ── 性能模式(依赖 P1/P2，未实现时灰置) ────────────  │
│ ⚡ 性能模式开关      [ 开/关 ]                         │
│ 探活并发            [ 4 ▼ ]  提示:轻量HTTP探测，       │
│                            吃上游配额，建议≤4          │
│ 竞速副本数          [ 2 ▼ ] 1~4 提示:每请求同时发往几路 │
│                            =几倍额度，2倍起步          │
│ ── 释放(依赖 D1) ────────────────────────────────  │
│ 一键释放并发        [ 4 ▼ ]  提示:同时释放实例数        │
│ [保存并发设置]                                       │
└─────────────────────────────────────────────────────┘
```

3. **取消硬编码**：`probe.go` 的 `Concurrency` 上限改读配置；`batch.go` worker 数改读配置；
   前端 `doAll('test')` 的 `Promise.allSettled` 改**分块并发**（按 `test_concurrency`）。
4. 每项配一行浅色提示文案说明"吃什么资源"，弱机用户一目了然。
5. 分组内用分隔线区分进程级/HTTP 级/依赖阶段，依赖未完成的项灰置不可改。

#### 测试
- `core/manager/config_test.go`：新配置默认值/非法回退/热更新解析。
- `probe_test.go` / `batch` 测试：并发改配置驱动后行为正确（fake runner 计数）。
- 前端：分组渲染、灰置逻辑、保存调 `configSet`。
- `go test -count=1 ./...` + `npm run build` 全绿。

#### 验证
- 浏览器走查：并发分组可见可保存；调低扫描并发后扫描行为符合（并发上限生效）；
  一键测试 30 实例时不再全量齐发。
- **验收**：全部并发项可在设置页配置并热生效；弱机调低、强机拉高均生效。

#### 阶段开头：D2 遗留声明
- （D2 验证不通过项在此声明并优先处理。）

---

### 阶段 M1：多端——Linux 桌面（系统调用替换 + 打包）

**目标**：Linux 桌面版跑通（实例启停/扫描/网关/托盘），产出 deb/rpm/AppImage。

#### 阶段开头：D1 遗留声明
- （D1 验证不通过项在此声明并优先处理。）

#### 功能开发
1. `core/manager/netstat_other.go`：空桩替换为 **lsof（优先）/ procfs `/proc/net/tcp` 兜底**
   端口占用解析与清理（与 Windows `netstat_other` 行为等价）。
2. `core/manager/autostart.go`：非 Windows 实现——Linux 写 `~/.config/autostart/*.desktop`；
   查询/设置/移除三操作。
3. Clash Verge profiles 目录按平台配置（Linux `~/.config/clash-verge*`），其余平台配置项覆盖。
4. `src-tauri/src/embed.rs`：按 `cfg!(target_os)` 选平台 sing-box 二进制；CI 矩阵下载。
5. `src-tauri/tauri.conf.json`：加 deb / rpm / AppImage 打包目标；Linux 托盘与窗口验证。

#### 测试
- Go：lsof/procfs 解析单测（golden 样例输出）；.desktop 生成单测。
- CI：Linux 构建流水线（go test 全绿 + npm build + tauri build 三包）。
- 环境隔离：AI-TESTING-GUIDE §5 三件套。

#### 验证
- Linux 真机/VM：实例启停 + 扫描 + 网关 + 托盘全链路；三包可安装启动。
- **验收**：端口清理/自启/托盘与 Windows 等价；三包产物完整。

---

### 阶段 M2：多端——macOS

**目标**：macOS 桌面版跑通，产出 dmg。

#### 阶段开头：M1 遗留声明
- （M1 验证不通过项在此声明并优先修复。）

#### 功能开发
1. autostart：macOS LaunchAgent（`~/Library/LaunchAgents/*.plist`）。
2. 端口清理：macOS 走 lsof（与 Linux 共用，平台差异收敛到配置）。
3. tauri.conf：dmg 打包；托盘/无边框窗口验证。
4. CI：macos-latest runner 构建 + sing-box 二进制下载。

#### 测试 / 验证
- Go 平台分支单测；CI macOS 构建；macOS 真机/CI 全链路 + dmg 安装走查。
- **验收**：macOS 全链路等价；dmg 完整；六页 UI 一致。

---

### 阶段 M3：Web 收尾 + 三平台联验 + 文档

**目标**：Web 部署体验收尾，四端全量联验，文档同步。

#### 阶段开头：M2 遗留声明
- （M2 验证不通过项在此声明并优先修复。）

#### 功能开发
1. Web 收尾：`docs/DEPLOYMENT.md` 与实际行为核对；Web 登录页/鉴权体验检查（Web 沿用密码鉴权，
   桌面免登录——V2 决策 #7）。
2. 跨平台回归清单：同组用例在 Win exe / Linux 桌面 / macOS / Web 四端执行
   （实例生命周期/扫描/网关/统计/日志/性能模式/退出交互）。
3. 文档：性能模式配置项进 `docs/CONFIGURATION.md`；质量分/熔断进 FAQ/API 文档；
   `docs/ARCHITECTURE-V2-PLAN.md` 验收表同步。

#### 测试 / 验证
- 四端回归记录（每端一条走查结果）；`go test` + `npm build` 全绿。
- **验收**：四端等价、文档齐、V2 验收表 P5 项打勾。

---

## 三、验收计划表（完成一项勾一项）

| # | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 / 备注 |
|---|---|---|---|---|---|
| 0 | P0 | 探针泄漏修复（defer 清理 + 成功路径断言） | ✅ | 2026-08-12 | `b2eee5c`；存量进程清理待用户确认 |
| 1 | P1 | 探活子系统：周期链路探测 + 滑动窗口评分 + 配置 + 持久化 + API | ⬜ | | 单测全绿；手动抖动模拟质量分升降 |
| 2 | P2 | 竞速模式 + 熔断/半开恢复 + 性能模式开关 | ⬜ | | 状态机单测；断断续续节点集成测试 |
| 3 | P3 | UI 质量分/熔断可视化 + 参数可调 + 六页一致 | ⬜ | | 浏览器走查；exe/Web 等价 |
| 3b | D1 | 退出二次确认 + 并行释放 + 进度条（方式二） | ⬜ | | 20 实例真机走查；tasklist 确认全清 |
| 3c | D2 | 一键测试未启动误报修复（ok/fail/skipped 分类）+ 延迟毫秒级显示 | ⬜ | | 混合状态走查；toast 颜色分支 |
| 3d | D3 | 并发设置抽离「并发与性能」分组（扫描/批量/测试/释放/探活/竞速全部可配） | ⬜ | | 弱机/强机并发走查；热生效验证 |
| 4 | M1 | Linux 桌面：lsof 端口清理 + .desktop 自启 + 三包产物 | ⬜ | | Linux 真机走查；CI 构建 |
| 5 | M2 | macOS：LaunchAgent + dmg + CI | ⬜ | | macOS 走查；CI 构建 |
| 6 | M3 | Web 收尾 + 四端联验 + 文档同步 | ⬜ | | 四端回归记录；文档核对 |

## 四、风险与备选

- **探活成本**：周期真实 HTTP 探测占用节点额度/流量——默认 45s + 并发 4 已克制；额度敏感可调或关探活。
- **竞速成本**：竞速 N 节点 = N 份上游配额——默认 2 副本（用户确认），`pool_race_copies` 可调 1~4；
  **竞速只在候选池内选，不向 down 节点浪费额度**。
- **误熔断**：探活+实测双通道后单次抖动可能误伤——阈值 3 次 + 半开自动回归兜底。
- **退出保活（方式一）**：Job Object 摘除涉及 syscall 与 attach 状态机，风险较高——方案 A/B 待用户拍板，
  默认 M 系列评估，先交付方式二。
- **多端阻塞**：sing-box 平台二进制需 CI 下载（上游可能限流）——备选：手动放置 + 文档说明。
- **行为回归**：性能模式默认开但提供开关；关闭时走基线路径（测试快照兜底）。

## 五、开工清单（P1 第一步）

1. 读 `docs/AI-TESTING-GUIDE.md` §3/§5（端口与进程检查、三件套环境隔离）。
2. `go test -count=1 ./...` 记录基线全绿（当前已验证 ✅）。
3. 读 `core/manager/config.go` / `health.go` / `socks.go` / `probe_node.go` 现有结构，
   定 `poolquality.go` 接口（复用 `socks5Dial` / `markSocks5Result` / 配置热更新机制）。
4. 分支 `feat/pool-performance` 已存在，直接在其上提交。