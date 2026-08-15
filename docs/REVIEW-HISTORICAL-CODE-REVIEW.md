# 历史系列 Code Review 问题清单（2026-08-15）

> 审查方式：3 个独立只读审查子代理 + 海鸥复核；审查对象为 P/S/E 系列 Go 并发核心 + U 系列前端
> 基线：`main@0c63703`（初始清单基线）
> 结论：**unreviewed 问题 3 高 / 9 中 / 19 低**——并发骨架扎实，缺陷集中在数据竞争与状态机收尾

## 处理状态总览（2026-08-15 全部完成，main@f5324f4）

| 批次 | 提交 | 内容 | 状态 |
|---|---|---|---|
| 第一批 | `dc6ad14` `44edafd` `faa6672` | G1 G3 G4 G5 G6 G7（付费直连/作用域/transport/原子化/上报/节流锁） | ✅ 已合并，独立审查通过（REVIEW-G1-G12-ACCEPTANCE.md） |
| 第三批 | `5facca5` `2a17bdc` `520167f` | G8 G9 G10 G11 G12（熔断重推/持久化/dismissed/异步/即时生效） | ✅ 已合并，独立审查通过 |
| 补漏 | `081782f` | G32 G33（审查发现组合缺口：Canceled 污染/tripped 状态） | ✅ 已合并 |
| 低危后端 | `b5765b8` | G13~G24 | ✅ 已合并（G13/15/18/20 选注释修正） |
| 低危前端 | `8360202` | G25~G31 | ✅ 已合并 |

> 说明：G13/G15/G18/G20 按「克制」原则选文档/注释修正而非行为变更（改动会动核心路径且收益低）。

---

## 一、🔴 高优先（建议下一阶段先修）

### G1. 竞速路径硬编码 TierFree，付费层请求被扇出到 SOCKS5 代理池
- **位置**：`vendors/opencode/chat.go:436`（`raceDo` 调 `racer.CandidateClients(contract.TierFree, ...)`，签名无 tier 参数）+ `chat.go:219`（`call()` 无条件进竞速）+ `models_source.go:72-78`（`TierPaid` 返回 nil 的防护是死代码）
- **触发场景（错误示例）**：假设你在节点池配了 10 个节点（走 SOCKS5 代理池），并在 codex / AI 编辑器里用带付费 token 的「zen://sk-xxxx」格式请求付费模型。请求进入调用链时 `a.tier()` 实际是 Paid（非 public），但竞速分支不看 tier 就无条件调 `raceDo`。
- **当前逻辑缺陷**：`raceDo` 内部硬编码 `CandidateClients(contract.TierFree, ...)`，把付费请求当成免费层，并行扇出 2 个候选出口（默认 pool_race_copies=2）；而单发路径（`chat.go:227`）用的是 `tr.Client(a.tier(), ...)` 会正确走直连。
- **错误后果**：① 付费 token 被发往第三方 SOCKS5 出口，违反「付费层走直连」的契约（凭据过第三方、隐私/合规风险）；② 更糟的是这些出口返回的 401/429 会被 `markSocks5Result` 记为**池节点失败**，触发冷却/坏池/熔断——一个健康节点被付费流量「污染」后，免费流量也跟着被降权；③ 付费流量触发 429 冷却（S2）后，免费流量被迫单发降级。
- **建议修复**：`raceDo` 签名加 `tier contract.Tier`，`call()` 对 `a.tier() == TierPaid` 直接跳过竞速走单发（与单发路径一致）；或 raceDo 内传 `a.tier()`。

### G2. 停止扫描后全局任务栈 scan 任务冻结（✅ 已修复）
- **状态**：✅ 已由 `70aca85` 修复（NodesPage 加 `onRemove`，停止/非本页停止/idle-done 全路径收尾 scan 任务）。
- 当时的错误示例：扫描到 3/10 时点停止 → 只上报 stop-scan 任务 → scan 任务以「扫描节点 3/10 正在扫描…」永久留在悬浮窗。本清单保留仅作记录。

