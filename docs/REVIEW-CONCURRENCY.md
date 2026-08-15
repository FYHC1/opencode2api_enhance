# 并发优化审查报告 + 处理状态（2026-08-15）

> 系统性并发审查（只读，未改文件）：按 4 个子系统并行深审后合并去重——
> ① vendors 竞速/熔断（opencode/windsurf/raceDo/breaker）② manager 扫描/探测/订阅/健康
> ③ 网关/聚合/日志/统计 ④ 前端轮询/任务面板。全部行号已逐条核对（`main` @ `2d6e4d3`）。
>
> 处理状态表随实施推进更新：⬜ 待派工 → 🔄 实施中 → ✅ 已合入（提交号）→ ⬜❌ 未通过下放。

## 处理状态总表

| 编号 | 问题 | 严重度 | 归属阶段 | 状态 |
|---|---|---|---|---|
| H1 | 请求取消不穿透到下游（`context.Background()`） | 高 | CONC-1 | ✅ `d120ad6` |
| H2 | 统计落盘每请求 spawn goroutine + 无锁并发写 | 高 | CONC-3 | ✅ `ae22129` |
| H3 | 每请求重建 http.Client/Transport，连接池失效 | 高 | CONC-2 | ✅ `20e70ae` |
| H4 | StopInstance 全局锁内 Kill + 全量落盘 | 高 | CONC-4 | ⬜ |
| H5 | windsurf 账号可被并发借用 | 高 | CONC-6 | ⬜ |
| H6 | 订阅导入端口记账偏移 `+10000`（应 `+2000`） | 高 | CONC-4 | ⬜ |
| M1 | 网关 Status「未运行则拉起」check-then-act 竞态 | 中 | CONC-5 | ⬜ |
| M2 | refreshModels 无锁读 g.password（数据竞态） | 中 | CONC-5 | ⬜ |
| M3 | 模型目录/上游目录多源串行拉取 | 中 | CONC-7 | ⬜ |
| M4 | 调用日志锁内磁盘 IO + 文件永不轮转 + 整文件读 | 中 | CONC-3（写侧 ✅ `ae22129`）/CONC-8（读侧） | 🔄 写侧完成，读侧待 |
| M5 | 健康巡检串行探测 + 双轮无互斥 + 旧快照重启 | 中 | CONC-7 | ⬜ |
| M6 | 自动订阅整轮串行 + 等待间隔取最短源 | 中 | CONC-7 | ⬜ |
| M7 | 订阅缓存 load-modify-write 无锁并发覆盖 | 中 | CONC-4 | ⬜ |
| M8 | 每请求重建模型目录（O(catalog) 热路径） | 中 | CONC-2 | ✅ `20e70ae` |
| M9 | 前端轮询无 in-flight 守卫（4 页） | 中 | CONC-9 | ⬜ |
| M10 | 任务悬浮面板驱动 App 整树重渲染 | 中 | CONC-9 | ⬜ |
| M11 | NodesPage 扫描状态机异常分支不收敛 | 中 | CONC-9 | ⬜ |
| L1 | 熔断/质量路由锁粒度（排序比较器内反复加锁） | 低 | CONC-10 | ⬜ |
| L2 | 每次竞速请求多次 loadPoolQualityCache 读盘 | 低 | CONC-10 | ⬜ |
| L3 | 429 退避 time.Sleep 不感知 ctx 取消 | 低 | CONC-1 | ✅ `d120ad6` |
| L4 | windsurf preRegisterIfLow 防抖失效（局部 channel） | 低 | CONC-6 | ⬜ |
| L5 | storedResponses 无上限/无过期 + 会话只增不减 | 低 | CONC-10 | ⬜ |
| L6 | SSE/流无并发连接上限 | 低 | CONC-10 | ⬜ |
| L7 | probe 单节点预算超支（GET/POST 各自拿预算） | 低 | CONC-10 | ⬜ |
| L8 | probe run() defer 吞掉 ScanError | 低 | CONC-10 | ⬜ |
| L9 | 管理端统计串行 IO（AggregateStats/ResetStats） | 低 | CONC-8 | ⬜ |
| L10 | SettingsPage effect 依赖 [toast] 重发请求/覆盖表单 | 低 | CONC-9 | ⬜ |
| 前端杂项 | toast timer 互相覆盖 / 收起 timer 无限重置 / dismissedRef 压制新一轮 / 退出无防重入 | 低 | CONC-9 | ⬜ |

