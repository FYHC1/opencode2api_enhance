# 网关流内超时切换 + 全流程日志 设计文档

> **日期：** 2026-08-05
> **状态：** 待评审
> **范围：** opencode2api_enhance（Go 核心 main.go + Rust 后端 + React 前端）

## 背景与问题

用户反馈：**经常回复一半就不回复**。

经过对 `model-gateway`（Python 参考项目）与 `opencode2api_enhance_main`（Go 核心）的源码分析，确认根因：

1. `getStreamingHTTPClientForTierWithProxy`（`main.go:366-374`）**主动把流式客户端总超时置 0**（注释：避免健康长推理流被 5 分钟切断）。副作用：上游推理中途挂起、连接仍开着时，`reader.ReadString`（`main.go:2166`）**无限阻塞，永不超时、永不切换**。
2. 流中断时（`main.go:2169-2174`）：只发 `data: {"error":"stream read error"}` 就 `return`。**不换节点续传、不保留已吐内容**。
3. 没有任何 TTFT（首字）计时、没有块间静默计时。
4. 上游 2xx 后（`main.go:2145-2162`）：`callOpenCodeAPIStream` 直接返回 body，**之后的读循环不再触碰任何切换逻辑**——failover 在流建立后完全失效。
5. 现有 `loggingMiddleware`（`main.go:661`）只记 `slog.Debug` 的 method/path/remote，**没有逐请求 JSON 落库**，无法诊断"卡死发生在哪一层"。

## 目标

1. **流内超时 + 断点续写切换**：给流式读循环加 TTFT 与块间静默计时，超时后自动切换健康节点，并把已吐内容作为上下文续写。
2. **全流程日志**：记录每个请求的完整决策链（接口/模型/节点/路由模式/超时/切换/结果），前端新增独立日志页。
3. **区间配置表单**：超时值不固定，而是一个 `[min, max]` 区间，每次请求随机取值，防上游识别成定时扫描/攻击。

## 非目标（YAGNI）

- 不做跨请求级联（一个请求同时经多个节点）。
- 不做真实节点竞速探测的自动启停——仅做"切换前并行探测候选"。

## 架构总览

```
前端 LogsPage(React) ──invoke──▶ Rust get_call_log ──读──▶ call_log.jsonl (Go 落盘)
                                                              ▲
客户端 ──▶ 统一网关 Go 进程 18080 ──(超时计时+切换在 Go)──▶ 某实例 sing-box SOCKS5 出口 ──▶ opencode 上游
                                                              │
                                                              └──▶ call_log.jsonl 每事件一行
```

- **Go 层**：负责超时计时、切换、续写、事件日志落盘（`call_log.jsonl`）。
- **Rust 层**：新增 `get_call_log` 接口读取 JSONL。
- **前端**：新增 `LogsPage` 渲染时间线，支持"只看失败"过滤。

## 功能设计

### 功能 1：流内超时 + 断点续写切换（Go 层）

**改动位置：** `chatCompletionsHandler`（`main.go:2101`）流式读循环 + 新增续写切换函数。

**计时器（区间随机）：**

| 配置项 | 默认 min | 默认 max | 含义 |
|--------|---------|---------|------|
| `timeout_ttft_min/max` | 15s | 25s | 建流后等首个 chunk，超时判定 |
| `timeout_silence_min/max` | 30s | 60s | 两个数据块之间无数据，判定卡死 |
| `failover_probe_min/max` | 2 | 3 | 切换前并行探测的候选数 |

- 每个请求在 `[min, max]` 区间内**均匀随机**取一个值（Go `math/rand/v2`）。
- 下限保护：即便随机也不会低于 min 触发过密重试/探测。

**切换流程（断点续写）：**

1. TTFT 超时或静默超时触发。
2. 调用新增的 `probeCandidates(candidates, n)`：并行向 n 个候选发最小请求（`max_tokens:1`），选最先 2xx+有内容 的作为切换目标。
3. 构造续写请求：原 messages + `{"role":"assistant","content":<已吐内容>}` + `{"role":"user","content":"请继续上面的回复，从中断处接着写。"}`
4. 切换到新节点继续 SSE 转发，客户端无感。
5. 记录事件日志（见功能 2）。

