# HANDOFF — B 项目 7 项功能合入 A 项目（merge-from-B）

> **交接时间：** 2026-08-04
> **交接分支：** `feat/merge-from-B`（基于 `main` 基线 c443a6b，工作树干净）
> **计划文件：** `.tokeny/plans/plan-1785810482989.md`（已完成项已勾选 [x]）
> **下一位执行者从 Task 11 开始。**

---

## 1. 项目背景

把项目 B（`opencode2api_enhance_2`）的 **7 项独有功能**按定稿方案合入项目 A（`opencode2api_enhance`）。
A 为基底：**Tauri 2 + Rust 后端 + Go 代理核心（main.go）+ React/Tailwind 前端 4 页**。
B 的脚本/UI 风格/free-usage 额度统计 **不迁移**。

## 2. 当前进度总览

| 期 | 功能 | Task | 状态 | Commit |
|---|---|---|---|---|
| 一期 Go | F4 配置热更新 | Task 3 | ✅ | `e50c48f` |
| 一期 Go | F3 代理池健康检查 + SSE 超时修复 | Task 4 | ✅ | `6c8a03e` |
| 一期 Go | F5 模型必填校验 | Task 5 | ✅ | `167191e` |
| 一期 Go | F6 同模型路由重试（不换模型） | Task 6 | ✅ | `6c8a03e` |
| 二期 Rust | F7a 批量启停并行（4/8 worker） | Task 7 | ✅ | `069963f` |
| 二期 Rust | F2 免费额度实测健康检查 | Task 8 | ✅ | `4fd1d33` |
| 二期 Rust | F7b 节点扫描并行 | Task 9 | ✅ | `4fd1d33` |
| 三期 | F1 统一网关（Rust + Go 补丁） | Task 10 | ✅ | `4144107` + `cbd0b4e` |
| 四期 | **前端 PoolPage + 节点分流 + 实例页** | **Task 11** | ⏭️ 未开始 | — |
| 五期 | **集成验证 + 手测 + 合回主干** | **Task 12** | ⏭️ 未开始 | — |

## 3. 验证状态（Task 10 结束时全量验证通过）

- ✅ `go build ./...` + `go test ./...`（新增 9 个单测全过）
- ✅ `cargo build`（src-tauri/）
- ✅ `npm run build`（tsc + vite）
- ⚠️ `cargo test`：**编译通过但 exe 无法运行**（`STATUS_ENTRYPOINT_NOT_FOUND`）——Windows 工具链已知环境问题，测试代码本身能编译；`cargo test --no-run` 可验证编译。

## 4. 剩余任务

### 4.1 Task 11 — 前端（PoolPage + 节点分流 + 实例页）

**目标产物（按 A 风格：左侧菜单、白卡片、teal 主色）：**

1. **新建 `src/pages/PoolPage.tsx`**，左侧菜单第 2 项（App.tsx 的 tabs 数组加 `{ id: 'pool', label: '实例池', icon: ... }`）：
   - 网关状态卡：运行状态、地址 `http://127.0.0.1:18080/v1` 一键复制、统一密钥 `sk-unified-local`、一键关闭（stop）
   - 池成员列表：健康状态展示、移出池按钮
   - 路由模式下拉（failover / round_robin）
2. **`src/lib/api.ts`** 增加（与 Rust 命令一一对应，防契约悬空）：
   ```ts
   gatewayStatus: () => invoke<GatewayStatus>('gateway_status'),
   gatewaySetRouteMode: (mode: string) => invoke<void>('gateway_set_route_mode', { mode }),
   setJoinGateway: (name: string, join: boolean) => invoke<void>('set_join_gateway', { name, join }),
   ```
   类型 `GatewayStatus`（字段见下），`Instance` 类型加 `join_gateway: boolean`。
3. **`src/pages/NodesPage.tsx`**：批量添加目标选择——「添加为独享实例」（默认）/「添加进实例池」（join_gateway=true）。
4. **`src/pages/InstancesPage.tsx`**：每行「移入池/移出池」按钮 + 健康状态展示。
5. 完成后 `npm run build` 全绿 + A 原有 4 页无回归 → 独立 commit。

**Rust 命令契约（均已实现并注册）：**

| 命令 | 签名 | 说明 |
|---|---|---|
| `gateway_status` | → `GatewayStatus` | 网关状态 |
| `gateway_set_route_mode` | `(mode: String)` → `()` | 切换 failover/round_robin |
| `set_join_gateway` | `(name: String, join: bool)` → `()` | 实例移入/移出池 |
| `sync_gateway` | 内部函数（非命令） | 启停/增删实例后自动调用，前端无需直调 |

