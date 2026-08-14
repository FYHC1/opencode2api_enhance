# 验收报告：S/E 系列实施检查（2026-08-14）

> 验收人：海鸥（只读分析，未改代码）
> 验收基线：`main@22665c3`（同事已合入 E1/S1/S2/S3/S4/S5）
> 测试状态：`go test -count=1 ./...` **全绿**（9 包，含 `-race` 关键包）

---

## 一、总体结论

**S/E 系列（EXECUTION-PLAN 范围）—— 实现扎实，验收通过 ✅**

| 阶段 | 核心实现 | 位置 | 状态 |
|---|---|---|---|
| S1 竞速预算 | `select + timer` 整体预算（默认 10s）、流式首字节预算、冷启动 unknown 不竞速、超时非 nil 错误走续写 | `vendors/opencode/chat.go` `raceDo` + `socks_perf.go:539` | ✅ |
| S5 自适应竞速 | 压力系数动态副本（<0.5 上限 / 0.5-1.0 取 2 / ≥1.0 单发）、`proxyInFlight` 每节点计数、高压力随机摊开 | `chat.go:314` `raceCopies` + `socks_perf.go:163` | ✅ |
| S2 429 感知 | `last429` 时间戳冷却内跳竞速、指数退避 `rateLimitBackoff`、中文文案 status 透传 | `chat.go:265-290` | ✅ |
| S3 质量自愈 | 坏池**分类型**（链路类 `badUntil` 到期半开 / 账号类零值永久禁用）、halfopen 竞速兜底、unknown 计数 | `socks.go:198-217` + `socks_perf.go:269` | ✅ |
| S4 日志补齐 | 网关+独享实例 call_log 聚合（Source 标注）、responses/claude `recordCall`、`pool_probe_target` | `core/manager/calllog.go` + `responses.go:676` + `claude.go:47` | ✅ |
| E1 上游代理 | `upstream_proxy` 配置 + 设置页「代理出口」区块 + scheme 归一化 | `types_config.go:14` + `opencodecfg.go:35` + `SettingsPage.tsx:218` | ✅ |

**测试覆盖**：`race_test.go`、`poolquality_test.go`、`calllog_test.go` 等存在且全绿；
`go test -race` 关键包通过。未发现实现与计划的实质偏差。

---

## 二、⚠️ 未完成项：UI 改进需求（REQ 系列）—— 全部未动

**同事的"完成"范围 = EXECUTION-PLAN（S/E 系列）。以下 REQ 文档无人实施：**

| 需求文档 | 任务 | 现状（代码核实） | 状态 |
|---|---|---|---|
| `REQ-POOL-FILTER.md` | 实例池页状态筛选（all/running/stopped） | `PoolPage.tsx` **无 filter state**，仅有 releaseMode | ❌ 未做 |
| `REQ-UI-IMPROVEMENTS.md` #2 | 节点池扫描停止后 poll 自杀 bug | `NodesPage.tsx:185` **`return` 原样保留**——bug 未修 | ❌ 未做 |
| `REQ-UI-IMPROVEMENTS.md` #3 | `ui_poll_interval_sec` 配置 + 独享页轮询 + 折叠面板 | **全仓无此配置项**；InstancesPage 无 setInterval | ❌ 未做 |
| `REQ-UI-IMPROVEMENTS.md` #4 | 日志页分页（每页 100） | `LogsPage.tsx` **无分页逻辑** | ❌ 未做 |
| `REQ-UI-IMPROVEMENTS.md` #5 | 统计页纯 CSS 迷你图 | StatsPage 无图表 | ❌ 未做 |

**#2 是缺陷（bug）而非改进**：`NodesPage.tsx:183-189` 停止扫描后 `return` 仍直接退出
poll 循环，后续扫描进度条永远 0。复现步骤：勾 100 节点 → 扫描 → 停止 → 再勾选扫描 →
进度条 0 无迹象。**此 bug 需尽快修**（30 分钟小活）。

---

## 三、验收结论与建议

1. **S/E 系列验收通过**——可视为 EXECUTION-PLAN 交付完成；验收表 S1~S5/E1 行可打 ✅
   （端到端项仍标注"需部署机/用户"待真机确认：429 触发、20 并发、Clash 出口、CI 产物）。
2. **REQ 系列未实施**——需明确派给同事（或确认是否已被优先级排后）。
3. **优先级建议**：
   - 🔴 先修 `REQ-UI-IMPROVEMENTS #2`（poll bug，缺陷）
   - 🟡 再上 `REQ-POOL-FILTER`（状态筛选，需求已定稿）
   - 🟢 `#3/#4/#5`（体验改进）随排期
4. **建议动作**：把 REQ 系列作为"第二阶段"派工单交给同事；或在 EXECUTION-PLAN 追加
   阶段（如 U 系列：U1 poll bug / U2 状态筛选 / U3 轮询配置 / U4 日志分页 / U5 统计迷你图）。

---

## 四、附：S/E 实现抽查明细（验收证据）

### S1 竞速预算（`vendors/opencode/chat.go` raceDo）
- 主循环 `select { case o := <-results / case <-timer.C: cancel(); return 非nil错误 }` ✅
- 流式候选首字节预算 `fbTimer`（同 raceBudget）✅
- 冷启动：`poolquality.go:271` 空窗口标记 unknown 计 100 不参与竞速；`socks_perf.go:541` 候选过滤 unknown ✅

### S5 自适应竞速
- `raceCopies()`：pressure = active/healthy，`<0.5→上限 / 0.5-1.0→2 / ≥1.0→1` ✅
- `proxyInFlight` map + atomic 计数（RaceStarted 时 +1）✅
- 高压力（≥2.0）跳过质量排序纯随机摊开 ✅
- `pool_race_copies` 语义已改为上限 ✅

### S2 429 感知
- `last429.Store` 冷却内 `!v.inRateLimitCooldown()` 跳竞速 ✅
- `rateLimitBackoff(retry429Count)` 指数退避 ✅
- 429 最终失败中文文案 + status 429 透传 ✅

### S3 分类型恢复
- 链路类：`badUntil` 到期半开放行 1 探测，成功清/失败重坏 ✅
- 账号类（401/402/429）：`badUntil` 零值永久禁用 ✅
- halfopen 节点候选不足时进竞速兜底 ✅
- `PoolQualitySummary.Unknown` 字段 ✅

### S4 日志补齐
- `calllog.go` 聚合读取（网关 + 各 Running 独享实例，Source=实例名，tail 合并）✅
- `responses.go:676` / `claude.go:47` 构造 CallRecord + recordCall ✅
- `pool_probe_target` 配置 ✅

### E1 上游代理
- `upstream_proxy` 配置项 + `upstreamProxyAddr` scheme 归一化（剥 socks5:// http://）✅
- `opencodecfg.go:61` 非空时 active_socks5 指向代理 ✅
- 设置页「代理出口」区块（SettingsPage.tsx:218）✅

---

## 五、端到端验收清单（待用户/部署机执行）

- [ ] S1：挂起一个节点 → grok 首次请求 ≤10s 得到响应/明确错误
- [ ] S5：单窗口竞速副本=2；开 20 并发自动降单发、in-flight 分散
- [ ] S2：高频触发 429 → 日志请求量不再双倍；文案"额度用尽"
- [ ] S3：链路类故障 ×3 进坏池 → 等待复位自动恢复；429 ×3 → 不自动恢复
- [ ] S4：独享实例/Responses/Claude 日志页可见
- [ ] E1：填 Clash 代理 → 重启实例 → 请求 200；批量扫描不再 0/N 全挂
