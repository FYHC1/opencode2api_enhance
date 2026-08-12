# REVIEW — P/D 系列验收差异清单（2026-08-12 复核）

> 本文档由海鸥对 `main` 上已合入的 P/D 系列提交做**代码级复核**产出，
> 供实施同事对照修正。复核方式：只读分析（`git log` / `git show` / grep），未改动任何代码。
> 复核基线：`main@baf49df`（已含 P0~P3 + 附加），`go test -count=1 ./...` 全绿 ✅。

> **【2026-08-12 整改完成】** 复核差异项已全部补齐并推送（`go test -count=1 ./...` 9 包全绿 + 前端构建通过）：
> - P2b 竞速补齐 → `64d2a20`；D2 一键测试三分类 → `782cad3`；
> - D3 并发设置抽离 → `820576c`；D1 退出二次确认 → `b0bf9ba`；
> - P3 残留细节核实时已满足（毫秒直出 / 设置页参数）。以下表格已按完成态更新。

---

## 〇、总评

| 阶段 | 状态 | 结论 |
|---|---|---|
| P0 探针泄漏修复 | ✅ 通过 | 实现与计划一致（defer 清理 + 成功路径断言） |
| P1 探活 + 滑动窗口评分 | ✅ 通过 | `core/manager/poolquality.go` + 配置 + API 齐全 |
| **P2 竞速模式** | ✅ **已补齐** | 质量加权路由 + 熔断 + **请求级竞速**（P2b，`64d2a20`） |
| P3 UI 质量徽标 | ✅ 已通过 | 池页质量分/性能模式开关；设置页参数已入（`820576c` 并入并发分组） |
| D1 退出二次确认 | ✅ **已补齐** | 前端三选弹窗 + 并行释放 + 全局面板进度（`b0bf9ba`） |
| D2 一键测试误报 | ✅ **已补齐** | `doAll('test')` 仅测 Running，未启动计「跳过」（`782cad3`） |
| D3 并发设置抽离 | ✅ **已补齐** | scan/batch/test/pool_probe 并发可配 + 设置页「并发设置」分组（`820576c`） |
| 附加：残留进程清理 | ✅ 超出计划 | `feature/orphan-cleanup` → `dd13af9`，做得好 |

**核心结论：P2 竞速（用户最大痛点的直接解药）被实现成了"加权路由"替代品，竞速本体未落地；
D 系列三项全部缺失。** 详见下。

---

## 一、🔴 P2 竞速缺失（最高优先级）

### 计划要求（PERFORMANCE-PLATFORM-PLAN.md 阶段 P2）

- `raceCandidates(need)` 候选池选择
- `raceRequest(...)`：一个请求**并行扇出 N 个节点（N=`pool_race_copies`，默认 2）**；
  非流式第一个完整 2xx 胜出、流式第一个 chunk 胜出，其余 `cancel()` 断开
- 配置 `pool_race_copies` / `pool_performance_mode`

### 实际实现（`socks_perf.go`，b848ce4）

| 能力 | 计划要求 | 实际 |
|---|---|---|
| 质量加权路由 | ✅ 有 | ✅ `pickWeightedProxy`：healthy 优先/degraded 降权/flaky 跳过/down 剔除，7:3 融合实测量 |
| 熔断状态机 | ✅ 有 | ✅ open / half-open / closed，阈值 3、半开 60s，成功回归 |
| 请求结果回填 | ✅ 有 | ✅ `recordPoolFeedback` 10 分钟窗口，实测量与探活分融合 |
| **请求级竞速** | ✅ **必须有** | 🔴 **无**：全仓 grep `raceRequest` / `raceCandidates` / `race_copies` / `竞速` / `hedged` 仅命中注释一行 |
| 配置 `pool_race_copies` | ✅ 必须有 | 🔴 不存在（config 仅有 probe/breaker/halfopen/performance_mode） |

### 影响（用户视角）

**用户最初的核心诉求**：节点断断续续（10s 无响应又恢复）→ 发一个问题同时发往多个节点，
谁先响应用谁 → **响应速度 = 最快节点**。当前实现的加权路由只能"选个更可能好的节点串行发"，
单个请求仍可能撞上抖动节点卡 10s——**只解决了"决策依据"，没解决"响应快"**。
原计划明确写着"纯加权路由 ❌（被动）"被否掉，竞速+评分闭环才是拍板方案。

