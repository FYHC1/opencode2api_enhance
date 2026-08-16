# HANDOFF —— 交接说明（给接手同事）

> 本文档是 `docs/PERFORMANCE-PLATFORM-PLAN.md` 的配套交接说明。
> **读法**：先读本文档掌握全局与决策背景 → 再按 `PERFORMANCE-PLATFORM-PLAN.md` 的阶段计划逐项施工。
> 生成日期：2026-08-12。交接人：海鸥（自动化助手，会话上下文可追问仓库历史）。

---

## 一、任务一句话

在 **实例池** 上实现「请求级竞速 + 链路级质量评分」的性能模式（解决节点断断续续导致的响应慢），
修复桌面退出卡顿与一键测试误报，随后推进 Linux/macOS/Web 多端。

## 二、仓库与分支状态

| 项 | 值 |
|---|---|
| 仓库 | `D:\ai_projects\opencode2api_enhance` |
| 当前分支 | `feat/pool-performance`（自 `main@7e5dc2d` 切出，2026-08-12） |
| 未推送 | 本地领先 origin 2 提交（`7e5dc2d`、`50e1bd0`）——**上游入库前先处理** |
| 本分支提交 | `bf321b4` → `ca9dec2` → `3903bdf` → `b2eee5c`（见下） |

```
bf321b4  docs: 计划新增 D2 阶段（一键测试未启动误报修复 + 延迟毫秒级显示）
ca9dec2  docs: 实例池性能模式 UI 草图（HTML 交互版 + PNG 参考渲染）
3903bdf  docs: 计划扩写为交接级详细版（P1/P2 数据结构+配置+API 契约、D1 退出改造、UI Markdown 草图）
b2eee5c  fix: 探针进程泄漏——probeNode 成功路径也清理 sing-box/opencode2api 探针进程
```

## 三、阶段总览与优先级

| 序 | 阶段 | 内容 | 状态 |
|---|---|---|---|
| 0 | **P0** | 探针进程泄漏修复 | ✅ 已提交 `b2eee5c` |
| 1 | **P1** | 链路级主动探活 + 滑动窗口质量评分 | ⬜ 下一优先 |
| 2 | **P2** | 请求竞速（hedged requests）+ 熔断/半开恢复 | ⬜ |
| 3 | **P3** | UI 质量分/熔断可视化 + 参数设置 | ⬜ |
| 4 | **D1** | 桌面退出二次确认 + 并行释放 + 进度条 | ⬜ |
| 5 | **D2** | 一键测试未启动误报修复 + 延迟毫秒级 | ⬜ |
| 5b | **D3** | 并发设置抽离「并发与性能」分组（六项可配） | ⬜ |
| 6 | **M1~M3** | Linux 桌面 / macOS / Web 收尾 | ⬜ |

**执行纪律**（AGENTS.md 强化）：
- 每阶段 = 功能开发 + 测试 + 验证；验证不通过项在**下一阶段开头声明**并优先修复。
- `go test -count=1 ./...` 全绿才提交；一阶段一提交。
- **环境红线**：`docs/AI-TESTING-GUIDE.md` 必读——测试/开发用
  `OPCODE2API_DATA_DIR` / `OPCODE2API_GATEWAY_PORT` / `OPCODE2API_INSTANCE_BASE_PORT` 三件套隔离，
  禁止 kill 非自己启动的 opencode2api/sing-box 进程（AGENTS.md 硬性红线）。

## 四、关键决策记录（为什么这么做）

### D1. 性能模式 = 竞速 + 评分闭环（2026-08-12 用户确认）

用户原诉求「节点断断续续（10s 无响应又恢复），不想手动检查」。
讨论过的三个方案：
- ❌ **纯加权路由**（后台打分→路由按分选）：决策依据有了，但用户请求仍可能撞上抖动节点，被动。
- ❌ **纯竞速**（无脑向全部节点扇出）：额度消耗 N 倍，且向已知坏节点浪费名额。
- ✅ **竞速 + 评分闭环**（采用）：探活=决策、竞速=执行、结果=反馈。
  竞速只在候选池（healthy+degraded）内选 2~3 个，不向 down 节点浪费额度；
  竞速失败者记冷却 = 每次真实请求都是免费探活。**默认竞速副本 = 2（可配 1~4），用户确认接受 2 倍额度消耗。**

### D2. 探针进程泄漏根因（P0，已修复）

- 症状：用户只开 2 实例，任务管理器 96 个 opencode2api/sing-box 进程。
- 根因：`core/manager/probe_node.go` 的 `probeNode()` **仅失败路径 Kill 探针进程**，
  成功路径（HTTP 错误/超时/分类返回）直接 `return base` 不清理 → 每次扫描每节点残留一对进程。
