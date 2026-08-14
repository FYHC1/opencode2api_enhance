# 开发总方案：MASTER-PLAN（全部系列的统一事实来源）

> **本文档是仓库所有开发系列的统一事实来源**，整合并取代以下历史文档：
> `PERFORMANCE-PLATFORM-PLAN.md`（P/D 系列）、`PLAN-S-REQUEST-OPTIMIZATION.md`（S 系列）、
> `ROADMAP.md`（M/Q/R 系列）、`REVIEW-PD-DIFF.md`（复核清单）。
> 历史文档已删除（内容并入本文档对应章节；git 历史可追溯）。
>
> - 制定：2026-08-12（海鸥）／增补 S 系列：2026-08-14（tianzhuzhu）／统一合并：2026-08-14（海鸥）
> - 遵循 AGENTS.md 纪律：每阶段 = **功能开发 + 测试 + 验证**；验证不通过项下放下一阶段开头声明。
> - 开工前必读 `docs/AI-TESTING-GUIDE.md`（端口/进程红线）与 `docs/ARCHITECTURE-V2-PLAN.md`（架构约束）。

---

## 〇、系列总览（状态矩阵）

| 系列 | 主题 | 状态 | 关键提交 |
|---|---|---|---|
| **P0** | 探针进程泄漏修复 | ✅ 完成 | `b2eee5c` |
| **P1** | 链路级主动探活 + 滑动窗口质量评分 | ✅ 完成 | `d594468` |
| **P2** | 质量加权路由 + 熔断/半开自动恢复 | ✅ 完成 | `b848ce4` |
| **P2b** | 请求级竞速（并行扇出首胜） | ✅ 完成 | `64d2a20` |
| **P3** | UI 质量分可视化 + 性能模式配置 | ✅ 完成 | `2a5403b` + `820576c` |
| **D1** | 退出二次确认（释放/不释放/取消） | ✅ 完成 | `b0bf9ba` |
| **D2** | 一键测试三分类（跳过不误报） | ✅ 完成 | `782cad3` |
| **D3** | 并发设置抽离（设置页分组） | ✅ 完成 | `820576c` |
| **M1** | 三平台编译验证 + 系统调用替换（lsof/.desktop） | ✅ 完成 | `48912e3` |
| **M2** | 内嵌二进制按平台（embed.rs） | ✅ 完成 | `92dca47` |
| **M3/M4** | 打包配置 + CI 三平台产物 | 🔶 配置完成，真机待 CI | `2f2b575` |
| **M5** | Docker 支持（headless 容器化） | 🔶 代码完成，待 Docker 验证 | `34fda77` |
| **M6** | macOS 专项收尾 | 🔶 文档/代码就绪，真机待验 | `44a0de3` |
| **S1** | 竞速整体预算 + 快速失败 + 冷启动不竞速 | ⬜ 未开工 | — |
| **S2** | 429 感知：降级单发 + 指数退避 + 可见报错 | ⬜ 未开工 | — |
| **S3** | 质量自愈：坏池恢复 + 半开竞速 + unknown 等级 | ⬜ 未开工 | — |
| **S4** | 调用日志补齐 + 429 提示 + 探活目标可配 | ⬜ 未开工 | — |
| **S5** | **自适应竞速：压力系数动态副本 + in-flight 均衡**（2026-08-14 新增） | ⬜ 未开工 | — |
| **Q 系列** | 模型矩阵 + auto 路由 | 🕓 延后 | — |
| **R 系列** | 供应商配置化 + 插件 | 🕓 延后 | — |

> 依赖关系：**S5 依赖 S1**（竞速预算机制）→ S5 排在 S1 之后；S2/S3 可与 S5 并行。
> 用户核心场景（2026-08-14）：节点池大 + 单用户窗口（竞速全开）↔ 节点池小 + 多用户窗口（自动降单发）。**S5 为此而生。**

---

## 一、已完成系列（P/D/M1-2）——摘要存档

> 细节见 git 历史，此处仅保留结论，避免重复文档。

### P0 探针进程泄漏修复
- 根因：`probe_node.go` `probeNode()` 仅失败路径 Kill 探针进程，成功路径直接 return → 每次扫描每节点残留一对进程（8-11 单日残留 90+）。
- 修复：spawn 成功后 `defer` 清理，所有返回路径必杀 `ocPID`+`sbPID`。
- 遗留：用户机器存量残留进程清理（`feature/orphan-cleanup` 已实现一键清理，**须用户确认后执行**）。

