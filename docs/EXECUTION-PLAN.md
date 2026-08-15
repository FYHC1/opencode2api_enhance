# 执行计划：阶段化开发排期（同事执行版）

> **本文档是交给实施同事的阶段化执行明细**，与 `docs/MASTER-PLAN.md`（总览/状态矩阵）配套：
> MASTER-PLAN 回答「有哪些系列、到哪了」，本文档回答「下一步按什么顺序做、每阶段做什么、验证不过怎么办」。
>
> ## 执行机制（硬性规则，每阶段必须遵守）
>
> 1. **每阶段 = 功能开发 + 测试 + 验证** 三件套，全部完成才算阶段完成。
> 2. **验证不通过 → 下放**：本阶段验证不通过的项目，必须逐条写进**下一阶段的「阶段开头：上阶段遗留」小节**，在下一阶段优先修复并重新验证，全部通过后才允许推进下一阶段的新功能。
> 3. **一阶段一提交**：每阶段完成 `go test -count=1 ./...` 全绿（+ `-race` 关键包、`npm run build`）才提交，提交信息带阶段号（如 `feat(S1): ...`）。
> 4. **遗留声明追踪**：每个阶段的「上阶段遗留」小节，开始时先核对上一阶段验收表，把 ⬜/❌ 项抄进来；处理完一项就把验收表对应行更新。
> 5. **验证分工（两层）**：
   - **自动化验证**（子代理交付前必须完成）：单测 + 集成测试 + `go test -count=1 ./...` 全绿 + `-race` 关键包 + `npm run build` + 代码走查。
   - **端到端验收**（标注「需部署机/用户配合」项）：挂节点、20 并发、高频 429、Clash 代理出口、CI 三平台产物、docker run、macOS 真机——由用户或部署机执行，验收人在验收表打勾。
6. **环境红线**：任何真实服务启动前按 `docs/AI-TESTING-GUIDE.md` §3/§5 做端口与进程检查 + 三件套隔离；禁止 kill 非自己启动的进程。

---

## 阶段顺序总览（依赖驱动）

```
第一阶段（已完成 ✅，2026-08-14 验收通过）：
  主链 A：M3~M6（验证/CI）→ S1（竞速预算）→ S5（自适应竞速）
  支线 B：E1（上游代理出口）  支线 D：S4（日志补齐）
  主链后：S2（429 感知）→ S3（质量自愈）
第二阶段（U 系列，✅ 已完成并验收，2026-08-14）：
  U1 节点池 poll bug 修复 → U2 实例池状态筛选 → U3 轮询配置+独享轮询
  → U4 日志分页 → U5 统计迷你图
第三阶段（V 系列，2026-08-15 新增，待派工）：
  V1 停止扫描并发接逻辑（N2 拍板：probeNode 取消支持 + stop_scan_concurrency 生效）
  → V2 全局任务悬浮窗（scan/stop-scan/restart/batch/release 多任务栈）
```

> 依赖说明：第一阶段 S/E 系列已全部合入 main 并通过验收（`go test` 全绿）。
> **U 系列 = REQ-POOL-FILTER.md + REQ-UI-IMPROVEMENTS.md 的实施**（第二阶段派工单），
> 与 S/E 无代码依赖，可独立开工。U1 是缺陷修复，优先级最高。

---

## 阶段编号与当前状态

| 执行序 | 阶段 | 名称 | 状态 | 并行性 |
|---|---|---|---|---|
| — | S1~S5 + E1 | 第一阶段（请求首效/质量系统/代理出口） | ✅ 已完成并验收（`69f2bab`） | — |
| 1 | **U1** | 节点池 poll 自杀 bug 修复（缺陷） | ✅ 已完成（`6149b72`） | 🔴 优先 |
| 2 | **U2** | 实例池状态筛选（REQ-POOL-FILTER） | ✅ 已完成（`83d198d`） | 独立 |
| 3 | **U3** | 轮询配置 `ui_poll_interval_sec` + 独享轮询 + 折叠面板 | ✅ 已完成（`f0351e9`） | 独立 |
| 4 | **U4** | 日志页分页（每页 100） | ✅ 已完成（`1d16501`） | 独立 |
| 5 | **U5** | 统计页纯 CSS 迷你图 | ✅ 已完成（`6f4daad`） | 独立 |
| 6 | **V1** | 停止扫描并发接逻辑（probeNode 取消支持 + stop_scan_concurrency 生效） | ⬜ 待派工 | 🔴 拍板项 |
| 7 | **V2** | 全局任务悬浮窗（scan/stop-scan/restart/batch/release 多任务栈） | ⬜ 待派工 | 依赖 V1 停止进度数据 |
| — | M3~M6 | 多端收尾验证（CI/真机） | 🔶 待部署机验证 | 可穿插 |

