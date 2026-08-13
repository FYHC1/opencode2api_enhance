# 开发计划：S 系列——请求首效与质量系统优化（竞速预算 / 429 感知 / 冷启动 / 质量自愈 / 日志补齐）

> **本计划是「首次请求慢 + grok 无故重试 + 429 限流放大」问题的专项整改依据**。
> 遵循仓库 AGENTS.md 纪律：每阶段 = 功能开发 + 测试 + 验证；**验证不通过的部分下放到下一阶段开头声明并优先处理**。
> 开工前必读 `docs/AI-TESTING-GUIDE.md`（端口与环境隔离红线）与 `docs/ARCHITECTURE-V2-PLAN.md`（架构约束）。
>
> - 提出日期：2026-08-14（用户反馈：grok CLI 首次请求慢、无故显示重试、测试节点全通但请求 429）
> - 系列定位：与 ROADMAP 的 Q（模型矩阵）/R（供应商配置化）并列的新迭代方向，**S = 请求首效（Request First-Effort）**
> - 依赖现状：P 系列（性能模式）已合入 main；本系列在其上做体验与稳定性修正

---

## 〇、背景与根因（代码级已核实的证据）

### 0.1 用户症状

1. 3 个节点全部「测试」通过（freeCompletion 直连实例端口成功），但 grok CLI 首次请求**长时间无响应/显示重试**；
2. 统一网关日志出现**连续 2 分钟 429**（`FreeUsageLimitError: Rate limit exceeded`），一秒内最多 4 条；
3. 过一段时间（约 2~3 分钟）自行恢复，疑似限流窗口滑动。

### 0.2 根因链（全部代码实锤）

| # | 根因 | 位置 | 机制 |
|---|---|---|---|
| R1 | **竞速无整体预算（快速失败缺失）** | `vendors/opencode/chat.go` `raceDo` 主循环 `for { o := <-results }` | 无 `select + ctx.Done()`；候选挂起时请求无限悬着 → grok 客户端自行超时重试 |
| R2 | **流式候选客户端无超时** | `models_source.go` `CandidateClients`：`sc.Timeout = 0` | 流式候选挂起永不返回，raceDo 死等；且竞速卡死在 vendor 层，`streamWithResume`（TTFT 续写兜底）在 `callOpenCodeAPIStream` 返回后才接手——超时保护被架空 |
| R3 | **429 被当可重试 → 重试循环放大限流** | `vendors/opencode/opencode.go` `ErrSemantics().Retryable` 含 429；`chat.go` `isRetryable` | 429 → 重试 → 竞速再扇出 2 份 → 上游限流更狠（日志一秒 4 条 429 即此） |
| R4 | **冷启动质量分未知 → 全乐观 100 → 全部进竞速候选** | `poolquality.go` `computeQuality` 空窗口返回 100/healthy；`socks_perf.go` `raceCandidates` | 探活未跑第一轮时 3 节点全进候选 → 首次请求 2 份并行炸上游，429 概率大增 |
| R5 | **坏池节点无自动恢复** | `socks.go` `markSocks5Result` + `pickHealthyProxy`/`pickWeightedProxy` | 连续 3 次 429/401 进坏池（`badReason`）后直接 `continue`，**无恢复路径**，只能重启进程清内存 |
| R6 | **熔断/半开节点在竞速路径饿死** | `socks_perf.go` `raceCandidates`：`breakerState != "closed"` 直接跳过 | 半开放行只写在单发路径 `pickWeightedProxy`；healthy 候选够用时熔断节点永无试探机会 |
| R7 | **调用日志缺口** | `chat_handler.go`（唯一 `recordCall` 处）、`responses.go`/`claude.go` 无记录 | ① 独享实例请求不进日志页（只读 `_unified-gateway/call_log.jsonl`）② Responses/Claude 协议不写日志 |
| R8 | **429/超时无可见报错** | 网关错误透传裸状态码 | 用户只看到「重试」，不知道是额度用尽还是节点故障 |

