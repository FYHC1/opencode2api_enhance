# AI 测试避让指南：端口与环境隔离（所有 AI 必读）

> **适用对象**：任何在本仓库工作的 AI 代理 / 助手。
> **核心原则**：本机可能存在**正式版、dev 版、便携测试版、Web 版等多个环境同时运行**，且
> **正式版（`D:\Program Files\opencode2api\`）是交付给客户的生产服务**。任何测试都必须在
> **完全隔离的端口 + 数据目录**中进行，禁止占用其它环境的端口、禁止改动/杀死其它环境的进程。

---

## 1. 端口总清单（代码事实来源）

| 端口 | 用途 | 默认值 | 覆盖方式 | 代码位置 |
|---|---|---|---|---|
| **8000** | core HTTP（管理面板 + SPA 托管） | 8000 | `-port` 启动参数 | `main.go` |
| **18080** | 统一网关（-gateway 子进程） | 18080 | `OPCODE2API_GATEWAY_PORT` | `core/manager/tcp.go`、`gateway.go` |
| **18100+** | 实例 API 端口段（批量添加起始） | 18100 | `OPCODE2API_INSTANCE_BASE_PORT` | `core/manager/batch.go` |
| **实例端口+10000** | 每个实例的 sing-box SOCKS5 出口 | 随实例 | 随实例端口 | `core/manager/admin_ops.go`、`batch.go` |
| **19000** | 探针 API（节点扫描时） | 19000 | `OPCODE2API_PROBE_API_PORT` | `core/manager/probe.go` |
| **29000** | 探针 SOCKS（节点扫描时） | 29000 | `OPCODE2API_PROBE_SOCKS_PORT` | `core/manager/probe.go` |
| **10000–39999** | `PortSuggest` 端口建议区间 | — | `OPCODE2API_INSTANCE_BASE_PORT` | `core/manager/port.go` |
| **9097** | Clash 外部控制（拉取节点，用户自己配的 Clash Verge） | 9097 | 设置页 | 用户环境 |

> ⚠️ **重点**：实例 sing-box 端口 = 实例端口 + 10000。例如实例占 18107，则 **28107 也同时被占**。
> 检查端口时两者都要查。

### 1.1 测试自身使用的固定端口（`go test` 会真实 bind，任何运行中的服务占了都会导致测试失败）

| 端口 | 测试 |
|---|---|
| **19904 / 29904** | 探针相关测试（probe_test.go 等） |
| **27901 / 27902 / 37901 / 37902** | `TestRestartPoolStartsMembersThenGateway`（模拟运行中实例） |
| **21080** | 网关测试 |

> ⚠️ **运行 `go test ./...` 前**，确认没有**节点扫描正在跑**——扫描探针会占用探针端口
> （默认 19000/29000，或你配置的 26xxx/27xxx 段）+ 每个 worker 递增端口，极可能撞上
> 27901/27902/19904/29904 等测试固定端口。**先停止扫描/等待扫描结束，再跑全量测试**。

---

## 2. 环境约定（数据目录 + 端口段，决策 #8）

| 环境 | 数据目录（`OPCODE2API_DATA_DIR`） | 实例端口段（`OPCODE2API_INSTANCE_BASE_PORT`） | 网关（`OPCODE2API_GATEWAY_PORT`） |
|---|---|---|---|
| **正式 release** | `%APPDATA%\opencode2api-manager`（默认） | 18100 | 18080 |
| **dev**（tauri dev） | `%APPDATA%\opencode2api-manager-dev` | 30000 | 需显式覆盖 |
| **便携测试包** | `%APPDATA%\opencode2api-manager-test` | 50000 | 需显式覆盖 |
| **web-dev** | `%APPDATA%\opencode2api-manager-web-dev` | 需显式覆盖 | 需显式覆盖 |

数据目录经 `OPCODE2API_DATA_DIR` 注入，`core/manager/paths.go` 的 `DefaultDataDir()` 读取。
**实例池 / 配置 / runtime 互不干扰**——这是隔离测试的根基。

---

## 3. 测试前强制检查（每次必做）

### 3.1 查看当前所有相关进程（不要凭印象，要看事实）

```powershell
# 所有 opencode2api / sing-box 进程（含启动命令，能看出是哪个环境的）
Get-CimInstance Win32_Process | Where-Object { $_.Name -match 'opencode2api|sing-box' } |
  Select-Object ProcessId, Name, CreationDate, @{N='Cmd';E={$_.CommandLine.Substring(0, [Math]::Min(160, $_.CommandLine.Length))}}