---

## 阶段 S1：竞速整体预算 + 快速失败 + 冷启动不竞速

**目标**：竞速在任何情况下有界（bound）；冷启动不双倍炸上游。根因 R1/R2/R4（详见 MASTER-PLAN §三）。

### 阶段开头：上阶段（M 系列）遗留
- （M3~M6 验证不通过项在此声明：如 CI 产物缺平台、docker build 失败、macOS 打包异常——逐条列出后优先处理。）

### 功能开发
1. `vendors/opencode/chat.go` `raceDo` 主循环加整体预算：
   ```go
   budget := v.raceBudget() // 默认 10s，可配 race_budget_ms
   timer := time.NewTimer(budget)
   for {
       select {
       case o := <-results:   // 现有成功/全败分支不变
       case <-timer.C:
           cancel()           // 终止所有候选
           return nil, "", fmt.Errorf("race budget exceeded") // 非 nil 错误 → 上层续写/重试
       }
   }
   ```
2. `models_source.go` `CandidateClients`：`sc.Timeout = 0` → 首字节等待预算（同 `race_budget_ms`）；赢家流本身不设限。
3. 冷启动不竞速：`poolquality.go` 质量记录新增 `unknown` 态（无探活样本）；`socks_perf.go` `raceCandidates` 候选必须 `known=true`；全 unknown → 返回 nil（退化单发）。
4. 竞速失败接入续写兜底：错误正常进入 `retryCount` 循环的单发路径，`streamWithResume` 可接手。

**配置**：`race_budget_ms`(10000)（`types_config.go` + `config.go` + `opencodecfg.go` 透传）。

### 测试
- `race_test.go`：挂起候选（阻塞 RoundTrip）→ 预算到期返回错误 + `cancel` 调用 + 无悬挂 goroutine（`-race`）；快候选预算内返回不回归；预算=0 回退 10s。
- `socks_perf_test.go`：unknown 节点不参与竞速；全 unknown → nil；探活一轮后进入候选。
- 流式候选：等待期超时返回错误；锁流后长流不被截断（计时读完）。
- `go test -count=1 ./...` 全绿 + `go test -race` 关键包。

### 验证
- 自动化：`go test` 全绿 + `-race`；挂起候选用例（阻塞 RoundTrip）单测通过。
- 端到端（需部署机/用户配合）：挂起一个节点（临时断 sing-box）→ grok 首次请求 ≤10s 得到响应/明确错误，不无限悬着。
- **验收**：竞速有界；unknown 冷启动不竞速；超时/失败后走续写链。验收表 S1 行打 ✅。

---

## 阶段 S5：自适应竞速——压力系数动态副本 + in-flight 均衡

**目标**（用户核心需求）：节点池大 + 少窗口 → 竞速全开；节点池小 + 多窗口 → 自动降单发分散。运营零干预。

### 阶段开头：S1 遗留
- （S1 验证不通过项在此逐条声明并优先修复，修复后回归验证；通过后才进入 S5 新功能。）

### 功能开发（文件级）
| 项 | 位置 | 内容 |
|---|---|---|
| 活跃请求计数 | `vendors/opencode/chat.go` 或网关入口 | 全局 `atomic.Int64`：请求进入 +1 / 完成 -1 |
| 每节点 in-flight | `socks_perf.go` | `map[addr]*atomic.Int64`：发出 +1 / 完成 -1 |
| 动态副本 | `raceCopies()` 改造 | `copies = clamp(上限, 1, 按 pressure 分段)`；pressure = 活跃请求数 / 健康节点数（滑动均值 5s 平滑） |
| 候选均衡 | `raceCandidates` 改造 | in-flight 最少优先 > 质量分 > 随机扰动 |
| 配置 | `pool_race_copies` 语义改为**上限** | 新增 `pool_race_pressure_high`(1.0) / `pool_race_pressure_low`(0.5) |

