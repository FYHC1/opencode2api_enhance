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
> 5. **环境红线**：任何真实服务启动前按 `docs/AI-TESTING-GUIDE.md` §3/§5 做端口与进程检查 + 三件套隔离；禁止 kill 非自己启动的进程。

---

## 阶段顺序总览（依赖驱动）

```
M3~M6（验证/CI，可随时穿插）→ S1（竞速预算，S5 的依赖）→ S5（自适应竞速，用户核心需求）
→ S2（429 感知，可与 S3 并行）→ S3（质量自愈）→ S4（日志补齐）→ E1（上游代理出口）
```

> 依赖说明：**S5 必须排在 S1 之后**（S5 动态副本需要 S1 的预算机制作安全垫底）。
> S2/S3 无相互依赖，可并行；E1 独立，任何空闲人手可先做。

---

## 阶段编号与当前状态

| 执行序 | 阶段 | 名称 | 状态 |
|---|---|---|---|
| 1 | M3~M6 | 多端收尾验证（CI 触发/真机） | 🔶 配置完成，待验证 |
| 2 | **S1** | 竞速整体预算 + 快速失败 + 冷启动不竞速 | ⬜ 未开工 |
| 3 | **S5** | 自适应竞速：压力系数动态副本 + in-flight 均衡 | ⬜ 未开工 |
| 4 | **S2** | 429 感知：降级单发 + 指数退避 + 可见报错 | ⬜ 未开工 |
| 5 | **S3** | 质量自愈：坏池恢复 + 半开竞速 + unknown 等级 | ⬜ 未开工 |
| 6 | **S4** | 调用日志补齐 + 429 提示 + 探活目标可配 | ⬜ 未开工 |
| 7 | **E1** | 上游代理出口（socks5 直连 Clash，MVP） | ⬜ 未开工 |

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
- 手动（隔离环境）：挂起一个节点（临时断 sing-box）→ grok 首次请求 ≤10s 得到响应/明确错误，不无限悬着。
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
- 手动（隔离环境）：单窗口看日志竞速副本=2；开 20 并发后自动降单发、节点 in-flight 分散。
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
- 手动：连续高频请求触发 429 → 网关日志请求量不再双倍（单发）；错误体文案正确。
- **验收**：429 后竞速自动降级；重试有退避；用户看到"额度用尽"。

---

## 阶段 S3：质量自愈——坏池恢复 + 半开竞速 + unknown 等级

**目标**：坏池/熔断节点自动恢复，全池故障能自愈。根因 R5/R6。

### 阶段开头：S2 遗留
- （S2 验证不通过项在此逐条声明并优先修复。）

### 功能开发
1. 坏池自动恢复：`socks.go` `badReason` 加过期 `badUntil = now + bad_pool_reset_sec`(300)；到期放行 1 探测，成功清状态 / 失败重新坏池（重置计时）。
2. 半开进竞速：`socks_perf.go` `raceCandidates` 候选不足 n 时放行 1 个 half_open 节点兜底。
3. unknown 等级正式化：`poolquality.go` `PoolQualitySummary` 增 unknown 计数；UI 可显示"探测中"（S4 前端一并做）。

**配置**：`bad_pool_reset_sec`(300)。

### 测试
- `socks_test.go`：坏池到期放行 → 成功清 badReason / 失败重新坏池；重置计时。
- `socks_perf_test.go`：候选不足时 half_open 被选；候选充足时不选（不回归）。
- `poolquality_test.go`：unknown 计数；空窗口状态。
- 全绿 + `-race`。

### 验证
- 手动：制造 401/429 三次 → 进坏池 → 不干预等待 `bad_pool_reset_sec` → 自动试探 → 恢复。
- **验收**：坏池/熔断均能自动恢复；无重启依赖。

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
- 手动：独享实例发请求 → 日志页可见（含来源标注）；grok 走 `/v1/responses` → 日志页可见；制造 429 → 日志带"额度用尽"标签。
- **验收**：三种协议 + 独享/网关全链路日志；429 提示明确；探活目标可配。

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
- 手动（隔离环境）：填 Clash 代理 → 重启实例 → 请求上游 200；批量扫描不再 0/N 全挂。
- **验收**：上游代理配置生效；扫描/实例流量走代理出口；未配置时行为与现状一致。

---

## 附：验收计划表（同事逐项打勾）

| 执行序 | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 |
|---|---|---|---|---|---|
| 1 | M3~M6 | CI 三平台产物 / docker run / macOS dmg | 🔶 | | CI 触发 + 真机 |
| 2 | S1 | 竞速有界 + 冷启动不竞速 + 续写兜底 | ⬜ | | race_test 挂起用例 + 手动 |
| 3 | S5 | 压力动态副本 + in-flight 均衡，无聚集 | ⬜ | | 两场景单测 + 手动 20 并发 |
| 4 | S2 | 429 降级单发 + 退避 + 可见文案 | ⬜ | | 高频触发，日志请求量减半 |
| 5 | S3 | 坏池/熔断自愈 + 半开竞速 + unknown | ⬜ | | 401/429×3 等待复位 |
| 6 | S4 | 日志全链路 + 429 标签 + 探活目标可配 | ⬜ | | 独享/Responses/Claude 可见 |
| 7 | E1 | 上游代理配置生效，扫描/实例走代理出口 | ⬜ | | 填 Clash 代理批量扫描不再全挂 |

> 维护规则：每完成一项把状态改 ✅ 并注明日期/验证命令。验证不通过 → 该行保持 ❌，
> 下一条目阶段的「阶段开头：上阶段遗留」中声明并优先修复。