### 0.3 目标一句话

> **首次请求快、重试有明确原因、限流不放大、坏节点能自愈、日志全链路可见。**

---

## 一、阶段计划

> 每阶段 = 功能开发（文件级）+ 测试 + 验证；遗留声明规则同 P 系列。

---

### 阶段 S1：竞速整体预算 + 快速失败 + 失败接入续写兜底（治 R1/R2/R4-c）

**目标**：竞速在任何情况下有界（bound），grok 重试前必有答案；冷启动不双倍炸上游。

#### 功能开发

1. **`raceDo` 主循环加整体预算**（`vendors/opencode/chat.go`）：
   - 现有 `for { o := <-results }` 改为：
     ```go
     budget := v.raceBudget() // 默认 10s，可配 race_budget_ms
     timer := time.NewTimer(budget)
     for {
         select {
         case o := <-results:
             // 现有成功/全败分支不变
         case <-timer.C:
             cancel() // 预算到期：终止所有候选，返回超时结果让上层走续写/报错
             return nil, "", fmt.Errorf("race budget exceeded")
         }
     }
     ```
   - 超时结果必须**非 nil 错误**，触发上层既有重试/续写路径，不产生悬挂请求。
2. **流式候选保留有限等待**（`models_source.go` `CandidateClients`）：
   - `sc.Timeout = 0` 改为**首字节等待预算**（同 `race_budget_ms`）；注意区分：**等待期**有界，**赢家流本身不设限**（已在锁流后用原 client 继续读，不受影响）。
3. **冷启动不竞速**（`socks_perf.go` `raceCandidates` + `poolquality.go`）：
   - 新增质量记录状态 `unknown`（无探活样本）：`poolQualityOf` 返回 known=false 的节点，**首轮竞速不纳入候选**（走单发），探活跑过 ≥1 轮后自动启用竞速。
   - `raceCandidates` 入参增加过滤：候选必须 `known=true` 才参与竞速；全部 unknown → 返回 nil（退化单发）。
4. **竞速失败接入续写兜底**（`vendors/opencode/chat.go` `raceDo` 调用处）：
   - 竞速返回错误/超时后，正常进入现有 `retryCount <= maxRetries` 循环的单发路径（`tr.Client`），确保 `streamWithResume` 能接手（该链在 `chat_handler.go` 已接线，只需竞速不再提前吞掉错误）。

**配置项**（`types_config.go` + `config.go` + `opencodecfg.go` 透传）：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `race_budget_ms` | int | 10000 | 竞速整体预算（0 = 用默认 10s） |

#### 测试
- `vendor/opencode/race_test.go` 扩展：
  - 新增挂起候选（RoundTrip 阻塞不返回）→ 断言预算到期返回错误、`cancel` 被调用、无悬挂 goroutine（用 `-race` 跑）
  - 快候选在预算内返回 → 断言正常赢（不回归）
  - 预算=0 回退 10s 默认
- `socks_perf_test.go`：unknown 节点不参与竞速候选；全 unknown → nil；探活一轮后进入候选
- 流式候选：等待期超时返回错误；锁流后长流不被截断（计时 5s 长流读完）
- `go test -count=1 ./...` 全绿 + `go test -race` 关键包

#### 验证
- 手动（隔离环境，AI-TESTING-GUIDE 三件套）：挂起一个节点（临时断 sing-box）→ grok 首次请求 **≤10s 得到响应/明确错误**，不再无限悬着。
- **验收**：竞速在任何情况下有界；unknown 冷启动不参与竞速；超时/失败后正常走续写链。

---

### 阶段 S2：429 感知——降级单发 + 指数退避 + 可见报错（治 R3/R8）

**目标**：限流时不再放大流量，重试退避，用户看到明确原因。

#### 功能开发

1. **429 时跳过竞速**（`vendors/opencode/chat.go` `call` 循环）：
   - 记录最近一次 429 时间戳；若距上次 429 < `rate_limit_cooldown_sec`（默认 30s），**本次请求直接走单发**（不调 `raceDo`），避免双倍打上游。