### G3. U2 状态筛选下批量按钮计数与实际作用域不一致
- **位置**：`src/pages/PoolPage.tsx:94-111`（members 链）+ `385-397`（doReleaseAll）+ `449-453`（doAll）+ `731-739/748-774`（按钮计数 `poolSelected.size`）
- **触发场景（错误示例）**：假设实例池有 5 个运行中 + 3 个已停止成员。你勾选了 3 个「运行中」成员（此时按钮显示「批量释放（3）」正常），然后点筛选切到「已停止」。
- **当前逻辑缺陷**：`poolSelected` 是个独立 state，切换筛选只影响 `members` 显示链，**不会清理选中集**。3 个已选「运行中」成员被过滤隐藏，但仍留在 `poolSelected` 里；按钮的计数和禁用状态只看 `poolSelected.size`。
- **错误后果**：切筛选后按钮仍显示「批量释放（3）」且可点，但点下去实际作用域 `members ∩ selected = 0` → 弹「请先勾选池成员」或确认框显示「已勾选 0 个池成员」，与按钮计数直接矛盾；批量启动（doAll）会对隐藏成员静默跳过，用户完全无感知——以为启动了 3 个，实际 0 个。
- **建议修复**：作用域统一为勾选驱动（按钮计数/禁用态改用 `members ∩ selected` 计算），或切换筛选/搜索时自动清理不在可见域的选中项。

---

## 二、🟡 中优先

### G4. `Vendor.transport()` 懒初始化对 `v.tr` 无锁写（数据竞争）
- **位置**：`vendors/opencode/opencode.go:115-124`
- **触发场景（错误示例）**：假设应用刚启动、模型列表首次刷新与用户第一个请求并发到达（或任何绕过 `sync.Once` 串行化的顺序），两个 goroutine 同时第一次调 `transport()`。
- **当前逻辑缺陷**：`transport()` 是「读 nil → 赋值」的非原子写，无互斥；竞速路径每个请求都调它（`chat.go:213`）。
- **错误后果**：两个 goroutine 同时读到 nil 并同时写 `v.tr` → 数据竞争（`-race` 必报，审查用 16 goroutine 流式压测实测复现 opencode.go:117 vs 119）；理论上可读到撕裂的接口值。生产上窗口窄（启动期通常已预热），但一旦爆发就是难排查的偶发崩。
- **建议修复**：`New()` 里直接初始化 `v.tr = cfg.Transport`（或 fallback），彻底去掉懒初始化。

### G5. 配置热重载对普通全局变量无锁写，与请求路径无锁读竞争
- **位置**：写 `config.go:62-79`（applyConfig，持 configMu）；读 `socks_perf.go:490/354-355`、`socks.go:325/351/381`（请求路径无锁）
- **触发场景（错误示例）**：假设服务运行中，你在设置页把「熔断阈值」从 3 改成 5、或改了 `bad_pool_reset_sec`，`startConfigWatcher`（每秒检查）检测到变化后在 goroutine 里调 `applyConfig`——与此同时池里还有流量在跑。
- **当前逻辑缺陷**：`applyConfig` 在 `configMu` 下写 `poolPerfMode`/`poolBreakerThreshold`/`badPoolResetSec`/竞速阈值等**普通 bool/int 全局变量**；请求路径（`raceCandidates`、`applyPoolResult`、`badPoolResetDuration`、`getHTTPClientWithProxy`）读这些变量时**不持任何锁**。
- **错误后果**：写 goroutine 与读 goroutine 无同步并发访问同一变量 → 数据竞争（`-race` 必报）；x86 上 int/bool 撕裂概率低，但可能读到「半套配置」（如阈值已更新、开关还没更新），Go 内存模型下属未定义行为。
- **建议修复**：四个阈值/开关收敛进 `atomic.Int32/Bool`，或读侧 `configMu.RLock()` 拷贝后使用。