- 修复：spawn 成功后 `defer` 清理，覆盖所有返回路径。测试 `TestProbeNodeOK` 增强断言 killed。
- **存量清理待办**：用户机器上残留的 90+ 进程需一次性清理（按 `runtime/_probe/worker-*` 路径识别），
  涉及正式版进程，**须用户确认后执行**。

### D3. 退出慢的根因与方案（D1 阶段）

- 症状：托盘「退出」在 20 实例运行时卡顿数秒。
- 根因：`src-tauri/src/lib.rs` quit → `commands.rs stop_all_instances` **串行** taskkill
  40 次（20 实例 × 2 进程/实例），无进度反馈。
- 方案：方式二「退出并释放」= 并行释放（batch 4）+ 前端进度条 0/20 → 完成后再 exit。
- 方式一「退出不释放」：硬障碍是 `src-tauri/src/job.rs` Windows Job Object（壳退出自动杀全部子进程，
  防孤儿设计）。方案 A = `NtRemoveProcessFromJob` 摘除保活 + 下次 attach；方案 B = 隐藏窗口托盘常驻。
  **未经用户拍板，默认排 M 系列评估，先交付方式二。**

### D4. 一键测试误报根因（D2 阶段）

- 症状：10 实例（7 正常 3 停止）一键测试报「成功 7 失败 3」红色 toast，误报异常。
- 根因：前端 `doAll('test')`（`PoolPage.tsx` / `InstancesPage.tsx`）对**全部池成员**（含非 Running）
  发起测试，`OK:false` 一律计入失败；后端 `InstancesTestHandler` 对非 Running 返回 `OK:false`（友好文案，
  但前端归类错误）。
- 修复：一键测试只测 Running；三分类 `正常 N / 未启动 M / 失败 K`；红色 toast 仅真失败。

### D6. 并发设置抽离（2026-08-12 用户需求）

- 诉求：并发抽到设置菜单自定——**并发与电脑性能强相关**（进程级并发吃内存/CPU），
  与上游额度相关（竞速/探活级别为 HTTP 级），硬编码不健康。
- 现状：扫描并发 8 写死、批量启停 start 4/stop 8、**一键测试前端全量并行无上限**（30 实例齐发可打爆弱机）。
- 决策：新增 **D3 阶段**——设置页「并发与性能」分组，六项全部可配：
  `scan_concurrency`(8) / `batch_op_concurrency`(4) / `test_concurrency`(4，补上限) /
  `release_concurrency`(4，D1 引) / `pool_probe_concurrency`(4，P1 引) / `pool_race_copies`(2，P2 引)。
- 草图：Markdown 结构在 `PERFORMANCE-PLATFORM-PLAN.md` D3 阶段内；进程级并发在上、
  性能模式（依赖 P1/P2）中置灰、释放（依赖 D1）在下。

### D5. 其他已确认偏好

- UI 草图用 **Markdown 文本结构**表达（非必要不做渲染效果图）；参考渲染图仅 `docs/images/pool_perf_sketch/`。
- 探活/测试延迟**一律毫秒级整数**（`1234ms`），不换算秒。
- 实例池默认路由模式 smart；性能模式独立开关 `pool_performance_mode`，关闭时行为与基线完全一致。
- 开发环境只有 Edge 浏览器（无 Chrome），需要浏览器自动化时用 Edge headless / agent-browser 配 Edge。

## 五、P1 开工清单（已就绪）

1. 读 `docs/AI-TESTING-GUIDE.md` §3/§5。
2. `go test -count=1 ./...` 基线已全绿（2026-08-12 验证）。
3. 读现有代码：`core/manager/config.go` / `health.go` / `socks.go` / `probe_node.go` / `admin_ops.go`，
   新文件 `core/manager/poolquality.go` 复用 `socks5Dial` / `markSocks5Result` / 配置热更新机制。
4. 按 `PERFORMANCE-PLATFORM-PLAN.md` **P1 → P2 → P3 → D1 → D2 → M 系列** 顺序推进。

## 六、索取更多上下文

- 计划与决策：本仓库 `docs/` 下（本文件 + `PERFORMANCE-PLATFORM-PLAN.md` + 各既有文档）。
- 历史会话：本仓库 git 提交信息含根因摘要；更早的讨论可通过会话检索（`session_search`）追回。
- 架构事实来源：`docs/ARCHITECTURE-V2-PLAN.md`（P0~P4 已完成，P5 即 M 系列的前身）。