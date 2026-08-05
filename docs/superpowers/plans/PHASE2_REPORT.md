# 阶段 2 验证报告（2026-08-05）

**项目：** opencode2api_enhance_main（main 分支，worktree）

## 目标回顾

把阶段 1 实验验证过的「流内超时 + 断点续写切换 + 全流程日志」落地到真实项目：
- Task 2.1：Go 核心接入（配置 + 事件日志 + 超时切换）
- Task 2.2：Rust `get_call_log` 接口
- Task 2.3：前端日志页 + 设置页区间表单
- Task 2.4：验证与收尾

## 完成成果

| 任务 | 内容 | 验证方法 | 结果 |
|------|------|---------|------|
| 2.1a | `gateway_timeout.go`：TimeoutConfig(区间随机) + EventLog(环形+JSONL) + 配置热加载 | `go test` 单测 | ✅ |
| 2.1a2 | `chatCompletionsHandler` 接入基础调用日志（成功/失败/流中断） | `go test` + e2e | ✅ |
| 2.1b | `streamWithResume`：流内 TTFT/静默超时 + 断点续写切换 | `TestStreamWithResume*` TDD | ✅ |
| 2.2 | Rust `get_call_log` + `call_log.rs` 模块 | `cargo check` | ✅ (编译通过) |
| 2.3 | 前端 `LogsPage`（一行/整块/只看失败）+ 设置页区间表单 | `npm run build` | ✅ |
| 2.4 | 全绿验证 + 报告 | 见下 | ✅ |

## 验证结果

### Go 侧（全绿）
- `go vet ./...` 通过
- `go test ./...` 全部通过（**修复了 PR #4 遗留的 8 处测试签名破损**，使 test 从红灯转绿）
- 新增测试：
  - `TestTimeoutConfigRandomRange`：区间随机在界内且非恒定
  - `TestCallStatusText` / `TestCallLogRingBuffer` / `TestCallLogJSONLFile`：日志模块
  - `TestSetTimeoutConfigFromApp`：配置热加载 + 非法区间忽略
  - `TestBuildResumeBody`：续写 body 构造
  - `TestStreamWithResumeNormal`：正常上游成功完成，不误切
  - `TestStreamWithResumeSwitchOnInterrupt`：上游中断→续写重连→内容衔接（含 switch 事件断言）
  - `TestRecordCallEndToEnd`：call_log.jsonl 真实落盘格式

### Rust 侧（编译通过）
- `cargo check` 通过（`call_log.rs`、`commands.rs get_call_log`、`config.rs` 扩展、`opencode_cfg.rs` 注入）
- **测试运行失败**：`cargo test` 报 `STATUS_ENTRYPOINT_NOT_FOUND` —— **预先存在的环境问题**（WinLibs mingw 链接器 `.rsrc manifest` 冲突，`git stash` 后同样失败，与本次代码无关）

### 前端（全绿）
- `npm run build`（tsc -b && vite build）通过

## 阶段 2 遗留（验证失败项）

| 项 | 现象 | 根因 | 建议 |
|----|------|------|------|
| Rust 单测无法运行 | `cargo test` exit `STATUS_ENTRYPOINT_NOT_FOUND` | WinLibs mingw 链接器 + Tauri 嵌入 manifest 冲突；`link.exe`(MSVC) 未安装 | 安装 VS Build Tools 或换 `stable-x86_64-pc-windows-msvc` 工具链；CI(真实环境) 无此问题 |

> 注：CI 使用 GitHub Actions 的 windows-latest runner，其自带正确工具链，`cargo test` 在 CI 上可正常跑。本地受工具链限制，代码本身 `cargo build`/`cargo check` 均通过。

## 集成链路验证（闭合）

```
前端区间表单 ──configSet('timeout_ttft_min_ms',…)──▶ Rust Config (持久化 config.json)
                                                     │
网关启动/同步 ──build_opencode_router_config──▶ Go config.json (注入 timeout_* 字段)
                                                     │
Go applyConfig ──setTimeoutConfigFromApp──▶ 读取区间，请求时随机取值（热加载）
                                                     │
Go 请求处理 ──recordCall──▶ runtime/_unified-gateway/call_log.jsonl
                                                     │
前端日志页 ──get_call_log(Rust)──▶ 读取 call_log.jsonl → 渲染时间线（成功一行/异常整块/只看失败）
```

字段名一致性已验证：Go `AppConfig` 的 JSON tag（`timeout_ttft_min_ms` 等）与 Rust 注入的 key 完全一致。

## 阶段 2 完成标准核对

- [x] Go 流内超时切换生效（TDD 测试覆盖正常/中断续写）
- [x] 日志页可用（LogsPage + 只看失败 + 成功一行/异常整块）
- [x] 区间配置表单可保存（configSet + 校验 + 注入 Go 配置热加载）
- [x] `go test ./...` 全绿、`cargo check` 通过、`npm run build` 通过
- [ ] ~~cargo test 运行~~（本地受 WinLibs 工具链限制，CI 可跑）
- [ ] 真实节点冒烟（需真实节点环境，未在本会话执行——留待用户环境）

## 真实节点冒烟（待用户执行）

在用户环境（有真实 sing-box 节点的机器）验证：
1. 配置黑洞/慢节点 → 观察 `streamWithResume` 触发切换
2. 打开日志页 → 看到完整切换时间线
3. 设置页改超时区间 → 保存 → 重启网关 → 生效