### G6. 竞速落选候选的失败结果完全不可见（不冷却/不进坏池/不触发熔断）
- **位置**：`vendors/opencode/chat.go:432-572`（raceDo 无 Mark 调用）+ `576-585`（raceDrain 只 Close）
- **触发场景（错误示例）**：假设池里有 2 个出口 A、B，A 稳定返回 503/超时，B 正常。每个请求 `raceDo` 都扇出 A+B，B 每次赢。
- **当前逻辑缺陷**：`raceDo` 内部从不调 `tr.Mark`；落选候选的 resp 在 `raceDrain` 里只 `Close()` 不报结果；`call()` 只给赢家打 mark；全败且 `firstFail` 为 err（预算到期/连接错误）时 `proxyAddr==""` → `tr.Mark("",0,err)` 是**空操作**，一个节点都不会被标记。
- **错误后果**：A 节点在竞速中持续「隐形失败」——不累计冷却、不进坏池、不触发熔断，永远留在候选池被反复扇出，S3/S5 的健康机制对它失明；429 同理，竞速中落选候选的 429 不触发 S2 冷却。池里明明有个坏节点，用户却永远看不到它被处理。
- **建议修复**：raceDo 对每个落选候选的最终 outcome 调 `tr.Mark(addrs[i], status, err)`（赢家留给调用方统一标记），或在 all-fail 路径把各候选结果逐个上报。

### G7. `loadPoolQualityCache` 节流字段无锁读写（数据竞争 + 重复读盘）
- **位置**：`socks_perf.go:69-71`（无锁读）/ `92-93`（锁内写）
- **触发场景（错误示例）**：假设网关运行中且有持续流量（每次请求都经 `loadPoolQualityCache`），pool_quality.json 的 mtime 在两次探活之间不变，但上次加载已超过 5s。
- **当前逻辑缺陷**：节流判断 `!ModTime().After(poolQualityStamp) && time.Since(poolQualityLoaded) < 5s` 在**未持锁**下读这两个字段，而它们在 `poolQualityMu` 内写。
- **错误后果**：多个并发请求同时读/写 `poolQualityStamp`/`poolQualityLoaded`（time.Time 双字结构可撕裂）→ 数据竞争（`-race` 流量下必报）；且每 5s 全流量重复 `os.ReadFile` + `json.Unmarshal`（多余的磁盘 I/O + 解析开销）。
- **建议修复**：节流判断放入 `poolQualityMu.RLock()/RUnlock()`，或两字段改 atomic。

### G8. `applyPoolResult` 熔断后每次失败都前移 `openUntil`，延迟半开恢复
- **位置**：`socks_perf.go:349-358`
- **触发场景（错误示例）**：假设某节点连续失败 3 次（阈值）触发熔断 open 的**瞬间**，已经有 20 个并发请求在途正流向它；这些请求随后陆续带着失败返回。
- **当前逻辑缺陷**：`applyPoolResult` 里只要 `failures >= threshold` 就无条件 `openUntil = now + interval`——包括这 20 个「跳闸前已派发」的迟到失败。
- **错误后果**：每个迟到失败都把半开探测窗口向后推迟一个完整 interval；高并发下「最后一批迟到失败 + 整个 interval」可能把恢复探测延后数十秒甚至更久，节点明明早已不忙却被反复推迟探测。
- **建议修复**：仅在 close→open **跳变**（failures 恰好达到阈值那次）或**半开探测失败**（probeUsed）时重设 `openUntil`；跳闸后在途失败不再顺延。