### 修正要求

按计划 P2 补齐：
1. 配置 `pool_race_copies`（1~4，默认 2）+ `getHTTPClientWithProxy` 旁路竞速客户端集合
2. `raceRequest(ctx, req, candidates, streaming)`：并行 N goroutine，首响应胜，其余 cancel
3. 流式：首 chunk 锁定后转独占（与 `gateway_timeout.go` 续写通道衔接）
4. 竞速结果回写：赢家成功 + 反馈窗口；失败者冷却（复用 `applyPoolResult` / `markSocks5Result`）
5. 测试：双后端快/慢竞速、慢者 cancel 断言、全败回退、流式首 chunk 锁定、`pool_race_copies=1` 退化串行

---

## 二、🔴 D1 未做：退出二次确认 + 并行释放 + 进度条

计划：退出弹窗（退出并释放/退出不释放/取消）→ 并行释放（batch 4）→ 进度 0/20 → exit。
现状：`src-tauri/src/lib.rs` quit 仍串行 `stop_all_instances`（40 次 taskkill），无确认、无进度。
`src/App.tsx` 仅有一键释放面板，与退出流程无关。
修正按计划 D1；方式一（Job Object 摘除保活）排期待用户拍板。

## 三、🔴 D2 未做：一键测试未启动误报

计划：`doAll('test')` 过滤非 Running → ok/fail/skipped 三分类 → 红 toast 仅真失败 → 延迟毫秒级。
现状：`src/pages/PoolPage.tsx` / `InstancesPage.tsx` 仍全量 `Promise.allSettled`，未启动计失败，
红 toast 误报。grep `skipped` / `未启动` 无命中。修正按计划 D2。

## 四、🔴 D3 未做：并发设置抽离

计划：`scan_concurrency`(8) / `batch_op_concurrency`(4) / `test_concurrency`(4) /
`release_concurrency`(4) / `pool_probe_concurrency`(4) / `pool_race_copies`(2) → 设置页「并发与性能」分组。
现状：扫描并发 8 仍硬编码（`probe.go`）、批量 start 4/stop 8 硬编码（`batch.go`）、
一键测试无上限；全仓 grep `scan_concurrency` 等无命中。修正按计划 D3。

---

## 五、🔶 P3 部分通过 + 细节修正

已通过：池页质量分徽标、性能模式开关、质量分/延迟展示。
需修正：
1. **探活延迟显示单位**：计划要求毫秒级整数（`1234ms`，不换算秒）。检查 UI 是否 `ms` 直出；
   若显示为秒级小数需改。
2. **设置页**：性能模式参数（探活间隔/窗口/熔断阈值/半开间隔）是否已入设置页？未入则补
   （与 D3 合并实施亦可）。

---

## 六、✅ 附加好评

- `dd13af9` 残留进程探测 + 一键清除：超出计划（P0 遗留的"存量进程清理"），覆盖
  `runtime/_probe/worker-*` 识别维度，直接解决用户机器 90+ 残留进程问题——方向正确。
- 工作区未提交的「统计页按天查看」（`stats_day.go` + `StatsPage.tsx`）：与计划无冲突，可继续。

---

## 七、修正后的建议排期

| 序 | 事项 | 预估 |
|---|---|---|
| 1 | **P2 竞速补齐**（socks_perf.go 扩展 raceRequest + 配置 + 测试） | 高优先 |
| 2 | D2 一键测试三分类（前端小改 + 后端 state 字段可选） | 快 |
| 3 | D3 并发设置（config + 设置页分组 + 硬编码消除） | 中 |
| 4 | D1 退出流程（Rust + 前端弹窗 + 进度条） | 中 |
| 5 | P3 残留细节（延迟单位、设置页参数） | 快 |

> 复核人注：以上修正项完成后再跑 `go test -count=1 ./...` + `npm run build`，并对照
> `docs/PERFORMANCE-PLATFORM-PLAN.md` 验收表打勾。竞速补齐后需补「断断续续节点」集成测试
> （fake 双后端交替成功/超时）作为 P2 验收证据。