2. **指数退避**（`vendors/opencode/chat.go` 重试逻辑）：
   - 429 分支重试前 `time.Sleep(backoff)`：`backoff = min(base*2^n, cap)`，base=1s，cap=30s（可配 `rate_limit_backoff_base_ms`/`cap_ms`）。
3. **可见报错文案**（`vendors/opencode/chat.go` + `chat_handler.go` 透传）：
   - 429 最终失败时，错误体携带明确文案：`免费额度已用尽（Rate limit exceeded），请稍后重试`（保持原 status=429 透传，客户端可识别）。
   - 超时/续写切换事件在调用日志 Events 中已存在（`switch`），前端日志页可显示；错误文案对齐。

**配置项**：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `rate_limit_cooldown_sec` | int | 30 | 429 后跳过竞速的冷却 |
| `rate_limit_backoff_base_ms` | int | 1000 | 429 重试退避基数 |
| `rate_limit_backoff_cap_ms` | int | 30000 | 429 重试退避上限 |

#### 测试
- 单测（fake transport）：第一次 429 → 第二次请求验证**未走 raceDo**（`CandidateClients` 未被调用）；退避序列 1s/2s/4s…30s cap；429 错误文案含"额度"
- 配置解析默认值/非法回退
- 回归：非 429 场景竞速行为不变

#### 验证
- 手动：连续高频请求触发 429 → 观察网关日志请求量**不再双倍**（只有单发）；错误体文案正确。
- **验收**：429 后竞速自动降级；重试有退避；用户看到"额度用尽"而非裸重试。

---

### 阶段 S3：质量系统自愈——坏池恢复 + 半开竞速 + unknown 等级（治 R5/R6）

**目标**：坏池/熔断节点有自动恢复路径，全池故障能自愈。

#### 阶段开头：S2 遗留声明
- （S2 验证不通过项在此声明并优先处理。）

#### 功能开发

1. **坏池自动恢复**（`socks.go`）：
   - `badReason` 节点增加**过期机制**：`state.badUntil = now + bad_pool_reset_sec`（默认 300s）；到期后自动清 `badReason` 并放行 1 个探测请求（复用半开放行语义）。
   - `pickHealthyProxy`/`pickWeightedProxy` 中：坏池节点 `now >= badUntil` → 视为半开，放行 1 个。
   - 恢复后首个请求成功 → 完全清状态；失败 → 重新进坏池（重置计时）。
2. **半开节点进竞速**（`socks_perf.go` `raceCandidates`）：
   - 候选不足 `n` 时，放行 1 个 `half_open`（open 到期未消费探针的）节点进候选兜底——恢复探测不饿死。
3. **unknown 等级正式化**（`poolquality.go`）：
   - `PoolQualitySummary` 增加 `unknown` 计数；UI 可显示"探测中"状态（S4 前端一并做）。

**配置项**：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `bad_pool_reset_sec` | int | 300 | 坏池自动复位时间 |

#### 测试
- `socks_test.go`：坏池节点到期放行 1 个探测 → 成功清 badReason / 失败重新坏池；重置计时
- `socks_perf_test.go`：候选不足时 half_open 节点被选中；候选充足时仍不选（不回归）
- `poolquality_test.go`：unknown 计数；空窗口状态
- 全绿 + `-race`

#### 验证
- 手动：制造 401/429 三次 → 节点进坏池 → 不手动干预，等待 `bad_pool_reset_sec` → 节点自动试探 → 恢复（上游解除限流后）。
- **验收**：坏池/熔断节点均能自动恢复；无重启依赖；全池故障可自愈。

---

### 阶段 S4：调用日志补齐 + 前端提示优化 + 探活目标可配（治 R7/R8 补充）

**目标**：全链路日志可见；429/超时在 UI 有明确提示；探活不加重限流。

#### 阶段开头：S3 遗留声明
- （S3 验证不通过项在此声明并优先处理。）

#### 功能开发