### G9. `pool_quality.json` 持久化无序列化，探活轮与手动触发并发写
- **位置**：`core/manager/poolquality.go:367-391`（load/save）+ `467-478`（后台循环）+ `524-529`（手动触发）
- **触发场景（错误示例）**：假设用户点「立即探活」按钮的同一秒，后台 45s 探活轮也恰好触发，两个 `RunPoolQualityOnce` 同时执行。
- **当前逻辑缺陷**：两者都 `loadPoolQuality` + `savePoolQuality` 同一文件，`os.WriteFile` 是 truncate+write **非原子**操作，无任何互斥；`poolQualityView` 也在任意时刻读该文件。
- **错误后果**：并发写可产生撕裂/截断的 JSON → 一方读到空记录 → 整轮历史样本丢失 → 质量等级集体回退到 unknown → 路由短期退化；UI 视图可能瞬时空。下一轮（45s 后）才自愈，但用户会看到一次莫名的质量闪退。
- **建议修复**：Manager 加探活互斥（或 load/save 共用一把锁）；临时文件 + `os.Rename` 原子落盘。

### G10. 任务卡 ✕ 关闭失效（busy 任务被 poll 秒速加回）
- **位置**：`src/App.tsx:214-218`（removeTask）+ NodesPage/PoolPage 持续 upsert
- **触发场景（错误示例）**：假设你正扫描 50 个节点，悬浮窗显示「扫描节点 12/50」，你觉得吵闹点了右上角 ✕。
- **当前逻辑缺陷**：`removeTask` 只把这条记录从 `tasks` 数组移除，没有任何「已关闭」记忆；NodesPage 的 poll 每 800ms 还会用同一个 id `upsertTask` 加回来（任务还在后台跑）。
- **错误后果**：✕ 点了等于没点——卡片在 1 秒内原样复现，「关闭（后台继续）」语义完全失效，用户只能看着任务卡一直挂着。
- **建议修复**：App 维护 `dismissed` id 集合，upsert 前过滤「已关闭且仍 busy」的任务；任务真正结束后清除 dismissed 记录。

### G11. RequestStop 同步阻塞（/scan/stop HTTP 数百 ms~秒）
- **位置**：`core/manager/probe.go:159`（interruptProbes 串行 kill + wg.Wait 在 HTTP 线程）
- **触发场景（错误示例）**：假设扫描 50 个节点、8 个 worker 全在跑，用户点「停止扫描」，前端 `api.scanStop()` 发 HTTP 请求。
- **当前逻辑缺陷**：`RequestStop` 在 HTTP handler 线程里直接调 `interruptProbes()`——它要 kill 全部活跃探针对（每对 sing-box + opencode2api 两个进程，taskkill 每个可能几十~几百 ms），`wg.Wait()` 等全部结束才返回。
- **错误后果**：/scan/stop HTTP 请求被阻塞数百 ms 到数秒（50 对、并发上限 4 → 约 13 批串行），前端「正在停止中…」按钮一直转圈；Windows PID 复用窗口内二次 kill 有极小误杀风险（幂等性 OK 但值得注意）。
- **建议修复**：中断逻辑移到后台 goroutine，`RequestStop` 置 Stopping 后立即返回快照（渐进计数仍在后台更新）。

### G12. U3 保存轮询间隔后不即时生效
- **位置**：`src/pages/PoolPage.tsx:322-339`、`src/pages/InstancesPage.tsx:102-119`
- **触发场景（错误示例）**：假设用户在实例池设置弹窗把刷新间隔从 5 改成 0（想关掉自动轮询），点保存，toast 提示「重载后生效」。
- **当前逻辑缺陷**：`handleSaveUi` 只调 `configSet` 写后端，**不改前端的生效值 `uiPollSec`**；表单值已显示 0，但页面还在用旧的 `uiPollSec=5` 轮询。
- **错误后果**：用户以为关掉了轮询（表单确实显示 0），实际页面继续每 5 秒静默刷新——「0=关」的需求语义在保存后不成立，直到用户手动刷新页面。表单值与生效值漂移，界面与行为不一致。
- **建议修复**：保存成功后同步 `setUiPollSec(v)` 即时生效，或关闭弹窗时把表单值回滚为生效值。