## 问题明细（含场景与修复建议）

### 🔴 高严重度

**H1. 请求取消不穿透到下游 —— 上游一律 `context.Background()`**
- 位置：`upstream.go:155/185`、`gateway_timeout.go:366`；签名层 `upstream.go:236/265`。
- 现状：适配层把请求 ctx 丢弃；`raceDo` 候选、401 重试链、`EnsureReady`（最长 6min）都拿不到取消。
- 场景：客户端在竞速窗口内断开 → N 个候选一个都不 cancel，全部打到预算到期，非流式还跑完整个重试链，白白消耗配额/带宽/handler。
- 修复：ctx 一路透传 `callOpenCodeAPI(Stream)` → `chatViaVendor(Stream)` → `v.Chat(ctx,msg)`；`streamWithResume` 重连用 `r.Context()`；`EnsureReady` 用请求 ctx。

**H2. 统计落盘：每请求派生写盘 goroutine + 同一文件无锁并发写**
- 位置：`stats_fns.go:51`（`go saveTokenStats()`）、`stats.go:96`（`go saveNodeStats()`）；marshal 在锁内、写盘在锁外。
- 场景：峰值 50 req/s → 每秒 50 个写 goroutine 并发写同一 `stats.json`，交错/最后写赢；goroutine 数随吞吐无界。
- 修复：单一周期 flush goroutine（dirty flag + ticker 1~5s + 退出兜底 flush），锁内只做计数累加。

**H3. 每请求重建 http.Client/http.Transport，连接池完全失效**
- 位置：`socks.go:430`（RR 不缓存）、`socks.go:443-457`（clientForProxy 每次新建）、`models_source.go:85-86`（每候选新建）。
- 场景：RR+竞速 2 候选、并发 100 → 每次请求 2 个全新 Transport，TCP/TLS/SOCKS 握手风暴，吞吐骤降。
- 修复：按 `addr` 缓存 `http.Client`（`socks5CacheMu` 扩为 `map[addr]*http.Client`，代理列表变更整体失效）；流式浅拷贝去 Timeout。

**H4. StopInstance/RemoveInstanceAlive 全局锁内 Kill + 全量落盘**
- 位置：`core/manager/instance.go:165-185`、`195-210`；调用方 `batch.go:148-191`、`restart_pool.go:30`、`DataClean`。
- 场景：一键重启 60 成员 → 串行 120 次 taskkill + 60 次落盘，30s+，期间前端轮询/网关状态/健康巡检全排队。
- 修复：仿 StartInstance 短锁两段式——锁内快照 PID + 置 Stopping，锁外并行 Kill，锁内写回 Stopped + 清 PID。

**H5. windsurf 账号池可被并发借用**
- 位置：`vendors/windsurf/pool.go:98-110`（acquire 返回 avail[0] 指针，仅更新 LastUsedAt）。
- 场景：两个并发 acquire 都拿到账号 A 同一指针，同一 session token 打上游，限流/401 失衡。
- 修复：借用标记占用（InUse/busyUntil，usable 排除），release/markExhausted 解除；`persistLocked` 的持锁写盘一并处理（CONC-6）。

**H6. 订阅导入同批端口记账偏移 `+10000`（应 `+2000`）**
- 位置：`core/manager/subscribe.go:1251`（`usedPorts[port+10000]=true`），对 1221 行选端用 `singboxPortOffset`。
- 场景：一次导入 300+ 节点，某节点 API 端口 == 先导节点 singbox 端口，静默通过（新实例未监听查不到），同时启动必有一方 bind 失败。
- 修复：`usedPorts[port+singboxPortOffset]=true`，与 `BatchAdd` 的 `isPortUsedByInstance(port+singboxPortOffset)` 对齐。

### 🟠 中严重度

**M1. 网关 Status「未运行则拉起」check-then-act 竞态**
- 位置：`core/manager/gateway.go:245-246`（`if !running && memberCount()>0 { startChild }`）。
- 场景：前端多 tab 轮询 + 网关刚停 → 各自 spawn 一个 `-gateway` 子进程抢同一端口，pid 后写覆盖，孤儿进程。
- 修复：新增 `startMu` 串行化「检查+置位+startChild」，与 ApplyKey/sync 路径统一。