**压力分段**：pressure < 0.5 → 副本=上限；0.5~1.0 → 2；1.0~2.0 → 1（等效 1.3.1 分散轮转）；≥2.0 → 1 + 候选随机化摊负载。

### 测试
- 单测（fake transport + 假并发计数）：低压力(0.1)→上限；中(0.7)→2；高(1.5/3.0)→1；in-flight 3/0 → 选中 0；同 in-flight 质量分优先；单出口/全 unknown → nil。
- 集成：20 节点 1 请求 与 8 节点 50 请求两场景，断言副本动态变化、各节点 in-flight 方差小。
- `go test -count=1 ./...` + `-race` 全绿。

### 验证
- 自动化：两场景单测（20 节点 1 请求 / 8 节点 50 请求）+ in-flight 均衡断言全绿。
- 端到端（需部署机/用户配合）：单窗口看日志竞速副本=2；开 20 并发后自动降单发、节点 in-flight 分散。
- **验收**：压力动态调节生效；高并发无流量聚集；429 触发率下降；与 S1 预算协同。

---

## 阶段 S2：429 感知——降级单发 + 指数退避 + 可见报错

**目标**：限流时不放大流量、重试退避、用户看到明确原因。根因 R3/R8。

### 阶段开头：S5 遗留
- （S5 验证不通过项在此逐条声明并优先修复。）

### 功能开发
1. 429 冷却内跳过竞速：记录最近 429 时间戳；距上次 < `rate_limit_cooldown_sec`(30) → 直接单发（不调 `raceDo`）。
2. 指数退避：429 重试前 `sleep(min(base*2^n, cap))`，base=1s cap=30s（`rate_limit_backoff_base_ms`/`cap_ms`）。
3. 可见报错：429 最终失败错误体携带「免费额度已用尽（Rate limit exceeded），请稍后重试」，保持 status=429 透传。

**配置**：`rate_limit_cooldown_sec`(30) / `rate_limit_backoff_base_ms`(1000) / `rate_limit_backoff_cap_ms`(30000)。

### 测试
- 单测（fake transport）：首 429 → 二次验证未走 `raceDo`；退避序列 1/2/4…30 cap；错误文案含"额度"；非 429 场景竞速行为不变。
- 配置解析默认值/非法回退。

### 验证
- 自动化：fake transport 单测（首 429 后未走 raceDo、退避序列、文案）全绿。
- 端到端（需部署机/用户配合）：连续高频请求触发 429 → 网关日志请求量不再双倍（单发）；错误体文案正确。
- **验收**：429 后竞速自动降级；重试有退避；用户看到"额度用尽"。

---

## 阶段 S3：质量自愈——坏池恢复 + 半开竞速 + unknown 等级

**目标**：坏池/熔断节点自动恢复，全池故障能自愈。根因 R5/R6。

### 阶段开头：S2 遗留
- （S2 验证不通过项在此逐条声明并优先修复。）

### 功能开发
1. **坏池分类型恢复**（2026-08-14 用户拍板）：
   - **链路类**（连接失败/超时/5xx）：可自动恢复——`socks.go` `badReason` 加过期
     `badUntil = now + bad_pool_reset_sec`(300)；到期放行 1 探测，成功清状态 / 失败重新坏池（重置计时）。
   - **账号类**（401/402/429 额度/认证）：**不自动恢复**——保持现有 `badReason` 永久禁用语义，
     用户手动处理（UI 提供一键清除坏池入口，或重启生效）。禁止对账号类节点自动试探（避免反复打上游烧额度）。
2. 半开进竞速：`socks_perf.go` `raceCandidates` 候选不足 n 时放行 1 个 half_open 节点兜底。
3. unknown 等级正式化：`poolquality.go` `PoolQualitySummary` 增 unknown 计数；UI 可显示"探测中"（S4 前端一并做）。

**配置**：`bad_pool_reset_sec`(300)。

### 测试
- `socks_test.go`：链路类坏池到期放行 → 成功清 badReason / 失败重新坏池；重置计时；**账号类（401/402/429）永不自动放行**（断言无试探、badReason 保持）。
- `socks_perf_test.go`：候选不足时 half_open 被选；候选充足时不选（不回归）。
- `poolquality_test.go`：unknown 计数；空窗口状态。
- 全绿 + `-race`。