---

## 三、🟢 低（记录，可按批次清理）

### G13. `contract.go:127-139` 文档与实现不符——仅 Racer 未实现 RaceTracker 的传输被静默禁用竞速
- **触发场景**：假设你接入一个第三方 transport，只实现了 `contract.Racer`（CandidateClients）但没实现 `RaceTracker`（健康计数）。
- **当前逻辑缺陷**：文档承诺「未实现时动态副本回退固定上限」，但 `raceCopies()` 里 `healthy=0` → `pressure=2.0` → 返回 1 → 永不竞速。
- **错误后果**：竞速被**静默禁用**，用户以为开了竞速实际一直单发，无任何提示。
- **建议**：改文档说明，或在无 tracker 时回退配置上限（二选一）。

### G14. `chat.go:436-441` `CandidateClients` 返回 clients/addrs 等长的契约未约束
- **触发场景**：假设未来某个 transport 实现返回 2 个 client 但 1 个 addr（或反之）。
- **当前逻辑缺陷**：`raceDo` 多处 `addrs[i]`（chat.go:476/479/482 等）直接按下标访问，无长度校验。
- **错误后果**：越界 panic，整个网关进程崩掉（HTTP handler 无 recover 会 500/崩溃）。
- **建议**：按 `min(len(clients), len(addrs))` 截断，或契约注明必须等长。

### G15. `config.go:81-92` 竞速配置热重载不生效（需重启）
- **触发场景**：假设用户运行中把 `pool_race_copies` 从 4 改成 2，期待副本数生效。
- **当前逻辑缺陷**：`applyConfig` 只更新全局变量；`vendorParams`（models_source.go:155-163）只在 `newAggregator()` 启动时读一次并快照进 Vendor.cfg。
- **错误后果**：运行中改竞速参数对已装配的 Vendor 完全无效，用户以为改成 2 了实际还是 4。
- **建议**：配置变更时通知重建 Vendor 参数。

### G16. `chat.go:318-323` `low>=high` 压力分段倒挂
- **触发场景**：假设用户手滑配置 `pool_race_pressure_low=0.8`、`high=0.5`（low >= high）。
- **当前逻辑缺陷**：分段判定按原顺序比较，`pressure=1.2`（≥high）→ 返回 1；`pressure=1.4`（<low）→ 反而返回上限。
- **错误后果**：中压力区消失且行为倒挂——压力越大的请求副本反而越多，与设计意图相反。
- **建议**：`applyConfig` 处钳制 low < high。

### G17. `socks_perf.go:111-116/169-172` poolFeedback/proxyInFlight map 无驱逐
- **触发场景**：假设你反复增删节点（添加→删除→再添加不同端口），或订阅更新替换节点。
- **当前逻辑缺陷**：节点从配置移除后，其反馈样本与 in-flight 计数条目永久留在 map（值会自动归零，但条目不删）。
- **错误后果**：内存随节点历史缓慢增长（低频无感），长时间运行后 map 里有大量死条目。
- **建议**：节点移除时清理对应 key。

### G18. `chat.go:589-612` activeRequests 不含流式消费期
- **触发场景**：假设 20 个 long 流式回答同时被客户端慢慢消费（每次消费数百 ms~秒）。
- **当前逻辑缺陷**：流式请求在「返回流」时即 -1，消费期间不计入活跃请求数。
- **错误后果**：压力系数被低估 → 可能开出高于预期的竞速副本数（放大上游请求）。
- **建议**：这是已声明的设计取舍，若要精确需流关闭时再 -1（提示即可）。