### P1 链路级主动探活 + 滑动窗口评分
- `core/manager/poolquality.go`：周期（默认 45s）经实例 sing-box SOCKS 出口发真实 HTTP 探测，滑动窗口（默认 10min）算成功率/延迟/连续失败 → 质量分 0~100 与等级（healthy≥80 / degraded 60-79 / flaky 30-59 / down<30）。
- 配置：`pool_probe_enabled` / `pool_probe_interval_sec`(45) / `pool_probe_timeout_sec`(3) / `pool_probe_concurrency`(4) / `pool_quality_window_min`(10)。
- API：`GET /api/admin/pool/quality`、`POST /api/admin/pool/quality/probe`。

### P2 质量加权路由 + 熔断/半开
- `socks_perf.go` `pickWeightedProxy`：healthy 优先 / degraded 降权 / flaky 跳过 / down 剔除；实测量反馈与探活分 7:3 融合。
- 熔断状态机：连续失败 ≥`pool_breaker_threshold`(3) → open；`pool_halfopen_interval_sec`(60) 后 half-open 放行 1 探测，成功回归。
- 配置：`pool_performance_mode`(true) 总开关。

### P2b 请求级竞速
- `vendors/opencode/chat.go` `raceDo` + `contract.Racer`：一次请求并行扇出至多 `pool_race_copies`(2) 个候选，非流式首个完整 2xx 胜出、流式首个 chunk 锁流胜出，其余 `cancel()`。
- 候选选择 `socks_perf.go` `raceCandidates`：坏池/冷却/熔断/down/flaky 剔除，healthy 优先、分高在前。

### P3 UI + D1/D2/D3
- 池页质量分徽标 + 性能模式开关；设置页「并发设置」分组。
- D1 退出三选弹窗 + 4 并发释放 + 全局进度面板；D2 测试三分类（跳过不误报）；D3 并发参数可配。

### M1/M2 多端基础
- `netstat_other.go` lsof 端口清理（纯函数解析，单测注入假行）；`autostart.go` Linux `.desktop` / macOS plist。
- `embed.rs` 按 `cfg!(target_os)` 选平台二进制。

---

## 二、M 系列收尾（M3~M6，真机/CI 验证）

### M3/M4 打包配置 + CI 三平台产物（🔶 配置完成，待 CI 触发）
- `tauri.conf.json` 已配 bundle.linux（deb/rpm/AppImage）+ bundle.macOS（dmg）。
- GitHub Actions 三平台矩阵：windows-latest / ubuntu-latest / macos-latest，产物作 release 附件。
- 验证：提交信息含 `CI` → 三平台各产 1 个完整包；日常提交不触发。

### M5 Docker 支持（🔶 代码完成，待 Docker 环境验证）
- `Dockerfile` 多阶段（构建 Go core + dist → alpine 精简镜像）；`docker-compose.yml` 示例；DEPLOYMENT.md 补 Docker 小节。
- 验证：`docker build` 通过，一条 `docker run` 起完整服务，数据卷重启不丢。

### M6 macOS 专项收尾（🔶 文档/代码就绪，真机待验）
- 签名/公证策略（Gatekeeper 提示处理）、lsof 端口清理确认、LaunchAgent 自启、托盘行为。
- 验收：macOS 交付 dmg，六页 UI + 实例生命周期 + 扫描 + 网关全通。

---

## 三、S 系列：请求首效与质量系统优化（未开工）

> 背景（2026-08-14 用户反馈）：grok CLI 首次请求慢、无故重试、测试节点全通但请求 429。
> 根因链全部代码级实锤，见各阶段。

### 阶段 S1：竞速整体预算 + 快速失败 + 冷启动不竞速（治 R1/R2/R4）

**目标**：竞速在任何情况下有界（bound），grok 重试前必有答案；冷启动不双倍炸上游。

**根因**：
- R1：`raceDo` 主循环 `for { o := <-results }` 无 `select + ctx.Done()` → 候选挂起时请求无限悬着。
- R2：`models_source.go` `CandidateClients` `sc.Timeout = 0` → 流式候选挂起永不返回，竞速死等。
- R4：冷启动质量分空窗口返回 100/healthy → 全节点进竞速候选 → 首次请求 2 份并行炸上游。

