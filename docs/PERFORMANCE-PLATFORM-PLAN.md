# 开发计划：实例池性能模式 + 多端支持（feat/pool-performance）

> **本计划是本次改造的实施依据（阶段 / 验收 / 遗留声明）**，遵循仓库 AGENTS.md 纪律：
> 每阶段 = 功能开发 + 测试 + 验证；**验证不通过的部分下放到下一阶段开头声明并优先处理**。
> 开工前必读 `docs/AI-TESTING-GUIDE.md`（端口与环境隔离红线）与 `docs/ARCHITECTURE-V2-PLAN.md`（架构约束）。
>
> - 分支：`feat/pool-performance`（自 `main` 切出，2026-08-12）
> - 基线：`main@7e5dc2d`（本地领先 origin 2 提交，未推送）
> - 优先级：**性能模式在前（P 系列），多端在后（M 系列）**——性能模式直接解决节点质量不稳痛点

---

## 〇、背景与目标

### 0.1 方向一：实例池性能模式（P 系列，优先）

**痛点（用户原话整理）**：节点质量参差，部分节点"断断续续"——比如 10s 没响应，过 10 秒又恢复。说不能用吧还能用，说能用吧又时常不能用。**无法手动持续检查每个节点**。

**现状缺陷（已核实代码）**：
| 现有机制 | 位置 | 缺陷 |
|---|---|---|
| 健康巡检 | `core/manager/health.go` | 仅 **TCP 端口探测**——端口通=进程活，**测不出链路抖动**（用户场景端口恒通，巡检恒健康） |
| 代理池冷却/坏池 | `socks.go` | **被动**——请求失败才记冷却（20s/45s/2min），坏状态码 3 次进坏池；**无主动探测** |
| 冷却恢复 | `socks.go` | 冷却到期靠**下一次请求碰运气**，无主动回归验证（半开） |
| 路由 | `socks.go` smart/failover/round_robin | 游标+冷却跳过，**无按质量加权**——抖动节点不在冷却期照样被选中 |
| 流内超时续写 | `gateway_timeout.go` | 已吐内容拼上下文重连（最多 3 次）——**保留资产**，性能模式与其协同不冲突 |

**目标**：实例池/节点池具备**主动链路探活 + 滑动窗口质量评分 + 加权路由 + 熔断半开自动恢复**，坏节点自动剔除、恢复自动回归，全程用户无感。

### 0.2 方向二：多端支持（M 系列，后做）

**现状**：Web 版已通（core 托管前端，浏览器全功能）；Linux headless 可跑；**Linux 桌面 / macOS 未实现**（V2 计划 P5 未动工）。
**已具备**：Go core 纯 stdlib 跨平台；Rust 壳编译层无 Windows-only crate；`process_other.go` 非 Windows 已有 SIGKILL/signal-0 实现。
**待做**：`netstat_other.go` 空桩（P5 注释明确 lsof/procfs 替换）、autostart 非 Windows 报"未启用"（需 .desktop/LaunchAgent）、embed.rs 按平台选 sing-box、tauri.conf 打包矩阵、CI 三平台产物。

---

## 一、分支与提交纪律

- 本分支内**一阶段一提交**，每阶段完成 `go test -count=1 ./...` 全绿才提交。
- 两个方向独立分支（`feat/pool-performance` / `feat/multi-platform`），完成后逐个合并回 main。
- 提交信息遵循仓库风格（`feat(ui):` / `fix:` / `build:` / `docs:` 前缀）。

---

## 二、阶段计划

> 每阶段格式：**功能开发 / 测试 / 验证** 三件套 + 验收标准。
> **遗留声明规则**：上一阶段验证不通过项 → 本阶段「阶段开头：上阶段遗留」小节声明，优先处理完毕才能推进本阶段功能。

---

### 阶段 P0：探针进程泄漏修复（✅ 已完成，2026-08-12）

**背景**：用户发现只开 2 个实例，但任务管理器出现 **96 个 opencode2api/sing-box 进程**。
经排查（psutil 全量进程审计 + 代码审查）确认**非内存泄漏，是探针进程泄漏**：

- `core/manager/probe_node.go` 的 `probeNode()`：**仅失败路径 Kill 探针进程**，
  **成功路径（含 HTTP 错误/超时/分类返回）直接 `return base`，不清理** `sbPID`/`ocPID`。
- 每次扫描每探测 1 个节点残留 1 对进程（sing-box + opencode2api），
  8 worker × 多轮扫描 → 数十个进程堆积（实测 8-11 单日扫 6-7 轮即残留 90+）。