# 关键端口监听情况
netstat -ano -p tcp | Select-String 'LISTENING' | Select-String ':8000|:9097|:18080|:1810|:2810|:19000|:29000|:3000|:5000'
```

### 3.2 判断哪些是"别人的环境"（禁止触碰）

- `D:\Program Files\opencode2api\` 开头的进程 = **正式生产版**（可能正在被客户使用）→ **绝不动**
- `C:\Users\ASUS\Desktop\opencode2api-manager-test\` = 便携测试包 → 别人的测试环境 → 不动
- `src-tauri\target\debug\bin\` = dev 构建 → 可能是别的 AI 会话留下的 → 不动
- 只有**你自己本次启动**的进程，才可以由你自己停止。

---

## 4. 测试规则（红线）

1. **绝不直接跑 `-port 8000` 做真实走查** —— 8000 虽默认空闲，但正式版管理器可能随时占它；
   即使空闲，与正式环境共用端口段也会互相干扰。
2. **启动任何真实服务前，必须显式设置三件套隔离环境变量**（见 §5 模板），并选一个**不与任何
   现有进程冲突的端口块**。
3. **禁止 kill 非自己启动的 `opencode2api`/`sing-box` 进程**（可能正在为正式版服务）。
4. **单元测试 / 自动化 E2E 是安全的**：`go test` 用 httptest 随机端口 + `t.TempDir()` 临时目录，
   不占任何固定端口，随时可跑，无需隔离。
5. **测试结束后清理自己启动的进程**（Ctrl+C / Stop-Process 你自己启动的 PID），并复查 §3.1 的
   进程列表确认没有残留。
6. 检查端口用 `netstat -ano | Select-String 'LISTENING'`，**不要只检查目标端口本身**，还要检查
   `实例端口+10000`（sing-box）和探针端口。

---

## 5. 安全启动模板（真实服务走查 / 手工冒烟）

以下端口块 **24000/24080/24100/24900/25900** 避开全部已知占用段（正式 8000/9097/18080/18100+、
探针默认 19000/29000、**测试固定端口 19904/27901/27902/29904/37901/37902/21080**），可作为模板：

```powershell
cd D:\ai_projects\opencode2api_enhance_main
go build -o opencode2api_e2e.exe .

# ── 三件套隔离：数据目录 / 网关 / 实例端口段 ──
$env:OPCODE2API_DATA_DIR           = "C:\Users\ASUS\Desktop\opencode2api-e2e-ai"  # 独立目录
$env:OPCODE2API_GATEWAY_PORT       = "24080"   # 避开 18080（正式网关）
$env:OPCODE2API_INSTANCE_BASE_PORT = "24100"   # 实例 24100+，sing-box 34100+
# 探针端口（如要做节点扫描；避开 19000/29000 默认与 19904/29904 测试端口）
$env:OPCODE2API_PROBE_API_PORT     = "24900"   # worker 24901+，不撞测试端口
$env:OPCODE2API_PROBE_SOCKS_PORT   = "25900"   # worker 25901+，不撞 27901/27902

# 管理端口避开 8000
.\opencode2api_e2e.exe -port 24000 -password=123456
```

浏览器访问 `http://127.0.0.1:24000`。**测试完删除 `opencode2api_e2e.exe` 与
`C:\Users\ASUS\Desktop\opencode2api-e2e-ai` 目录，并清掉你启动的进程。**

> ⚠️ `-password=`（等号空值）在 PowerShell 里才是"关闭登录页"；`-password ""` 的空字符串会被
> PowerShell 5.1 吞掉导致 `flag needs an argument`。要么用 `-password=`，要么给真实密码。

---

## 6. 常见坑速查

| 坑 | 说明 |
|---|---|
| sing-box 端口是隐藏的 | 实例 18107 占用时，**28107 也占着**，检查要成对 |
| 探针端口不显眼 | 节点扫描瞬间占 19000 + 29000，可能撞你的测试实例 |
| PortSuggest 会避开已占端口 | 它自动跳开，但若你的测试端口与正式版段重叠，建议/批处理会互相让位造成混乱 |
| 数据目录决定一切 | 不设 `OPCODE2API_DATA_DIR` = 读写**正式版**的配置和实例池 → 必须设 |
| 残留进程 | dev 会话崩溃后 sing-box 可能残留，检查进程列表（§3.1）而非只看端口 |