### G19. `socks_perf.go:59-72` 质量缓存节流缺陷
- **触发场景**：假设探活被关闭（pool_probe_enabled=false）、流量稀疏，文件 mtime 长期不变。
- **当前逻辑缺陷**：节流条件只挡「距上次加载 <5s」；mtime 未变且 >5s 时每次请求都重新读文件；mtime 倒退（时钟回拨）时 `After` 恒 false，缓存永不刷新。
- **错误后果**：低流量场景持续徒劳读盘；时钟异常时读到陈旧质量数据。
- **建议**：内容 hash 或仅按 5s 节流。

### G20. `socks_perf.go:199-210` 压力分母偏大
- **触发场景**：假设池有 10 个节点，5 个 healthy、5 个 degraded，其中 3 个还在冷却/坏池。
- **当前逻辑缺陷**：`raceHealthyNodeCount` 分母 = healthy|degraded 计数，但 `raceCandidates` 实际候选还排除冷却/坏池/熔断节点。
- **错误后果**：分母偏大 → 压力系数偏小 → 高压时仍按「温和竞速（2 副本）」开扇出，流量分散不足。
- **建议**：分母用实际可候选节点数。

### G21. `core/manager/poolquality.go:379/389` 持久化错误全部静默吞掉
- **触发场景**：假设磁盘满或目录权限异常（如 %APPDATA% 被清理工具锁住）。
- **当前逻辑缺陷**：`loadPoolQuality` unmarshal 失败返回 nil 无日志；`savePoolQuality` 的 `MkdirAll`/`WriteFile` 错误 `_ =` 丢弃。
- **错误后果**：质量数据悄悄丢失/写不进去，用户与日志都无任何线索，排查困难。
- **建议**：至少 `slog.Warn` 一次。

### G22. `core/manager/poolquality.go:469-486` 探活/巡检 goroutine 无停止句柄
- **触发场景**：假设测试或热重建 Manager 实例（同一进程内多次 New/Close）。
- **当前逻辑缺陷**：`StartPoolQualityLoop`/`StartHealthLoop` 起的无限 for 循环 goroutine 没有停止通道。
- **错误后果**：每次重建泄漏一个常驻 goroutine（生产端单一生命周期无感，测试累积可见）。
- **建议**：加 context cancel 句柄。

### G23. `socks.go:418-424` socks5Client 缓存写在 RLock 内
- **触发场景**：假设 `applyConfig` 清缓存（写锁）与多个并发请求同时发生。
- **当前逻辑缺陷**：`socks5Client`/`socks5ClientAddr` 普通变量在 `socks5Mu.RLock` 内被多个 goroutine 写。
- **错误后果**：写-写数据竞争（值等价，实际危害极低，但 `-race` 会报）。
- **建议**：单独写锁或 atomic 指针。

### G24. `core/manager/opencodecfg.go:39-56` upstream_proxy 非法值静默回退 + 病态输入通过校验
- **触发场景**：假设用户填了带鉴权的 `socks5://user:pass@127.0.0.1:7897`，或手滑填了 `socks5:////127.0.0.1:5`、端口 `127.0.0.1:0`。
- **当前逻辑缺陷**：① `net.SplitHostPort` 对带凭据地址报 too many colons → 返回 "" → **静默回退本地直连**，无任何日志；② `//` 前缀被归一化为 `//127.0.0.1:5` 且通过校验（实测）；③ 端口 0 通过 `ParseUint(port,10,16)` 校验。
- **错误后果**：用户以为走了代理，实际流量仍走本地出口（隐私/限流问题）；或 active_socks5 指向必失败的地址 → 节点全部落冷却，表现为「扫描全挂」且无从排查。
- **建议**：校验失败路径 `slog.Warn`；`//` 前缀与 port=0 判非法。

### G25. `PoolPage.tsx:415-420` 一键释放 1.2s 收尾 timer 无句柄
- **触发场景**：假设你点「一键释放」第一次完成后 1 秒内（收尾窗口内）又发起第二次释放。
- **当前逻辑缺陷**：`setTimeout(() => onRelease({active:false}), 1200)` 不保存句柄、无代次标志；旧 timer 到点触发 `removeTask('release')`。
- **错误后果**：第一次的旧 timer 把**第二次进行中的** release 任务卡直接移除，进度条闪没。
- **建议**：clearTimeout 或带代次标志。