- Windows 侧无 Job Object 兜底（`process_windows.go` 仅 taskkill 单进程），
  父进程退出也无法回收已泄漏子进程。

**修复（已提交本阶段）**：`probeNode()` spawn 成功后加 `defer` 清理——
所有返回路径（成功/失败/超时/分类）退出前必杀 `ocPID` + `sbPID`；
幂等 Kill 无害；`spawn` 失败路径原本已清理，保持不变。

#### 测试
- `TestProbeNodeOK` 增强：成功路径断言 `run.starts == 2`（sing-box + opencode）且
  `run.killed` 包含两个探针 pid（101/102）——修复前该断言必失败（killed 为空）。
- `go test -count=1 ./...` 全绿。

#### 验证
- ✅ `go test -count=1 ./...` 全绿（9 包）。
- ⬜ **存量进程清理**：用户机器上已残留的 90+ 进程需一次性清理
  （识别 `runtime/_probe/worker-*` 归属的探针进程 + 更早遗留 sing-box），
  **涉及正式版环境进程，须用户确认后执行**（AGENTS.md 红线：禁止 kill 非自己启动的进程）。

---

### 阶段 P1：链路级主动探活 + 质量评分模型（core 侧）

**目标**：建立"能发现链路抖动"的主动探活子系统与质量评分模型。

#### 功能开发
1. 新模块 `core/manager/poolquality.go`：
   - **链路级探活**：周期（默认 45s，可配）对实例池 Running 成员发**真实 HTTP 探测**（经实例 sing-box SOCKS 通道发轻量请求，如 `GET /v1/models` 或最小 chat），记录延迟与成败——替代 health.go 的纯 TCP 探测（TCP 探测保留用于实例存活判据）。
   - **滑动窗口评分**：最近窗口（默认 10 分钟，可配）内成功率 + 平均延迟 + 连续失败计数 → 每节点质量分（0~100）与等级（healthy / degraded / flaky / down）。
   - 探活调度 goroutine，与 `StartHealthLoop` 并行，探活间隔/超时（默认 3s）可配。
2. 配置项（`core/manager/config.go` 扩展 + `config.example.json`）：
   - `pool_probe_interval_sec`（默认 45）、`pool_probe_timeout_sec`（默认 3）、`pool_quality_window_min`（默认 10）、`pool_probe_enabled`（默认 true）。
3. 数据持久化：`runtime/pool_quality.json`（质量分/延迟/失败计数/最后探测时间，损坏容错）。
4. 管理 API：`GET /api/admin/pool/quality`（质量汇总视图，供后续 UI）；`POST /api/admin/pool/quality/probe`（手动触发一轮）。
5. 探活并发度控制（默认 4），避免探测风暴；单实例探测失败不阻塞整轮。

#### 测试
- 单测（全部 mock/httptest、不触网）：
  - 滑动窗口评分计算：边界（窗口滑出、成功率 0%/100%、延迟分档、连续失败计数）
  - 探活调度：fake Runner / fake HTTP 后端，探测成功/超时/连接拒绝三种路径
  - 持久化 roundtrip + 损坏文件容错
  - 配置解析：新配置项默认值、非法值回退

#### 验证
- `go test -count=1 ./...` 全绿。
- 手动验证（按 AI-TESTING-GUIDE 环境隔离三件套，用测试数据目录）：起 2~3 个实例入池，模拟节点抖动（临时断 sing-box / 挂起代理），确认质量分正确下降、恢复后回涨、**无需手动检查**。
- **验收**：探活间隔/窗口/超时可配；质量分能反映"10s 断断续续"场景；无探活风暴。

---

### 阶段 P2：加权路由 + 熔断/半开自动恢复（core 侧）

**目标**：路由层消费质量分，坏节点自动降权剔除、恢复自动回归。

#### 阶段开头：P1 遗留声明
- （P1 验证不通过项在此逐条列出并优先修复，修复后回归验证。）

#### 功能开发
1. **加权路由**：`socks.go` smart 模式升级——按质量分排序/加权选择节点（分高优先、degraded 降权、flaky 跳过、down 剔除）；failover 模式同样跳过低分节点；round_robin 保持轮询但跳过 down。全池不可用回退原有冷却兜底逻辑。
2. **请求结果回填**：真实请求成败/延迟回写评分窗口（探活 + 实测量通道合一），失败快速反映到分数。
3. **熔断**：连续失败达阈值（默认 3，可配 `pool_breaker_threshold`）→ 节点进入熔断态（open），路由不再选中。
4. **半开恢复**：熔断节点按周期（默认 60s，可配 `pool_halfopen_interval_sec`）放行 1 个探测请求，成功即恢复（closed）自动回归池子，失败继续熔断。
5. 性能模式开关：`pool_performance_mode`（默认 true）——关闭时行为与现状一致（纯游标+冷却）。

