# Phase 2：真实进程管理 + sing-box/opencode2api 集成

> **状态**：规划中
> **For agentic workers**：按 Task 顺序连续执行，每 Task 完成后验证再进入下一 Task，**不中断询问**
> **验证失败处理**：记录失败原因，在下一阶段计划中提及并制定修复方案
> **元规范**：`docs/ai-framework/phased-plan-driven.md`

**Goal**：让 `opencode2api-manager` 真正拉起 sing-box + opencode2api 子进程，每个实例绑定不同代理节点，实现多实例独立运行
**Architecture**：解析 Clash Verge 本地配置 → 生成 sing-box 配置 → 启动 sing-box(SOCKS5) → 生成 opencode2api 配置(active_socks5 指向 sing-box) → 启动 opencode2api
**Tech Stack**：Rust + serde_yaml + std::process（子进程管理）

---

## 关键发现（Phase 1 遗留问题）

1. **Clash 外部控制 API 不返回节点连接凭据**（只有名称、类型、延迟），无法直接生成 sing-box 配置
2. **Clash Verge 本地配置文件包含完整节点信息**：
   - 路径：`%APPDATA%\io.github.clash-verge-rev.clash-verge-rev\profiles\*.yaml`
   - 格式：Clash YAML，`proxies:` 列表含 `server/port/type/password/uuid/sni` 等
   - 示例 trojan 节点：`{name: 🇸🇬 新加坡 G1, server: 139.177.187.106, port: 26150, type: trojan, password: xxx, sni: v.qq.com, skip-cert-verify: true}`
   - 示例 vless 节点：`{name: CF移动优选1, server: ..., port: 2096, type: vless, uuid: ..., tls: true, servername: ..., network: ws, ws-opts: {...}}`
3. **运行环境已就绪**：
   - `bin/opencode2api.exe`（已复制）
   - `bin/sing-box.exe` v1.13.15（已下载）

---

## 当前阶段

| 阶段 | 状态 | 说明 |
|------|------|------|
| Phase 1 | ✅ | CLI 骨架、配置、实例状态机、Clash API 节点列表 |
| **Phase 2** | 🔲 | 真实进程管理 + 配置生成 |
| Phase 3 | ⬜ | 订阅链接解析 + Web 界面 |

---

## File Structure（预期文件）

| 文件 | 操作 | 说明 |
|------|------|------|
| `src/clash_yaml.rs` | 新建 | 解析 Clash Verge 本地 YAML 配置，提取节点凭据 |
| `src/singbox.rs` | 新建 | 从节点生成 sing-box JSON 配置 |
| `src/opencode_cfg.rs` | 新建 | 生成 opencode2api config.json（指向 sing-box 端口） |
| `src/instance.rs` | 修改 | start/stop 真实拉起/终止子进程 |

---

## Task 1：Clash YAML 配置解析

**Files**：`src/clash_yaml.rs`

**为**：读取 Clash Verge 本地配置文件，提取带连接凭据的节点列表

**Steps**：
1. 添加 `serde_yaml` 依赖
2. 实现 `ClashNode` 结构体（name/server/port/node_type/password/uuid/sni/tls/network/ws_opts 等）
3. 实现 `parse_clash_yaml(content) -> Vec<ClashNode>`，支持 trojan / vless / vmess / ss
4. 实现 `find_clash_profiles_dir()`（Windows `%APPDATA%` 路径探测）
5. 实现 `list_local_nodes() -> Vec<ClashNode>`（扫描 profiles 目录合并所有节点）
6. `cargo test` 验证解析逻辑
7. Commit：`✨ feat(rust-client): parse Clash YAML node configs`

---

## Task 2：sing-box 配置生成

**Files**：`src/singbox.rs`

**为**：从选中节点生成 sing-box JSON 配置文件