1. **日志补齐**：
   - ① 独享实例日志：`core/manager/calllog.go` 的 `CallLogPath` 扩展——日志页聚合读取：统一网关日志 + 各 Running 独享实例 cwd 下 `call_log.jsonl`（按实例名标注来源），合并排序返回。
   - ② Responses/Claude 协议记录：`responses.go` `responsesHandler`、`claude.go` `claudeMessagesHandler` 补 `CallRecord` 构造 + `recordCall`（对齐 `chat_handler.go` 的三态：成功/失败/切换；含 `RouteMode`/`Nodes`/`Events`）。
2. **前端提示**（`src/pages/LogsPage.tsx` + 状态徽标）：
   - 429 错误行显示"额度用尽"标签（解析 `err_msg`/body 关键字）；超时/切换事件颜色区分。
3. **探活目标可配**（`poolquality.go` `probeTargetURL` + 配置）：
   - `pool_probe_target` 配置项（默认 `""` = 现状自动拼接）；文档提示探活消耗免费额度，可改轻量目标或调低频率。

**配置项**：
| 配置键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `pool_probe_target` | string | "" | 探活目标 URL（空=自动） |

#### 测试
- `calllog_test.go`：多目录聚合读取（网关 + 独享实例）、按时间合并排序、实例名标注
- `responses_test.go`/`claude` 测试：新增 handler 走查断言 `CallLogRecord` 落盘（httptest + 临时目录）
- `poolquality_test.go`：`pool_probe_target` 覆盖默认拼接
- 前端：`npm run build` + 日志页渲染走查
- 全绿

#### 验证
- 手动：注册独享实例发请求 → 日志页可见（含来源标注）；用 grok 走 `/v1/responses` 发请求 → 日志页可见；制造 429 → 日志行带"额度用尽"标签。
- **验收**：三种协议 + 独享/网关全链路日志可见；429 提示明确；探活目标可配。

---

## 二、验收计划表

| # | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 / 备注 |
|---|---|---|---|---|---|
| 1 | S1 | 竞速有界：预算超时返回 + 候选超时 + 冷启动不竞速 + 续写兜底 | ⬜ | | `race_test` 挂起候选用例 + 手动挂节点走查 |
| 2 | S2 | 429 感知：降级单发 + 指数退避 + 可见文案 | ⬜ | | 连续高频触发 429，网关日志请求量减半 |
| 3 | S3 | 坏池/熔断自动恢复 + 半开竞速 + unknown 等级 | ⬜ | | 制造 401/429 三次，等待复位自动恢复 |
| 4 | S4 | 日志全链路 + 429 标签 + 探活目标可配 | ⬜ | | 独享/Responses/Claude 日志可见 |

## 三、风险与备选

- **竞速预算过短误伤正常慢请求**：默认 10s 已覆盖绝大多数 TTFT；超时走续写兜底而非失败，影响可控；预算可配。
- **429 降级单发后无并发加速**：额度受限时的正确行为是"少发"，单发+退避是主动降速，符合上游风控语义。
- **坏池 5 分钟复位若上游持续限流**：复位试探失败会重新进坏池，不会反复打上游（每次仅 1 个探测）。
- **日志聚合读多文件**：独享实例多时（20+）每次读全部文件有 IO 成本——限制只读最近 N 行（如各文件 tail 5000），合并排序。
- **行为回归**：S1~S3 全部默认值保底兼容（0/空 = 现行为）；`go test` + `-race` 双跑防回归。

## 四、开工清单（S1 第一步）

1. `go test -count=1 ./...` 记录基线全绿。
2. 读 `vendors/opencode/chat.go`（raceDo/call 循环）、`models_source.go`（CandidateClients）、`socks_perf.go`（raceCandidates）、`socks.go`（坏池/冷却）。
3. `types_config.go` 加 `RaceBudgetMS`，`config.go` 解析，`opencodecfg.go` 透传（网关子进程生效）。
4. 按 S1 → S2 → S3 → S4 推进，一阶段一提交。