**M2. refreshModels 后台 goroutine 无锁读 g.password**
- 位置：`core/manager/gateway.go:343`（fetchGatewayModels(g.port, g.password)）vs `ApplyKey` 锁内写（87-90）。
- 场景：保存新密钥热重启网关时并发轮询触发 refreshModels → 旧密钥抓模型 401，界面 modelsErr 闪错；`-race` 可检出。
- 修复：goroutine 内在 `g.mu` 临界区快照 `pwd/port` 后再请求。

**M3. 模型目录/上游目录多源串行拉取**
- 位置：`core/aggregator/aggregator.go:40-54`（逐厂商 ListModels 共用 60s 预算）、`opencode.go:182-219`（zen+go 串行）、windsurf 多 host 串行。
- 场景：一家上游 30s 无响应 → 其余排队等它耗尽预算，目录刷新超时，`/v1/models` 长期旧目录。
- 修复：errgroup/WaitGroup 并行 + 每厂商独立超时；合并仍单次写锁重建倒排索引。

**M4. 调用日志锁内磁盘 IO + 文件永不轮转 + 读侧整文件**
- 位置：写 `gateway_timeout.go:127-149`（EventLog.Append 持锁 open/write/close）；读 `core/manager/calllog.go:84-130`（整文件 ReadFile 截尾、多实例串行）。
- 场景：瞬时 50 条流收尾串行排队 3 次文件 syscall；数月后日志 2GB，日志页每轮询整读进内存。
- 修复：写侧内存队列 + 单写者批量写 + 按大小/条数轮转；读侧尾部 Seek/小文件整读 + 多实例并发读归并。

**M5. 健康巡检串行探测 + 双轮无互斥 + 旧快照重启**
- 位置：`core/manager/health.go:101-119`（串行 probePort 1s 超时）、`128-149`（旧快照重启）、`212`（HTTP 手动触发同函数）。
- 场景：30 实例全挂一轮 30s；后台循环与手动 POST 并发对同一实例重复 stop/start；用户刚停的实例被强制拉起。
- 修复：probePort 并行（semaphore ≤8）+ `healthMu` 串行轮次 + 重启前复核仍 Running + health.json 原子写。

**M6. 自动订阅整轮串行 + 等待间隔取最短源**
- 位置：`core/manager/subscriptions.go:152-170`（逐源 `importSubscriptionForSource`、wait=min 间隔）；fetch 超时 `subscribe.go:72`（20s）。
- 场景：一条 DNS 黑洞订阅每轮挂 20s，其余订阅节点刷新连带延迟；间隔 24h 的源每轮被拉。
- 修复：每源独立 goroutine + 各自 IntervalMin 调度（semaphore 门控 ≤4）；StartSubscribeLoop 与 RunAllSubscriptionLoop 二选一保护。

**M7. 订阅缓存 load-modify-write 无锁**
- 位置：`core/manager/subscribe.go:1158-1173`（load→merge→整文件写）、1138/1188 两条路径锁前写缓存。
- 场景：后台自动拉订阅 A 与手动导入订阅 B 并发 → 两次全文件写交错，A 组节点从节点池消失。
- 修复：缓存读写加 `cacheMu`（load/save 过锁）或临时文件+rename 原子替换 + 锁内基于最新内容合并。

**M8. 每个聊天请求都重建模型目录**
- 位置：`upstream.go:125/160`（每请求 syncVendorState+seedVendorCatalog）、`upstream.go:81-96`（遍历缓存表重建）、`107-123`（遍历聚合目录）、`opencode.go:294-298`（SetCatalog 拷贝）。
- 场景：200 模型 × 100 req/s = 每秒 2 万次条目遍历拷贝，纯浪费的热路径串行点。
- 修复：目录加 generation 版本号，`syncVendorState` 后记版本，请求侧仅版本变化才 SetCatalog。

**M9. 前端轮询无 in-flight 守卫（4 页）**
- 位置：`LogsPage.tsx:248-268`（5s 全量 5000 条）、`InstancesPage.tsx:71-80/137-142`、`PoolPage.tsx:120-129/165-171`、`StatsPage.tsx:171-196`。
- 场景：load 慢于间隔 → 请求叠加；旧响应后到整体覆盖新快照（已释放实例复活、日志回退、数字来回跳）。
- 修复：每页代次序号/inFlightRef（复用 StatsPage `dayReqSeq` 模式）。

