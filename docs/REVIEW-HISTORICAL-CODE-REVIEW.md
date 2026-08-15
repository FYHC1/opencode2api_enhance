# 历史系列 Code Review 问题清单（2026-08-15）

> 审查方式：3 个独立只读审查子代理 + 海鸥复核；审查对象为 P/S/E 系列 Go 并发核心 + U 系列前端
> 基线：`main@0c63703`（本清单不含 V-review 修复 70aca85 已解决的问题）
> 结论：**unreviewed 问题 3 高 / 9 中 / 若干低**——并发骨架扎实，缺陷集中在数据竞争与状态机收尾

---

## 一、🔴 高优先（建议下一阶段先修）

### G1. 竞速路径硬编码 TierFree，付费层请求被扇出到 SOCKS5 代理池
- **位置**：`vendors/opencode/chat.go:436`（`raceDo` 调 `racer.CandidateClients(contract.TierFree, ...)`，无 tier 参数）+ `chat.go:219`（`call()` 无条件进竞速）+ `models_source.go:72-78`（`TierPaid` 返回 nil 的防护是死代码）
- **问题**：带 token（zen/go/auto 非 public）的付费请求被当 Free 层并行扇出到 2+ 个第三方 SOCKS5 出口，违反「付费层走直连」契约（contract.go:99-108）。副作用：付费流量 401/429 被 `markSocks5Result` 记为池节点失败 → 污染节点健康/触发冷却坏池 → 误伤免费流量；429 冷却（S2）被付费流量触发后免费流量跟着降级。
- **验证**：审查夹具实测 `CandidateClients` 收到 tier=0（Free），复现确认。
- **建议**：`raceDo` 签名加 `tier contract.Tier`，`call()` 对 `a.tier() == TierPaid` 跳过竞速走单发（与单发路径 `tr.Client(a.tier(), ...)` 一致）。

### G2. 停止扫描后全局任务栈 scan 任务冻结
- **状态**：✅ 已由 `70aca85` 修复（问题 1：NodesPage 加 `onRemove`，停止/非本页停止/idle-done 全路径收尾 scan 任务）。
- 本清单保留仅作记录。

### G3. U2 状态筛选下批量按钮计数与实际作用域不一致
- **位置**：`src/pages/PoolPage.tsx:94-111`（members 链）+ `385-397`（doReleaseAll）+ `449-453`（doAll）+ `731-739/748-774`（按钮计数 `poolSelected.size`）
- **问题**：`poolSelected` 不随筛选切换清理。选 3 个「运行中」→ 切到「已停止」→ 3 个被过滤隐藏但仍在 `poolSelected` → 按钮显示「批量释放（3）」但实际作用域 `members∩selected`=0，点击提示矛盾；批量启动会静默跳过隐藏成员。
- **建议**：作用域统一为勾选驱动（`poolSelected`），或按钮计数/禁用态改用 `members∩selected` 计算。

---

## 二、🟡 中优先

### G4. `Vendor.transport()` 懒初始化对 `v.tr` 无锁写（数据竞争）
- **位置**：`vendors/opencode/opencode.go:115-124`
- **问题**：读 nil→赋值非原子；竞速路径每个请求都调它（chat.go:213）。生产上首次访问常被 `sync.Once` 串行化，窗口窄，但并发首访 `-race` 必报（实测复现 opencode.go:117 vs 119，16 goroutine 流式压测）。
- **建议**：`New()` 直接初始化 `v.tr`，去掉懒初始化。

### G5. 配置热重载对普通全局变量无锁写，与请求路径无锁读竞争
- **位置**：写 `config.go:62-79`（applyConfig，持 configMu）；读 `socks_perf.go:490/354-355`、`socks.go:325/351/381`（请求路径无锁）
- **问题**：运行中改 config.json 时，`poolPerfMode`/`poolBreakerThreshold`/`badPoolResetSec`/竞速阈值等被并发读写 → 数据竞争（x86 落地撕裂概率低但违反 Go 内存模型，`-race` 必报），可能读到「半套配置」。
- **建议**：阈值/开关收敛进 `atomic`，或读侧 `configMu.RLock()` 拷贝。