**功能开发**：
1. `raceDo` 主循环加整体预算：`budget := v.raceBudget()`（默认 10s，可配 `race_budget_ms`）；`select { case o := <-results / case <-timer.C: cancel(); return 超时错误 }`——超时错误必须非 nil，触发上层重试/续写。
2. 流式候选 `sc.Timeout = 0` 改首字节等待预算（同 `race_budget_ms`）；赢家流本身不设限。
3. 冷启动不竞速：质量记录新增 `unknown` 态（无探活样本），`raceCandidates` 候选必须 `known=true`；全 unknown → 返回 nil 退化单发。
4. 竞速失败接入续写兜底：错误正常进入 `retryCount` 循环单发路径，`streamWithResume` 可接手。

**配置**：`race_budget_ms`(10000)。
**测试**：挂起候选 → 预算到期返回错误 + cancel 调用 + 无悬挂 goroutine（`-race`）；快候选不回归；unknown 不进候选；流式等待期超时返回、锁流后长流不截断。
**验证**：挂起一个节点 → grok 首次请求 ≤10s 得到响应/明确错误，不无限悬着。

### 阶段 S2：429 感知——降级单发 + 指数退避 + 可见报错（治 R3/R8）

**根因**：R3 429 被当可重试 → 重试循环放大限流（429 → 重试 → 竞速再扇出 2 份）；R8 429/超时无可见报错。

**功能开发**：
1. 429 后冷却内跳过竞速：记录最近 429 时间戳，距上次 < `rate_limit_cooldown_sec`(30) → 直接单发。
2. 指数退避：429 重试前 `sleep(min(base*2^n, cap))`，base=1s cap=30s（`rate_limit_backoff_base_ms`/`cap_ms`）。
3. 可见报错：429 最终失败携带文案「免费额度已用尽（Rate limit exceeded），请稍后重试」，保持 status=429 透传。

**配置**：`rate_limit_cooldown_sec`(30) / `rate_limit_backoff_base_ms`(1000) / `rate_limit_backoff_cap_ms`(30000)。
**测试**：首 429 → 二次未走 raceDo；退避序列 1/2/4…30 cap；文案含"额度"；非 429 回归不变。
**验证**：高频触发 429 → 网关日志请求量不再双倍；用户看到"额度用尽"。

### 阶段 S3：质量自愈——坏池恢复 + 半开竞速 + unknown 等级（治 R5/R6）

**根因**：R5 坏池节点无恢复路径（只能重启清内存）；R6 熔断节点在竞速路径饿死（半开放行只在单发路径）。

**功能开发**：
1. 坏池自动恢复：`badReason` 加过期 `badUntil = now + bad_pool_reset_sec`(300)；到期放行 1 探测，成功清状态 / 失败重新坏池。
2. 半开进竞速：`raceCandidates` 候选不足 n 时放行 1 个 half_open 节点兜底。
3. unknown 等级正式化：`PoolQualitySummary` 增 unknown 计数，UI 显示「探测中」。

**配置**：`bad_pool_reset_sec`(300)。
**测试**：坏池到期放行→成功清/失败重坏；候选不足时 half_open 被选、充足时不选；unknown 计数。
**验证**：制造 401/429 三次 → 进坏池 → 不干预等待复位 → 自动试探恢复。

### 阶段 S4：调用日志补齐 + 前端提示 + 探活目标可配（治 R7/R8 补充）

**根因**：R7 独享实例/Responses/Claude 协议不进日志页；R8 429/超时无 UI 提示。

**功能开发**：
1. 日志补齐：① 日志页聚合读取统一网关 + 各 Running 独享实例 `call_log.jsonl`（按实例名标注，合并排序，各文件 tail 5000）；② `responses.go`/`claude.go` 补 CallRecord 构造 + recordCall。
2. 前端：429 错误行显示「额度用尽」标签；超时/切换事件颜色区分。
3. 探活目标可配：`pool_probe_target`（默认 "" = 自动拼接）；文档提示探活消耗免费额度。

**配置**：`pool_probe_target`("")。
**测试**：calllog 多目录聚合/排序/标注；responses/claude handler 断言落盘；前端 build。
**验证**：独享实例请求日志可见；grok `/v1/responses` 日志可见；429 带标签。