**M10. 任务悬浮面板驱动 App 整树重渲染**
- 位置：`src/App.tsx:65-67`（tasks state）、`86-97`（upsertTask 未 useCallback）、`219-224`（onTask 每次渲染新建）、`242-281`（面板内联渲染）；驱动源 `NodesPage.tsx:265`（800ms 轮询）。
- 场景：200 条批量测试每完成一条 reportBatch 触发一次 tasks 更新 → 整张 200 行表格每完成一条全量重渲染，页面卡顿。
- 修复：任务面板抽独立 memo 组件 + `useCallback` 稳定回调 + busy 进度节流上报。

**M11. NodesPage 扫描状态机异常分支不收敛**
- 位置：`src/pages/NodesPage.tsx:214-226`（stopping 只认 done）、`237-241`（error/idle 只回 idle 不清理任务）；兜底 `src/App.tsx:126-141`（仅 scan 类型、≥60s）。
- 场景：端口不足 ScanError 结束 → busy scan 任务残留最长 60s；停止后状态回落 idle → 「正在停止」永久卡死，按钮禁用需切页。
- 修复：error 分支同步 onRemove/收尾上报；stopping 把 error/idle 与 done 等价处理；stale 兜底覆盖 `stop-scan`。

### 🟡 低严重度

**L1. 熔断/质量路由锁粒度** — `socks_perf.go:618-629` 排序比较器内每对元素 `proxyInflightOf`（每把全局锁）+ 每候选 `raceScoreJitter`；`pickWeightedProxy`（428-486）持 `socks5HealthMu` 叠取 breaker/quality/feedback/rand 四把子锁。→ 先快照局部再排序，`socks5HealthMu` 收窄为只保护列表。

**L2. 每次竞速请求多次 loadPoolQualityCache** — `socks_perf.go:231/414/538`，5s 后每请求 Stat/ReadFile 读盘。→ 质量刷新上移为节流定时器，请求路径只读缓存。

**L3. 429 退避 time.Sleep 不感知 ctx** — `chat.go:275-276`（最长 30s）。→ `select { <-ctx.Done(); <-time.After(backoff) }`。

**L4. windsurf preRegisterIfLow 防抖失效** — `windsurf.go:242-256`（局部 channel 恒空，每次调用都 spawn goroutine）。→ 防抖通道提为 Vendor 字段或复用 registering 标志。

**L5. storedResponses 无上限/无过期 + 会话只增不减** — `responses.go:428-453`（无条件写入完整 output）、`auth.go:154`（sessions 仅登出删除）。→ 加最大条数 + TTL + 定期清理；会话惰性过期。

**L6. SSE/流无并发连接上限** — `gateway_timeout.go:398-417`（每连接常驻读 goroutine）、chat 入口无信号量。→ 进程级并发流上限（复用 activeRequests 压力计数），超限 429/503。

**L7. probe 单节点预算超支** — `probe_node.go:94-108`、`probe_completion.go:47/75`（GET 拿 budget/2、POST 拿整 budget）。→ 共享 deadline 动态拆账，两请求总耗时 ≤ budget。

**L8. probe run() defer 吞掉 ScanError** — `probe_run.go:14-24`（无条件置 ScanDone）vs 34-41（ScanError 后 return）。→ defer 只在 ScanRunning/ScanStopping 时置 Done，错误态保留。

**L9. 管理端统计串行 IO** — `core/manager/stats.go:82-181`（串行读全部实例 stats.json）、`219-268`（ResetStats 逐实例 HTTP 6s 超时）。→ 并行读/并发 DELETE（小并发数）。

**L10. SettingsPage effect 依赖 [toast]** — `SettingsPage.tsx:37-53`（toast=showToast 每次渲染新建 → effect 重跑重发 3 请求 + 服务器旧值覆盖未保存表单）。→ toastRef + deps `[]`（对齐 G31 模式）。

**前端杂项** — `App.tsx:74-77` toast 多 timer 互相覆盖（→单 timer 复位）；`118-126` 收起 timer 被全局 tasks 变化无限重置（→按任务自 upsert 后计时）；`86-89/106-109` dismissedRef 压制同 id 新一轮 busy（→按任务实例记忆/结束即清）；`164-190` 退出/批量操作无 ref 防重入（→exitGuard/allBusyRef）。