### 验证
- 手动（需部署机/用户配合）：制造**链路类**故障（断 sing-box）三次 → 进坏池 → 不干预等待 `bad_pool_reset_sec` → 自动试探 → 恢复；制造 **429** 三次 → 进坏池 → 等待后**不自动试探**（保持禁用，UI 一键清除入口可解）。
- **验收**：链路类自愈、账号类保持禁用；无重启依赖。

---

## 阶段 S4：调用日志补齐 + 429 提示 + 探活目标可配

**目标**：全链路日志可见；429/超时 UI 明确提示；探活不加重限流。根因 R7/R8 补充。

### 阶段开头：S3 遗留
- （S3 验证不通过项在此逐条声明并优先修复。）

### 功能开发
1. 日志补齐：① `core/manager/calllog.go` 日志页聚合读取统一网关 + 各 Running 独享实例 `call_log.jsonl`（按实例名标注来源，合并排序，各文件 tail 5000）；② `responses.go`/`claude.go` 补 `CallRecord` 构造 + `recordCall`（对齐 chat_handler 三态）。
2. 前端 `src/pages/LogsPage.tsx`：429 错误行显示「额度用尽」标签；超时/切换事件颜色区分。
3. 探活目标可配：`poolquality.go` `probeTargetURL` + `pool_probe_target` 配置（默认 "" = 自动拼接）。

**配置**：`pool_probe_target`("")。

### 测试
- `calllog_test.go`：多目录聚合读取、按时间合并排序、实例名标注。
- `responses_test.go`/claude 测试：handler 走查断言 `CallLogRecord` 落盘（httptest + 临时目录）。
- `poolquality_test.go`：`pool_probe_target` 覆盖默认拼接。
- 前端 `npm run build` + 日志页渲染走查。

### 验证
- 自动化：calllog 聚合单测 + responses/claude handler 落盘断言 + `npm run build`。
- 端到端（需部署机/用户配合）：独享实例发请求 → 日志页可见（含来源标注）；grok 走 `/v1/responses` → 日志页可见；制造 429 → 日志带"额度用尽"标签。
- **验收**：三种协议 + 独享/网关全链路日志；429 提示明确；探活目标可配。

---

## 阶段 U1：节点池 poll 自杀 bug 修复（🔴 缺陷，最高优先）

**目标**：修复 `NodesPage.tsx` 停止扫描后轮询循环永久终止的 bug（用户可复现）。

**复现**：勾 100 节点 → 扫描 → 停止 → 再勾选扫描 → 进度条 0 无迹象。

**根因**：`NodesPage.tsx:183-189` 轮询循环 stopAck 分支命中时 `return` 直接退出，
跳过底部 `setTimeout(poll, 800)` —— poll 循环永久死亡。

**修复**（详见 `docs/REQ-UI-IMPROVEMENTS.md` #2）：stopAck 分支不 return，
跳过本次 setScan 但继续轮询。

**测试**：复现路径走查（停止→再扫，进度正常）；轮询在停止后仍存活。
**验证**：浏览器按复现步骤走查；**验收：停止后再次扫描进度正常。**

---

## 阶段 U2：实例池状态筛选

**目标**：实例池页补状态筛选（全部/运行中/已停止），对齐独享页。
**改动**：`src/pages/PoolPage.tsx`（详见 `docs/REQ-POOL-FILTER.md`，已定稿含代码）。
**测试/验证**：过滤逻辑断言 + 浏览器走查（搜索+筛选叠加、批量操作与筛选联动）。

---

## 阶段 U3：轮询配置 + 独享页轮询 + 折叠面板

**目标**：`ui_poll_interval_sec` 配置（默认 5s，可配 1~60，0=关）+ 独享页自动轮询 +
两页设置弹窗统一折叠面板（预留扩展）。
**改动**：`types_config.go` / `core/manager/config.go` / `InstancesPage.tsx` / `PoolPage.tsx`
（详见 `docs/REQ-UI-IMPROVEMENTS.md` #3）。
**测试/验证**：配置解析回退、轮询按新间隔生效、折叠面板展开收起、kill 进程状态自动更新。

---

## 阶段 U4：日志页分页

**目标**：日志大列表性能——前端分页每页 100，不引虚拟滚动库。
**改动**：`src/pages/LogsPage.tsx`（详见 `docs/REQ-UI-IMPROVEMENTS.md` #4）。
**测试/验证**：5000+ 条日志渲染流畅；翻页正确；轮询后停留当前页。