**Steps**：
1. 实现 `build_singbox_config(node: &ClashNode, listen_port: u16) -> String`（JSON）
2. 支持 trojan outbound：`{type: trojan, server, server_port, password, tls: {enabled, server_name, insecure}}`
3. 支持 vless outbound：`{type: vless, server, server_port, uuid, tls, transport: {type: ws, path, headers}}`
4. 生成配置写到 `runtime/<instance>/config.json`
5. `cargo test` 验证 JSON 生成
6. Commit：`✨ feat(rust-client): generate sing-box config from node`

---

## Task 3：opencode2api 配置生成

**Files**：`src/opencode_cfg.rs`

**为**：生成每个实例的 opencode2api config.json（active_socks5 指向 sing-box 端口）

**Steps**：
1. 实现 `build_opencode_config(instance: &Instance) -> String`（JSON）
2. `active_socks5` = `127.0.0.1:<singbox_port>`
3. 保留模型别名等基础配置
4. 写入 `runtime/<instance>/opencode2api.json`
5. `cargo test` 验证配置生成
6. Commit：`✨ feat(rust-client): generate opencode2api config`

---

## Task 4：真实进程启动/停止

**Files**：`src/instance.rs`

**为**：start/stop 真实拉起 sing-box + opencode2api 子进程

**Steps**：
1. `start_instance`：
   - 生成 sing-box 配置 → `sing-box run -c config.json`（后台进程，记录 PID）
   - 生成 opencode2api 配置 → `opencode2api.exe -port <port> -config config.json -password ...`（后台进程，记录 PID）
   - 等待端口就绪后标记 Running
2. `stop_instance`：
   - kill sing-box 进程
   - kill opencode2api 进程
   - 标记 Stopped
3. 进程输出重定向到 `runtime/<instance>/logs/`
4. 单元测试：模拟启动/停止流程（mock 外部命令）
5. Commit：`✨ feat(rust-client): spawn and manage child processes`

---

## Task 5：端到端验证

**Files**：无（验证用）

**为**：真实启动一个实例并验证请求可用

**Steps**：
1. 启动实例：`instance add --name user1 --port 8088 --node "新加坡 G1"`
2. 启动：`instance start --name user1`
3. 验证 sing-box 进程 + opencode2api 进程在运行
4. `curl http://127.0.0.1:8088/health` → OK
5. `curl http://127.0.0.1:8088/v1/models` → 返回模型列表
6. `curl http://127.0.0.1:8088/v1/chat/completions` → 返回回复
7. 停止：`instance stop --name user1`，验证进程终止
8. 记录验证结果（通过/失败/缺陷）

---

## 验收认证据

| # | 标准 | 通过命令 | 期望 |
|---|------|----------|------|
| 1 | 编译通过 | `cargo build` | exit 0 |
| 2 | 测试通过 | `cargo test` | 全部通过 |
| 3 | 格式检查 | `cargo clippy` | 无 error |
| 4 | 进程启动 | `instance start user1` | sing-box + opencode2api 进程存在 |
| 5 | 健康检查 | `curl :8088/health` | OK |
| 6 | 模型列表 | `curl :8088/v1/models` | 返回模型 |
| 7 | 对话请求 | `curl :8088/v1/chat/completions` | 返回回复 |
| 8 | 进程停止 | `instance stop user1` | 进程终止 |

---

## 交接提示词

```text
从本阶段交接下阶段时，请包含以下信息：
- 当前阶段验收结果（哪些通过、哪些失败）
- 已知的限制或待修复问题
- 下阶段需要关注的兼容性问题
- 构建产物路径（如有）
```

---

## 后续阶段规划

| 阶段 | 说明 |
|------|------|
| Phase 3 | 订阅链接解析（用户输入订阅 URL）+ 多实例 Web 管理界面 |
| Phase 4 | 自动节点切换与故障转移 |
| Phase 5 | 打包发布（cargo install / 二进制分发） |

---

## 备注

- 节点连接凭据来自 Clash Verge 本地配置文件（只读，不写入 git）
- 密钥不进 git：runtime 目录加入 `.gitignore`
- 遵守上游服务条款，仅在有权限的环境中使用