### G6. 竞速落选候选的失败结果完全不可见（不冷却/不进坏池/不触发熔断）
- **位置**：`vendors/opencode/chat.go:432-572`（raceDo 无 Mark 调用）+ `576-585`（raceDrain 只 Close）
- **问题**：全败路径若 `firstFail` 为 err，`tr.Mark("",0,err)` 空操作——坏节点在竞速中持续「隐形失败」，不累计冷却/坏池/熔断；429 同理不触发 S2 冷却。
- **建议**：raceDo 对落选候选最终 outcome 调 `tr.Mark(addrs[i], status, err)`（赢家留给调用方）。

### G7. `loadPoolQualityCache` 节流字段无锁读写（数据竞争 + 重复读盘）
- **位置**：`socks_perf.go:69-71`（无锁读）/ `92-93`（锁内写）
- **问题**：`poolQualityStamp/poolQualityLoaded` 每次请求无锁读，mtime 变化或 >5s 时并发请求同时读写 → 数据竞争；且每 5s 全流量重复 `os.ReadFile`+`json.Unmarshal`。
- **建议**：节流判断放入 `poolQualityMu.RLock()`，或字段改 atomic。

### G8. `applyPoolResult` 熔断后每次失败都前移 `openUntil`，延迟半开恢复
- **位置**：`socks_perf.go:349-358`
- **问题**：熔断跳闸瞬间**已在途**的并发请求陆续失败 → 每次 `failures>=threshold` 都把 `openUntil` 重推到 now+interval → 半开探测被迟到失败不断延后（可达数十秒）。
- **建议**：仅在 close→open 跳变或半开探测失败（`probeUsed`）时重设 `openUntil`。

### G9. `pool_quality.json` 持久化无序列化，探活轮与手动触发并发写
- **位置**：`core/manager/poolquality.go:367-391`（load/save）+ `467-478`（后台循环）+ `524-529`（手动触发）
- **问题**：`RunPoolQualityOnce` 并发执行时 `os.WriteFile`（truncate+write 非原子）可产生撕裂 JSON → 读到空记录→整轮历史样本丢失→质量等级短暂回退（45s 后自愈）。
- **建议**：Manager 加探活互斥；临时文件 + `os.Rename` 原子落盘。

### G10. 任务卡 ✕ 关闭失效（busy 任务被 poll 秒速加回）
- **位置**：`src/App.tsx:214-218`（removeTask）+ NodesPage/PoolPage 持续 upsert
- **状态**：V-review 问题 6，本次修复按范围**后置未做**（需 dismissed 集合）。
- **建议**：App 维护 `dismissed` id 集合，upsert 前过滤已关闭且仍 busy 的任务。

### G11. RequestStop 同步阻塞（/scan/stop HTTP 数百 ms~秒）
- **位置**：`core/manager/probe.go:159`（interruptProbes 串行 kill + wg.Wait 在 HTTP 线程）
- **状态**：V-review 问题 7，后置未做（Windows PID 复用二次 kill 极小误杀风险，幂等 OK）。
- **建议**：中断改后台 goroutine，RequestStop 立即返回快照。

### G12. U3 保存轮询间隔后不即时生效
- **位置**：`src/pages/PoolPage.tsx:322-339`、`src/pages/InstancesPage.tsx:102-119`
- **问题**：`handleSaveUi` 只 configSet 不改生效值；toast 说「重载后生效」但表单值已漂移。保存 0（关轮询）后忘刷新，页面继续按旧间隔轮询。
- **建议**：保存成功后同步 `setUiPollSec(v)`，或关闭弹窗时表单回滚为生效值。

---

## 三、🟢 低（记录，可按批次清理）