---

## 四、S5 自适应竞速：压力系数动态副本 + in-flight 均衡（2026-08-14 海鸥新增）

> **用户核心需求**（原话整理）：
> 1. 实例节点池很多，但用户每次只开 1 个对话窗口 → 希望竞速全开，用闲节点换最快响应；
> 2. 用户变多，同时开多个窗口，但只有很少的实例节点 → 竞速反而制造流量聚集，希望自动降级为分散路由（等效 1.3.1）。
> **一句话**：竞速副本数跟着「节点压力」动态走，运营零干预。

### 4.1 为什么固定竞速副本不行

- 竞速的本质是「用 N 倍额度换最快响应」，前提是**池大、闲节点多**。
- 当 请求数 ≥ 健康节点数 时（如 8 节点 50 窗口），`raceCandidates` 每次都选 **top-N 最健康节点** → 50 请求 × 2 副本 = 100 路全挤 top 2 → 流量聚集（thundering herd）→ 429/超时 → 熔断收缩 → 更挤。
- 1.3.1 的 `pickHealthyProxy` 游标轮转天然分散到全部节点，才是容量紧张时的正确行为。

### 4.2 核心设计：压力系数驱动动态副本

```
pressure = 当前活跃请求数 / 当前健康节点数
```

| 压力区间 | 实际竞速副本 | 行为 |
|---|---|---|
| pressure < 0.5 | 配置上限（2~4） | **全速竞速**：多候选抢最快 |
| 0.5 ~ 1.0 | 2 | 温和竞速 |
| 1.0 ~ 2.0 | 1 | 退化单发 = 分散轮转（等效 1.3.1） |
| ≥ 2.0 | 1 | 单发 + **候选随机化**（负载均衡摊开） |

**场景对号**：20 节点 1 请求 → pressure=0.05 → 竞速全开 ✅；8 节点 50 请求 → pressure=6.25 → 单发分散 ✅。全程自动。

### 4.3 第二层：候选随机化 + least-in-flight（治流量聚集）

- 每节点 `inFlight[addr]` 计数（请求发出 +1、完成 -1）。
- `raceCandidates` 改造：从 healthy/degraded 池按 **in-flight 最少优先**，同 in-flight 再按质量分，附随机扰动——50 请求 × 2 副本自动摊到全部健康节点，不再压垮 top 2。

### 4.4 功能开发（文件级）

| 项 | 位置 | 内容 |
|---|---|---|
| 活跃请求计数 | `vendors/opencode/chat.go` 或网关入口 | 全局 `atomic.Int64`：进入 +1 / 完成 -1 |
| 每节点 in-flight | `socks_perf.go` | `map[addr]*atomic.Int64`，选路用 |
| 动态副本 | `raceCopies()` 改造 | `copies = clamp(raceCopies上限, 1, 按 pressure 分段)` |
| 候选均衡 | `raceCandidates` 改造 | in-flight 最少优先 > 质量分 > 随机扰动 |
| 配置 | `pool_race_copies` 语义改为**上限** | 实际副本由压力系数定；新增 `pool_race_pressure_*` 阈值（可配） |

**配置项**（`types_config.go` + `config.go` + `opencodecfg.go` 透传）：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `pool_race_copies` | int | 2 | **竞速副本上限**（1~4；1 = 关闭竞速） |
| `pool_race_pressure_high` | float | 1.0 | pressure ≥ 此值 → 单发 |
| `pool_race_pressure_low` | float | 0.5 | pressure < 此值 → 全速 |

### 4.5 测试

- 单测（fake transport + 假并发计数）：
  - 低压力（0.1）→ 副本=上限；中压力（0.7）→ 副本=2；高压力（1.5/3.0）→ 副本=1
  - in-flight 均衡：两个节点 in-flight 3/0 → 新请求选中 in-flight=0 的
  - 候选随机化不回归：质量分排序仍优先（同 in-flight 时）
  - 单出口/全 unknown → nil（不竞速，退化单发）
- 集成：模拟「20 节点 1 请求」与「8 节点 50 请求」两场景，断言副本数动态变化、流量分散（各节点 in-flight 方差小）。
- `go test -count=1 ./...` + `-race` 关键包全绿。

### 4.6 验证