---

## 阶段 U5：统计页迷你图（克制版）

**目标**：纯 CSS 条形图（成功/失败占比、按天趋势），不引图表库。
**改动**：`src/pages/StatsPage.tsx`（详见 `docs/REQ-UI-IMPROVEMENTS.md` #5）。
**测试/验证**：`npm run build`；0 值不崩溃；百分比正确。

---

## 阶段 V1：停止扫描并发接逻辑（N2 拍板项，2026-08-15）

**背景**：`stop_scan_concurrency`（默认 4）目前是**死配置**——配置项/校验/title 齐全，
但停止逻辑从未使用；扫描停止 = `RequestStop()` 置 Stopping 标志，所有 worker 同时收到
（已并发），正在跑的 `probeNode` 阻塞无取消机制（最长 25s 干等）——这才是停止慢的真因。
需求文档：`docs/REQ-GLOBAL-TASK-PANEL.md` 第一节。

**拍板语义**（用户确认）：`stop_scan_concurrency` = **停止时并发中断正在探测进程的上限**
（防一次性斩断全部 worker 的资源尖峰）。

**功能开发**：
1. `core/manager/probe_node.go`：`probeNode` 加**取消支持**——探测循环可被停止信号中断，
   中断时 `runner.Kill(sbPID/ocPID)` 清理探针进程（复用 P0 defer 清理路径）；
2. `core/manager/probe.go`：`RequestStop(concurrency)` 接收并发上限，按限流中断正在跑的探测；
3. 前端 `api.scanStop()` 可传并发数（或读全局配置 `stop_scan_concurrency`）；
4. `RequestStop` 返回 `stopping_count`（当前探测中数）/ `stopped_count`（已停数），供 V2 悬浮窗进度。

**测试**：取消路径单测（探测中收到停止 → 探针进程被 Kill、goroutine 无泄漏 -race）；
并发上限边界；stopping_count/stopped_count 计数正确。

**验证**：挂起节点 → 停止 → 观察正在跑的探针被快速中断（不再等 25s）；配置 4 并发时
中断进程数 ≤4。**验收：停止响应明显变快；stop_scan_concurrency 生效。**

---

## 阶段 V2：全局任务悬浮窗（2026-08-15）

**目标**：长耗时操作在右下角**全局悬浮窗**显示进度，**用户可切换页面，不必留在原页等**。
需求文档：`docs/REQ-GLOBAL-TASK-PANEL.md` 第二节（含设计图）。

**功能开发**（文件级）：
1. `src/App.tsx`：`release` state 扩为**通用多任务栈** `tasks: [{id,type,title,done,total,active}]`；
   渲染右下角悬浮栈（fixed bottom-5 right-5 z-50 w-72，多任务纵向堆叠，每任务可 ✕ 关闭）；
   type→颜色/文案映射（release=red / scan=teal / stop-scan=amber / restart=amber / batch=teal）；
   现有 `onRelease` 包装兼容池页调用。
2. `src/pages/NodesPage.tsx`：扫描中上报 `scan`（Current/Total，来自轮询）；停止中上报
   `stop-scan`（done=stopped_count, total=stopping_count——依赖 V1 后端返回）。
3. `src/pages/PoolPage.tsx`：`doRestart` 上报 `restart`（阶段进度：停网关→停全部→释放端口→启成员→网关）；
   批量启停/测试上报 `batch`（分块并发 done/total）。
4. `src/pages/InstancesPage.tsx`（可选）：独享页批量操作同上报 `batch`。

**测试**：多任务并存堆叠；✕ 关闭不影响后台；跨页常驻进度更新；完成自动消失；
0/0 不崩溃；`npm run build` + tsc 全绿。

**验证**（需部署机/用户）：扫描 20 节点 → 切统计页 → 悬浮窗持续显示至完成；
停止扫描 → 切页 → 停止进度显示；多任务并存互不干扰。

---

## 阶段 E1：上游代理出口（MVP）

**目标**：设置页填「上游代理」（如 `socks5://127.0.0.1:7897`），所有实例 `active_socks5` 指向它，绕过本机裸连 IP 限流。已验证可行（详见 MASTER-PLAN §四·五）。

### 阶段开头：S4 遗留
- （S4 验证不通过项在此逐条声明并优先修复。）