| # | 位置 | 问题 |
|---|---|---|
| G13 | contract.go:127-139 | 文档与实现不符：仅 Racer 未实现 RaceTracker 的传输被静默禁用竞速（healthy=0→pressure=2→copies=1） |
| G14 | chat.go:436-441 | `CandidateClients` 返回 clients/addrs 等长的契约未约束，不等长会越界 panic |
| G15 | config.go:81-92 | 竞速配置（race_copies/budget/压力阈值）热重载不生效，需重启（applyConfig 不重建 Vendor.cfg） |
| G16 | chat.go:318-323 | `low>=high` 压力分段倒挂（1.2→1 而 1.4→上限），应在 applyConfig 钳制 |
| G17 | socks_perf.go:111-116/169-172 | poolFeedback/proxyInFlight map 无驱逐，节点移除后条目常驻（内存缓慢增长） |
| G18 | chat.go:589-612 | activeRequests 不含流式消费期，长流消费时压力系数低估 |
| G19 | socks_perf.go:59-72 | 质量缓存节流缺陷：mtime 未变且 >5s 时每请求读盘；mtime 倒退永不刷新 |
| G20 | socks_perf.go:199-210 | 压力分母含 degraded 但候选还排除冷却/坏池，分母偏大压力偏小 |
| G21 | poolquality.go:379/389 | load/save 损坏与 I/O 错误全部静默吞掉，无 slog |
| G22 | poolquality.go:469-486 | StartPoolQualityLoop/StartHealthLoop 无限 goroutine 无停止句柄 |
| G23 | socks.go:418-424 | socks5Client 非 RR 缓存写在 RLock 内，多并发读写竞争（值等价，危害极低） |
| G24 | opencodecfg.go:39-56 | upstream_proxy 非法/不支持值静默回退无日志；`//` 前缀与 port=0 病态输入通过校验（dial 必失败） |
| G25 | PoolPage.tsx:415-420 | 一键释放 1.2s 收尾 timer 无句柄，快速连续释放会误移除新任务 |
| G26 | LogsPage.tsx:236-240 | fetchLimitRef 对 call_log_max<=0 保留旧值而非回退 5000（后端校验使实际不可达） |
| G27 | LogsPage/StatsPage | 筛选日期过期后 value 悬空，列表被看不见的日期过滤为空需手动重选 |
| G28 | StatsPage.tsx:89-131 | 迷你条/趋势数据不随 5s 轮询刷新（仅手动），底部文案误导；catch 未清 dates |
| G29 | StatsPage.tsx:136-148 | 快速切按天日期请求竞态（本地后端 FIFO 风险低） |
| G30 | PoolPage.tsx:807-914 | 筛选空结果显示「暂无池成员」误导文案，未区分真为空 |
| G31 | InstancesPage/LogsPage/StatsPage | `load=useCallback(...,[toast])`，toast 每次渲染新函数 → poll 定时器重启+整量刷新（App showToast 未 memo） |

---

## 四、已核查无问题领域（审查结论）

- **raceDo 并发骨架**：results 通道容量=发送数无阻塞、drain 收尾不漏读、cancel 分支等待读 goroutine 退出、timer 全覆盖 Stop、in-flight 严格成对——无 goroutine 泄漏/阻塞发送/流拼接竞争（`-race` 多轮 + 16 并发流式压测通过）。
- **熔断/半开/坏池状态机**：closed→open→half-open 转换、半开放行与坏池探测配额单次消费均锁保护（`poolBreakerMu`/`socks5HealthMu`），无并发放行；`raceProbeFallback` 锁序无环。
- **滑动窗口边界**：窗口修剪、空窗口→unknown、等级判定（healthy/degraded/flaky/down）正确；损坏文件容错无 panic。
- **E1 配置**：scheme 剥离（含大写）、端口校验、非法值回退、未配置回归快照锁定——核心正确。
- **U4 分页 / U5 迷你图 / U6 日志聚合消费**：分页 clamp、轮询不跳页、memo 结构、占比除零守卫、0 值空态、source 标注映射——正确。
- **U1 轮询骨架 / U3 配置核心**：alive 清理、0=关 guard、openSections 函数式更新、interval 清理——正确。

---

## 五、建议修复顺序

1. **第一批（行为缺陷，优先）**：G1（付费层竞速路由，功能错误且可复现）+ G3（U2 批量计数矛盾）+ G6（竞速失败反馈盲区）
2. **第二批（数据竞争，-race 可复现）**：G4（transport 懒初始化）+ G5（配置热更）+ G7（质量缓存节流）
3. **第三批（状态机/健壮性）**：G8（熔断半开延迟）+ G9（pool_quality 持久化）+ G10/G11（✕ 关闭、RequestStop 阻塞）+ G12（U3 即时生效）
4. **低危**：G13~G31 随批次清理（每批顺手带几条）