### G26. `LogsPage.tsx:236-240` fetchLimitRef 对 call_log_max<=0 保留旧值
- **触发场景**：假设用户在设置里把 `call_log_max` 设为 0 或负数（虽然后端校验保证 ≥100，实际不可达）。
- **当前逻辑缺陷**：`if (c.call_log_max > 0)` 才更新 ref，否则沿用上一次正值；注释「只减不增」也与用户调大后 `Math.min` 会升到 5000 的行为不符。
- **错误后果**：配置语义与注释不符（实际不可达，信息级）。
- **建议**：统一回退默认 5000 逻辑。

### G27. `LogsPage/StatsPage` 筛选日期过期后 value 悬空
- **触发场景**：假设日志页有 6/14、6/15 两天，你选了 6/15；随后清空日志/新日志只剩 6/14。
- **当前逻辑缺陷**：轮询替换 logs 后 `dates` 收窄，`dateFilter`/`day` state 仍保留过期日期。
- **错误后果**：select 显示第一个 option 但 state 是旧值 → 列表/统计被一个**看不见的**日期过滤成空，用户必须手动重选才恢复。
- **建议**：dates 变化时 clamp/清空过期选择。

### G28. `StatsPage.tsx:89-131` 迷你条/趋势数据不随 5s 轮询刷新
- **触发场景**：假设你切到统计页看着底部「每 5 秒自动刷新」文案，此时日志又新增了几百条。
- **当前逻辑缺陷**：5s 轮询只刷 token 统计卡；`loadDates`（MiniBar/趋势条/日期下拉）只在挂载和手动刷新执行；catch 清了 `insOkFail/dayTrend` 却没清 `dates`。
- **错误后果**：图表数据停留在上次手动刷新值，与实际日志脱节，文案误导。
- **建议**：loadDates 并入轮询；catch 清理完整。

### G29. `StatsPage.tsx:136-148` 按天查看请求竞态
- **触发场景**：假设你快速切换按天日期 6/14 → 6/15 → 6/16。
- **当前逻辑缺陷**：`statsByDay` 在途多个、无请求序号，后落地的响应覆盖 state。
- **错误后果**：可能显示与所选日期不匹配的数据（本地后端基本 FIFO，风险低）。
- **建议**：请求序号或 AbortController。

### G30. `PoolPage.tsx:807-914` 筛选空结果显示「暂无池成员」误导文案
- **触发场景**：假设池有 5 个已停止成员，你筛选「运行中」。
- **当前逻辑缺陷**：`members.length > 0 ? 表格 : 空态`——成员存在但被筛选清空时走空态分支，显示「暂无池成员 + 去节点池添加」。
- **错误后果**：文案与计数条矛盾，误导用户以为池是空的。
- **建议**：区分真为空 vs 筛选/搜索无匹配（对齐 InstancesPage 空态）。

### G31. `InstancesPage/LogsPage/StatsPage` `load` 依赖 toast 导致轮询定时器频繁重启
- **触发场景**：假设你连续操作触发了多条 toast（每条约 3.6s 自动消失）。
- **当前逻辑缺陷**：三页 `load = useCallback(..., [toast])`，而 App 的 `showToast` 每次渲染都是新函数 → toast 出现/消失触发 App 重渲染 → load 重建 → poll effect 清理并重跑 → 立即 `load()` 一次 + 5s 定时器重置。
- **错误后果**：连续操作下页面实际变成「事件驱动轮询」，每次 toast 都多一次整量拉取，日志/实例多时浪费明显。
- **建议**：toast 用 ref 封装或从 useCallback 依赖剔除。

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