#### 测试
- 单测：
  - 加权路由选择：分数排序、降权/跳过/剔除语义、全冷兜底
  - 熔断状态机：open / half-open / closed 转换、阈值边界
  - 半开恢复：探测成功回归 / 失败维持熔断
  - 回填计算：成功/失败/超时对分数影响
  - 性能模式开关：关时路由行为与基线一致（回归快照）
- 集成测试（fake）：模拟"断断续续"节点（交替成功/超时），断言请求自动切换、恢复后自动回归、调用日志含切换事件。

#### 验证
- `go test -count=1 ./...` 全绿。
- 手动验证（隔离环境）：真实坏节点（挂起代理）请求自动切走、不报错；恢复后自动回归；**用户全程无感**（无需手动检查/重启）。
- **验收**：熔断阈值/半开间隔可配；切换事件在调用日志可见；与 `gateway_timeout.go` 续写机制协同（超时切换优先走质量分）。

---

### 阶段 P3：UI 可视化 + 全链路收尾（前端）

**目标**：六页 UI 中暴露质量分与熔断状态，设置页可调参数，完成 P 系列交付。

#### 阶段开头：P2 遗留声明
- （P2 验证不通过项在此逐条列出并优先修复。）

#### 功能开发
1. **实例池页**：每实例显示质量分徽标（healthy 绿 / degraded 黄 / flaky 橙 / down 红）+ 最近延迟 + 熔断状态；性能模式开关入口。
2. **节点池页**：节点行显示质量分/延迟/抖动状态（复用现有延迟列扩展）。
3. **设置页**：探活间隔/窗口/熔断阈值/半开间隔/性能模式开关配置项（走现有 config 热更新）。
4. 路由模式选择加"性能模式（smart+）"选项（与 P2 开关联动）。
5. 轻量化原则：纯 CSS 实现徽标/状态点，不引图表库。

#### 测试
- `npm run build` 通过；前端组件改动走现有测试口径（如有）。
- 管理 API 字段与 TS 类型对齐（`src/lib/api.ts` fetch 路径）。

#### 验证
- 浏览器走查（六页全链路：登录→节点→加实例→启动→入池→性能模式→质量分展示→统计/日志）。
- 与 Windows exe 行为一致（壳与 Web 等价）。
- **验收**：质量分/熔断状态可见、参数可调且热生效、六页 UI 一致。

---

### 阶段 M1：多端——Linux 桌面（系统调用替换 + 打包）

**目标**：Linux 桌面版跑通（实例启停/扫描/网关/托盘），产出 deb/rpm/AppImage。

#### 阶段开头：P3 遗留声明
- （P3 验证不通过项在此声明并优先处理；处理完毕后才推进 M1 功能。）

#### 功能开发
1. `core/manager/netstat_other.go`：空桩替换为 lsof（优先）/ procfs `/proc/net/tcp` 兜底，实现端口占用解析与清理（与 Windows netstat 行为等价）。
2. `core/manager/autostart.go`：非 Windows 实现——Linux 写 `~/.config/autostart/*.desktop`；查询/设置/移除三操作。
3. Clash Verge profiles 目录：按平台配置（Linux `~/.config/clash-verge*`），其余平台给配置项可覆盖。
4. `src-tauri/` embed.rs：按 `cfg!(target_os)` 选择平台 sing-box 二进制；CI 矩阵下载对应 sing-box。
5. tauri.conf.json：加 deb / rpm / AppImage 打包目标；Linux 托盘与窗口行为验证。

#### 测试
- Go 侧：lsof/procfs 解析单测（构造样例输出 golden）；autostart .desktop 生成单测。
- CI：Linux 构建流水线（go test 全绿 + `npm run build` + `tauri build` 产出三种包）。
- 环境隔离：按 AI-TESTING-GUIDE §5 三件套验证端口段。

#### 验证
- Linux 真机/虚拟机：实例启停 + 节点扫描 + 网关 + 托盘全链路走查；deb/rpm/AppImage 均能安装启动。
- **验收**：端口清理/自启/托盘与 Windows 行为等价；三种安装包产物完整。