### 功能开发（文件级）
1. 配置项 `upstream_proxy`（string，默认 "" = 现状直连）：`types_config.go` + `core/manager/config.go` 解析 + `config.example.json`。
2. `core/manager/opencodecfg.go`：`upstream_proxy` 非空时 `active_socks5`/`socks5_proxies` 指向代理（解析 scheme 去前缀取 host:port；Clash mixed-port 同时支持 socks5/http）。
3. `src/pages/SettingsPage.tsx`：「代理出口」区块——输入框（placeholder `socks5://127.0.0.1:7897`，留空 = 直连）+ 说明文案。
4. 扫描/实例/网关自动受益（共用 `buildOpenCodeCfg`）；配置后需重启实例/网关生效（先按此实现）。

### 测试
- 配置解析：空/带 scheme/非法端口回退。
- `opencodecfg` 生成：配置后 active_socks5 指向代理 + socks5_proxies 单元素；未配置 = 现状（回归快照）。
- 前端 build + 设置页渲染。

### 验证
- 自动化：配置解析单测 + `opencodecfg` 生成断言（代理地址/回归快照）+ `npm run build`。
- 端到端（需部署机/用户配合）：填 Clash 代理 → 重启实例 → 请求上游 200；批量扫描不再 0/N 全挂。
- **验收**：上游代理配置生效；扫描/实例流量走代理出口；未配置时行为与现状一致。

---

## 附：验收计划表（同事逐项打勾）

| 执行序 | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 |
|---|---|---|---|---|---|
| 1 | M3~M6 | CI 三平台产物 / docker run / macOS dmg | 🔶 | | CI 触发 + 真机（需部署机） |
| 2 | **E1** | 上游代理配置生效，扫描/实例走代理出口 | ⬜ | | 自动化单测 + 填 Clash 代理批量扫描不再全挂（端到端需部署机） |
| 3 | S1 | 竞速有界 + 冷启动不竞速 + 续写兜底 | ⬜ | | 挂起候选用例 + 端到端需部署机 |
| 4 | S5 | 压力动态副本 + in-flight 均衡，无聚集 | ⬜ | | 两场景单测 + 手动 20 并发（端到端需部署机） |
| 5 | S4 | 日志全链路 + 429 标签 + 探活目标可配 | ⬜ | | 聚合单测 + 独享/Responses/Claude 可见（端到端需部署机） |
| 6 | S2 | 429 降级单发 + 退避 + 可见文案 | ⬜ | | fake transport 单测 + 高频触发日志减半（端到端需部署机） |
| 7 | S3 | 链路类自愈 / 账号类不自动恢复 + 半开竞速 + unknown | ⬜ | | 分类型单测 + 断连/429 实测（端到端需部署机） |
| 8 | U1 | poll 自杀 bug 修复：停止后再扫描进度正常 | ✅ | 2026-08-14 | 代码级修复+走查（自动化 ✅）；浏览器复现路径待部署机 |
| 9 | U2 | 实例池状态筛选可用，与独享页一致 | ✅ | 2026-08-14 | 过滤逻辑走查 + tsc（自动化 ✅）；浏览器走查待部署机 |
| 10 | U3 | 轮询间隔可配 + 独享页自动轮询 + 折叠面板 | ✅ | 2026-08-14 | 配置解析单测（含 0=关持久生效）+ 前端走查（自动化 ✅）；浏览器待部署机 |
| 11 | U4 | 日志分页：5000+ 条流畅 | ✅ | 2026-08-14 | 分页逻辑走查 + tsc（自动化 ✅）；渲染走查待部署机 |
| 12 | U5 | 统计迷你图（纯 CSS） | ✅ | 2026-08-14 | `npm run build` + 占比/0 值走查（自动化 ✅）；目视待部署机 |
| 13 | V1 | stop_scan_concurrency 生效：停止中断探测 ≤ 配置并发，停止明显变快 | ⬜ | | 挂节点停止实测 + 单测 |
| 14 | V2 | 全局悬浮窗：scan/stop-scan/restart/batch/release 多任务跨页常驻 | ⬜ | | 切页走查 + 多任务堆叠 |

> 维护规则：每完成一项把状态改 ✅ 并注明日期/验证命令。验证不通过 → 该行保持 ❌，
> 下一条目阶段的「阶段开头：上阶段遗留」中声明并优先修复。