- 手动（隔离环境）：单窗口时观察日志竞速副本=2；开 20 个并发请求后观察自动降单发、节点 in-flight 分散。
- **验收**：压力动态调节生效；高并发无流量聚集；429 触发率显著下降；与 S1 预算机制协同（预算到期 → 单发续写）。

### 4.7 与 S 系列协同

- **依赖 S1**：竞速预算先落地，S5 的动态副本才有"有界"保障。
- **互补 S2**：S2 治"429 后别重试"，S5 治"请求多时别扇出"——双保险。
- **顺序建议**：S1 → S5 → S2/S3（可并行）→ S4。

---

## 五、Q/R 系列（延后方向，占位）

### Q 系列：模型矩阵 + auto 路由（延后）
- 痛点：10 节点 × ~10 模型 = ~100 对话口子，选哪个？上下文不同（200k/1M）怎么规范？
- 方向：Q1 模型元数据（context_window）+ 上下文校验；Q2 矩阵探测与持久化（复用 P1 探活通道）；Q3 auto 路由（模型×节点矩阵 + 质量分 + 用户偏好）；Q4 UI（矩阵视图/偏好设置）。
- 承接点：`core/manager/poolquality.go`、`socks_perf.go`、`core/router`。

### R 系列：供应商配置化 + 插件（延后）
- 方向：R1 供应商契约（ID/模型清单/上游端点/鉴权/协议/流式）+ OpenAI/Anthropic 兼容配置供应商；R2 供应商管理 UI；R3 插件机制（子进程 RPC / go plugin / wasm 评估）。

---

## 六、验收计划表（完成一项勾一项）

| # | 阶段 | 验收项 | 状态 | 验证方式 |
|---|---|---|---|---|
| 1 | M3/M4 | CI 三平台各产 1 完整包，日常提交不触发 | 🔶 | CI 首跑 + 产物安装 |
| 2 | M5 | 一条 docker run 起完整服务，数据卷不丢 | 🔶 | Docker 环境验证 |
| 3 | M6 | macOS dmg 交付 + 六页全通 | 🔶 | mac 真机/CI |
| 4 | S1 | 竞速有界：预算超时返回 + 冷启动不竞速 + 续写兜底 | ⬜ | race_test 挂起用例 + 手动走查 |
| 5 | S5 | 压力动态副本 + in-flight 均衡，高并发无聚集 | ⬜ | 两场景单测 + 手动 20 并发 |
| 6 | S2 | 429 降级单发 + 退避 + 可见文案 | ⬜ | 高频触发 429，日志请求量减半 |
| 7 | S3 | 坏池/熔断自动恢复 + 半开竞速 + unknown | ⬜ | 401/429×3 等待复位自愈 |
| 8 | S4 | 日志全链路 + 429 标签 + 探活目标可配 | ⬜ | 独享/Responses/Claude 日志可见 |
| 9 | Q | 模型矩阵 + auto 路由 | 🕓 | 启动时再立正式计划 |
| 10 | R | 供应商配置化 + 插件 | 🕓 | 启动时再立正式计划 |

## 七、风险与备选

- **竞速预算过短误伤慢请求**：默认 10s 覆盖绝大多数 TTFT；超时走续写兜底而非失败；可配。
- **自适应副本抖动**：pressure 用滑动均值（如 5s 窗口）平滑，避免单请求进出导致副本频繁跳变。
- **429 降级后无并发加速**：额度受限时"少发"是正确的主动降速，符合上游风控语义。
- **坏池 5min 复位若上游持续限流**：复位试探失败重新进坏池，不反复打上游（每次仅 1 探测）。
- **行为回归**：S1~S5 默认值全部保底兼容（0/空 = 现行为）；`go test` + `-race` 双跑。
- **多端阻塞**：sing-box 平台二进制 CI 下载可能限流——备选手动放置 + 文档说明。

## 八、开工清单

1. `go test -count=1 ./...` 记录基线全绿。
2. 读 `vendors/opencode/chat.go`（raceDo/call 循环）、`models_source.go`（CandidateClients）、`socks_perf.go`（raceCandidates/pickWeightedProxy）、`socks.go`（坏池/冷却）。
3. `types_config.go` 加 `RaceBudgetMS` + S5 压力配置；`config.go` 解析；`opencodecfg.go` 透传。
4. 按顺序推进：M 系列验证 → S1 → S5 → S2/S3 → S4。