---

### 阶段 M2：多端——macOS

**目标**：macOS 桌面版跑通，产出 dmg。

#### 阶段开头：M1 遗留声明
- （M1 验证不通过项在此声明并优先修复。）

#### 功能开发
1. autostart：macOS LaunchAgent（`~/Library/LaunchAgents/*.plist`）。
2. 端口清理：macOS 走 lsof（与 Linux 共用实现，平台差异收敛到配置）。
3. tauri.conf：dmg 打包（含 .app）；托盘/无边框窗口行为验证。
4. CI：macOS runner（macos-latest）构建流水线 + sing-box macOS 二进制下载。

#### 测试
- Go 侧平台分支单测（如有平台差异）；CI macOS 构建。
- 全平台 `go test -count=1 ./...` 全绿。

#### 验证
- macOS 真机/CI：实例启停 + 扫描 + 网关 + 托盘 + dmg 安装走查。
- **验收**：macOS 全链路等价；dmg 产物完整；与六页 UI 一致。

---

### 阶段 M3：Web 收尾 + 三平台联验 + 文档

**目标**：Web 部署体验收尾，三平台产物全量联验，文档同步。

#### 阶段开头：M2 遗留声明
- （M2 验证不通过项在此声明并优先修复。）

#### 功能开发
1. Web 收尾：部署文档核对（DEPLOYMENT.md 与实际行为一致）；登录页/鉴权体验检查（Web 沿用密码鉴权，桌面免登录——V2 决策 #7）。
2. 跨平台回归清单：同一组用例在 Win exe / Linux 桌面 / macOS / Web 四端执行（实例生命周期/扫描/网关/统计/日志/性能模式）。
3. 文档：性能模式配置项进 CONFIGURATION.md；质量分/熔断状态进 FAQ/API 文档；ARCHITECTURE-V2-PLAN.md 验收表同步。

#### 测试
- 四端回归记录（每端一条走查结果）。
- `go test -count=1 ./...` + `npm run build` 全绿。

#### 验证
- 三平台安装包 + Web 均可完整使用全部功能；文档与行为一致。
- **验收**：四端等价、文档齐、V2 验收表 P5 项打勾。

---

## 三、验收计划表（完成一项勾一项）

| # | 阶段 | 验收项 | 状态 | 完成日期 | 验证方式 / 备注 |
|---|---|---|---|---|---|
| 0 | P0 | 探针进程泄漏修复（defer 清理 + 成功路径断言测试） | ✅ | 2026-08-12 | `go test` 全绿；存量进程清理待用户确认 |
| 1 | P1 | 探活子系统：周期链路探测 + 滑动窗口评分 + 配置项 + 持久化 + API | ⬜ | | 单测全绿；手动抖动模拟质量分升降 |
| 2 | P2 | 加权路由 + 熔断/半开恢复 + 性能模式开关 | ⬜ | | 状态机单测；断断续续节点集成测试 |
| 3 | P3 | UI 质量分/熔断可视化 + 参数可调 + 六页一致 | ⬜ | | 浏览器走查；exe/Web 等价 |
| 4 | M1 | Linux 桌面：lsof 端口清理 + .desktop 自启 + 三包产物 | ⬜ | | Linux 真机走查；CI 构建 |
| 5 | M2 | macOS：LaunchAgent + dmg + CI | ⬜ | | macOS 走查；CI 构建 |
| 6 | M3 | Web 收尾 + 四端联验 + 文档同步 | ⬜ | | 四端回归记录；文档核对 |

## 四、风险与备选

- **探活成本**：周期真实 HTTP 探测占用节点额度/流量——默认间隔 45s + 并发 4 已克制；额度敏感场景可调 `pool_probe_interval_sec` 或关探活。
- **误熔断**：探活与实测量通道合一后，单次抖动可能误伤——熔断阈值默认 3 次 + 半开自动回归兜底，误熔断影响有限。
- **多端阻塞**：sing-box 平台二进制需 CI 下载（上游发布源可能限流）——备选：构建时手动放置 + 文档说明。
- **行为回归**：性能模式默认开启但提供开关；关闭时走基线路径（已有测试快照兜底）。

## 五、开工清单（P1 第一步）

1. 读 `docs/AI-TESTING-GUIDE.md` §3/§5（端口与进程检查、三件套环境隔离）。
2. `go test -count=1 ./...` 记录基线全绿。
3. 读 `core/manager/config.go` / `health.go` / `socks.go` 现有结构，定 `poolquality.go` 接口。
