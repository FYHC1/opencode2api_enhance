# AI 测试避让指南：端口与环境隔离（所有 AI 必读）

> **适用对象**：任何在本仓库工作的 AI 代理 / 助手。
> **核心原则**：本机可能存在**正式版、dev 版、便携测试版、Web 版等多个环境同时运行**，且
> **正式版（`D:\Program Files\opencode2api\`）是交付给客户的生产服务**。任何测试都必须在
> **完全隔离的端口 + 数据目录**中进行，禁止占用其它环境的端口、禁止改动/杀死其它环境的进程。

---

## 1. 端口总清单（代码事实来源）

**端口规划（2026-08-10 决策）**：每个环境固定一段「槽位」，从 **40000 向上、每槽 4100 宽**；
**sing-box 出口 = 实例 API + 2000**（紧挨，不再 +10000 错开）。槽内布局：
`base` 管理器、`+80` 网关、`+90` 探针 API、`+100~+2099` 实例段（2000 个）、
`+2090` 探针 SOCKS、`+2100~+4099` sing-box 段。

| 槽 | 环境 | 管理器 | 网关 | 探针 API/SOCKS | 实例 API 段 | sing-box 段（实例+2000） |
|---|---|---|---|---|---|---|
| 0 | **正式 release** | 40000 | 40080 | 40090 / 42090 | 40100~42099 | 42100~44099 |
| 1 | **dev（tauri dev）** | 44100 | 44180 | 44190 / 46190 | 44200~46199 | 46200~48199 |
| 2 | **便携测试包** | 48200 | 48280 | 48290 / 50290 | 48300~50299 | 50300~52299 |
| 3 | web-dev / headless | 52300 | 52380 | 52390 / 54390 | 52400~54399 | 54400~56399 |
| 4 | 预留 | 56400 | 56480 | 56490 / 58490 | 56500~58499 | 58500~60499 |

- 端口来源优先级：**环境变量 > config.json（gateway_port/instance_base_port/probe_*_port）> 编译默认**。
- 桌面壳按数据目录环境（正式/dev/便携）自动注入槽位端口，新开环境默认隔离，无需手动设置。
- headless/Web 直跑管理端口：`-port`（默认 8000），`-listen` 可收紧到 127.0.0.1；实例/网关/探针端口可经
  环境变量或 config.json 覆盖。
- **9097** Clash 外部控制（用户自行配置的 Clash Verge，拉取节点用）。

> ⚠️ **重点**：实例 sing-box 端口 = 实例端口 + 2000。例如实例占 44200，则 **46200 也同时被占**。
> 检查端口时两者都要查（段内成对）。

### 1.1 测试自身使用的固定端口（`go test` 会真实 bind，任何运行中的服务占了都会导致测试失败）

| 端口 | 测试 |
|---|---|
| **19904 / 29904** | 探针相关测试（probe_test.go 等） |
| **27901 / 27902 / 29901 / 29902** | `TestRestartPoolStartsMembersThenGateway`（模拟运行中实例，sing-box=实例+2000） |
| **21080** | 网关测试 |

> ⚠️ **运行 `go test ./...` 前**，确认没有**节点扫描正在跑**——扫描探针会占用探针端口
> （默认 19000/29000，或你配置的 26xxx/27xxx 段）+ 每个 worker 递增端口，极可能撞上
> 27901/27902/19904/29904 等测试固定端口。**先停止扫描/等待扫描结束，再跑全量测试**。

---

## 2. 环境约定（数据目录 + 端口段，决策 #8）

| 环境 | 数据目录（`OPCODE2API_DATA_DIR`） | 实例端口段（`OPCODE2API_INSTANCE_BASE_PORT`） | 网关（`OPCODE2API_GATEWAY_PORT`） |
|---|---|---|---|
| **正式 release** | `%APPDATA%\opencode2api-manager`（默认） | 40100（槽0） | 40080 |
| **dev**（tauri dev） | `%APPDATA%\opencode2api-manager-dev` | 44200（槽1） | 44180 |
| **便携测试包** | `%APPDATA%\opencode2api-manager-test` | 48300（槽2） | 48280 |
| **web-dev** | `%APPDATA%\opencode2api-manager-web-dev` | 52400（槽3） | 52380 |

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
netstat -ano -p tcp | Select-String 'LISTENING' | Select-String ':8000|:9097|:40080|:44180|:4420|:4620|:48280|:4830|:40000|:44100|:48200|:42090|:46190'
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
   `实例端口+2000`（sing-box）和探针端口。

---

## 5. 安全启动模板（真实服务走查 / 手工冒烟）

桌面壳（tauri dev/正式/便携）已按环境槽位自动注入端口（§1 表），**无需手动设置即可隔离**。
仅当你想用独立数据目录/自定义端口时，覆盖环境变量（槽 1 dev 为基准示例）：

```powershell
cd D:\ai_projects\opencode2api_enhance_main
go build -o opencode2api_e2e.exe .

# ── 隔离：数据目录 + 槽 1（dev 段）端口（也可完全省略端口变量直接用默认槽位）──
$env:OPCODE2API_DATA_DIR           = "C:\Users\ASUS\Desktop\opencode2api-e2e-ai"  # 独立目录
$env:OPCODE2API_MANAGER_PORT       = "44100"   # 管理器（槽1 基址）
$env:OPCODE2API_GATEWAY_PORT       = "44180"   # 网关（槽1 +80）
$env:OPCODE2API_INSTANCE_BASE_PORT = "44200"   # 实例 44200+，sing-box 46200+（+2000 紧挨）
# 探针端口（节点扫描时；槽1 +90/+2090）
$env:OPCODE2API_PROBE_API_PORT     = "44190"   # worker 44191+，不撞实例段
$env:OPCODE2API_PROBE_SOCKS_PORT   = "46190"   # worker 46191+，不撞 sing-box 段

# 管理端口 -port 用 44100（与 OPCODE2API_MANAGER_PORT 一致）
.\opencode2api_e2e.exe -port 44100 -password=123456
```

浏览器访问 `http://127.0.0.1:44100`。**测试完删除 `opencode2api_e2e.exe` 与
`C:\Users\ASUS\Desktop\opencode2api-e2e-ai` 目录，并清掉你启动的进程。**

> ⚠️ `-password=`（等号空值）在 PowerShell 里才是"关闭登录页"；`-password ""` 的空字符串会被
> PowerShell 5.1 吞掉导致 `flag needs an argument`。要么用 `-password=`，要么给真实密码。

---

## 6. 常见坑速查

| 坑 | 说明 |
|---|---|
| sing-box 端口是隐藏的 | 实例 44200 占用时，**46200（+2000）也占着**，检查要成对 |
| 探针端口不显眼 | 节点扫描瞬间占 槽位探针端口（如 dev 44190 + 46190），可能撞你的测试实例 |
| PortSuggest 会避开已占端口 | 它只在**当前环境槽内**建议，自动跳开已占与实例表占用，跨环境天然不重叠 |
| 数据目录决定一切 | 不设 `OPCODE2API_DATA_DIR` = 读写**正式版**的配置和实例池 → 必须设 |
| 残留进程 | dev 会话崩溃后 sing-box 可能残留，检查进程列表（§3.1）而非只看端口 |