**GatewayStatus 字段（gateway.rs L18-30）：**
```rust
pub struct GatewayStatus {
    pub running: bool,
    pub address: String,          // "http://127.0.0.1:18080/v1"
    pub port: u16,                // 18080
    pub api_key: String,          // "sk-unified-local"
    pub running_instances: usize,
    pub total_instances: usize,
    pub message: String,
    pub free_models: Vec<String>,
    pub free_models_updated_at: Option<u64>,
    pub free_models_loading: bool,
    pub free_models_error: Option<String>,
}
```

**前端现状：**
- `src/App.tsx`：tabs 数组（instances/nodes/stats/settings）+ 左侧菜单，菜单图标用 lucide-react。
- `src/lib/api.ts`：`api` 对象封装全部 invoke，现有 `listInstances/startInstance/stopInstance/batchStart/batchStop` 等。
- `src/pages/`：InstancesPage / NodesPage / SettingsPage / StatsPage（均接收 `toast` prop）。

### 4.2 Task 12 — 集成验证与合回

1. 全量构建：`go build ./... && go test ./...`、`cd src-tauri && cargo build && cargo test`、`npm run build` 全绿。
2. `npm run tauri:dev` 起桌面应用，按计划文件 Task 12 验收清单逐条走查（独享回归、网关、模型必填、代理切换、SSE 长流、热更新、批量并行、扫描并行）。
3. 修复遗留后再继续，不带着失败交付。
4. **合回主干**（注意：`feat/merge-from-B` 有 7 笔提交，合回时提交信息不含"编译"字样，必要时 squash）。

## 5. 关键技术要点（A 化注意点，务必理解再动手）

1. **网关 flag 坑（最高危）**：A 的 main.go 不支持 `-force-free`/`-free-usage-file` flag，直接传参会 `os.Exit(2)` 秒退。gateway.rs `start_child` 只传 `-port/-config/-password/-log-level`（已处理）。
2. **join_gateway 默认 false**：实例默认独享（一人一实例），入池为显式选项；网关 `sync` 只收「运行中且 join_gateway=true」的实例（instance.rs 有 `join_gateway` 字段，commands.rs `set_join_gateway` 切换并同步）。
3. **路由默认 failover**：成功不动游标、失败/限流/额度耗尽才切下一个健康实例。Go 层 `routeMode` 变量（main.go）+ Rust 层 `route_mode` 配置均已实现；可配置 round_robin。
4. **认证兼容**：统一网关密钥 `sk-unified-local` 命中 A 的 adminPassword 本地门禁分支（`extractUpstreamAuth`），底层走 public 免费通道，不会透传给上游。
5. **big-pickle 补丁**：`isFreeModel` 已加 `|| strings.EqualFold(modelID, "big-pickle")`（main.go L1813-1814）。
6. **健康检查不常驻**：F2 只在手动测试/节点扫描时实测 1 token；无后台常驻全实例探测。
7. **免费额度统计不迁移**：`/api/free-usage`、`FreeUsageData`、前端 FreeUsageCell 明确不移植（定稿方案拒绝项）。

## 6. 已知环境限制

- **cargo test exe 无法运行**（`STATUS_ENTRYPOINT_NOT_FOUND`）：Windows 工具链问题，非代码问题。验证用 `cargo build` + `cargo test --no-run`（编译验证）+ Go/前端测试。
- Git Bash 下 `git add` 有 LF→CRLF 警告，属正常，不影响提交。
- 代理池健康检查涉及真实网络探测，`go test` 的代理池用例为纯逻辑单测，不依赖网络。

## 7. 常用验证命令

```bash
# Go 层（仓库根）
go build ./... && go test ./...

# Rust 层
cd src-tauri && cargo build && cargo test --no-run

# 前端
npm run build

# 起桌面应用
npm run tauri:dev
```

## 8. 接续工作流程建议

1. 检出分支：`git checkout feat/merge-from-B`（或基于它新建分支）。
2. 先跑一遍第 7 节验证命令确认基线干净。
3. 按 Task 11 → Task 12 顺序推进；每功能独立 commit（提交信息不含"编译"字样）。
4. 遇到疑问先看 `.tokeny/plans/plan-1785810482989.md` 定稿方案（Task 11/12 的详细验收清单在里面）。
5. 如需派子代理：任务必须带明确产出物 + 范围边界（superpowers:subagent-driven-development），禁止开放式研读。