**失败兜底：** 全部候选失败 → 发 `data: {"error":"所有节点均失败，回复中断"}` 并结束。

### 功能 2：全流程日志（Go 埋点 + Rust 接口 + 前端页）

**日志格式（JSONL，一行一条请求记录）：**

```json
{"req_id":"abc123","ts":"2026-08-05T12:00:00.000Z","path":"/v1/chat/completions","model":"qwen25-coder-32b","stream":true,"route_mode":"failover","nodes":["127.0.0.1:28100","127.0.0.1:28101"],"events":[{"type":"choose_node","node":"127.0.0.1:28100","at":"..."},{"type":"connect_ok","at":"..."},{"type":"ttft_timeout","ttft_ms":18000,"at":"..."},{"type":"switch","from":"127.0.0.1:28100","to":"127.0.0.1:28101","reason":"ttft_timeout","at":"..."},{"type":"complete","status":"ok","prompt_tokens":12,"completion_tokens":345,"duration_ms":42000,"at":"..."}]}
```

**前端展示规则（用户确认）：**

- 成功日志：**一行简短**（如 `[成功] 12:00:00 qwen25-coder-32b 经 127.0.0.1:28100 45s 345tok`）。
- 异常/切换日志：**占一整块详细展示**（时间线列出每个事件：连接失败/超时/切换原因/目标节点/错误信息）。
- 每条日志以 `【成功】` / `【失败】` 开头，便于视觉识别与过滤。
- **"只看失败"复选框**：过滤只显示失败/切换日志。
- **保留策略：** 默认 5000 条，可配置（设置页）。

**存储：** `runtime/_unified-gateway/call_log.jsonl`，环形截断（超过上限丢弃最旧）。

### 功能 3：区间配置表单（前端 + 热加载）

- 设置页新增"网关超时"区块：3 个区间表单（min/max 各一输入框）。
- 配置写回 `config.json`，复用现有 `startConfigWatcher`（`main.go:1015`）热加载，不重启。
- Rust 层新增读/写配置接口（或复用现有配置接口）。

## 错误处理

- **续写切换失败**（探测无响应/新节点也失败）：回退到下一个候选；全部失败 → 结束流并发错误事件。
- **日志写入失败**（磁盘满/权限）：只 `slog.Error` 不阻塞请求。
- **配置非法**（min > max、超出范围）：拒绝保存，前端提示，沿用旧值。

## 测试策略（每阶段独立验证）

### 阶段 1：实验网关（独立目录 `D:\AI_Projects\gateway_experiment`）

本地模拟节点：
- **黑洞节点**：连接后永不回包（模拟静默卡死）→ 验证 TTFT 超时触发。
- **半路断流节点**：吐几个 token 后断连接 → 验证静默超时/流中断切换。
- **正常节点**：正常 SSE 响应 → 验证不误切。

验证点：
1. 黑洞 → TTFT 超时 → 切正常节点续写。✅
2. 半路断流 → 静默超时 → 切正常节点，内容衔接（无重复/无缺失）。✅
3. 区间随机：跑 20 次请求，TTFT 实际值落在 [15,25] 且非恒定。✅
4. 并行探测：切换时探测数 = 配置值。✅
5. 全部失败 → 错误事件正确。✅

### 阶段 2：落地集成（opencode2api_enhance_main）

- Go 层：移植实验模块，接入 `chatCompletionsHandler` + `callOpenCodeAPIStream`。
- Rust：新增 `get_call_log`。
- 前端：新增 `LogsPage` + 设置页表单。
- 验证：单元测试（超时/切换/续写/日志）+ 真实节点冒烟 + 前端手动验证。

## 阶段失败回滚机制

按用户方法论：**每阶段末验证失败的部分，记录到下一阶段计划中修复**，循环直到全部完成。每阶段结束写阶段报告，标注哪些验证失败、如何滚入下一阶段。
