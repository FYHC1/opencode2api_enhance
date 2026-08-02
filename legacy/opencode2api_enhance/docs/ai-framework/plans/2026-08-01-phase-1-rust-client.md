# Phase 1：Rust 客户端

> **状态**：规划中
> **For agentic workers**：按 Task 顺序执行，每 Task 完成后验证再进入下一 Task
> **验收标准**：见文末
> **元规范**：`docs/ai-framework/phased-plan-driven.md`

**Goal**：提供一个 Rust 客户端，用户安装后可直接连接 opencode2api 代理使用，支持 OpenAI Chat Completions 和 Anthropic Messages 格式
**Architecture**：CLI 工具 + 库，支持配置代理地址、API Key、模型选择、流式输出
**Tech Stack**：Rust + clap（CLI 解析）+ reqwest（HTTP）+ tokio（异步）

---

## 前置检查清单

| 优先级 | 文件 |
|--------|------|
| P0 | `docs/ai-framework/phased-plan-driven.md` |
| P0 | 当前项目 README.md |
| P0 | 项目 API 兼容说明 `docs/API.md` |
| P1 | `AGENTS.md`、`CODE_REVIEW.md` |

**分支策略**：基于 `main`，新建 `feat/phase-1-rust-client`
**提交规范**：`<gitmoji><type>(<scope>): <中文描述>`

---

## 当前阶段

| 阶段 | 状态 | 说明 |
|------|------|------|
| 上游 | ✅ | opencode2api Go 代理已运行 |
| **本阶段** | 🔲 | Rust 客户端开发 |
| 下游 | ⬜ | 等待本阶段完成 |

---

## File Structure（预期文件）

| 文件 | 操作 | 说明 |
|------|------|------|
| `Cargo.toml` | 新建 | Rust 项目配置 |
| `src/main.rs` | 新建 | CLI 入口 |
| `src/client.rs` | 新建 | 核心 HTTP 客户端逻辑 |
| `src/config.rs` | 新建 | 配置管理（代理地址、API Key、模型） |
| `src/format.rs` | 新建 | OpenAI / Anthropic 格式转换 |
| `src/stream.rs` | 新建 | SSE 流式输出处理 |

---

## 用户 API 约定

```text
用法：
  opencode2api-client chat --model deepseek-v4-flash-free --message "hello"
  opencode2api-client chat --model deepseek-v4-flash-free --message "hello" --stream
  opencode2api-client models
  opencode2api-client chat --anthropic --model claude-sonnet --message "hello"

配置：
  opencode2api-client config set base-url http://127.0.0.1:8010/v1
  opencode2api-client config set api-key sk-xxx
```

---

## Task 1：项目初始化与依赖

**Files**：`Cargo.toml`、`src/main.rs`

**为**：创建 Rust 项目骨架，定义 CLI 入口和依赖

**Steps**：
1. `cargo init --name opencode2api-client` 初始化项目
2. 在 `Cargo.toml` 中添加依赖：`clap`、`reqwest`、`tokio`、`serde`、`serde_json`、`anyhow`
3. 创建 `src/main.rs`，定义 CLI 结构（`chat`、`models`、`config` 子命令）
4. `cargo build` 验证编译通过
5. Commit：`✨ feat(rust-client): initialize project with CLI skeleton`

---

## Task 2：配置管理

**Files**：`src/config.rs`

**为**：支持配置代理地址、API Key、默认模型

**Steps**：
1. 实现 `Config` 结构体（base_url、api_key、default_model）
2. 支持从环境变量读取（`OPENCODE2API_BASE_URL`、`OPENCODE2API_API_KEY`）
3. 支持从 `~/.config/opencode2api/config.json` 读取
4. 实现 `config set` / `config get` CLI 子命令
5. `cargo test` 验证配置读写
6. Commit：`✨ feat(rust-client): add config management with env and file support`

---

## Task 3：核心 HTTP 客户端

**Files**：`src/client.rs`

**为**：实现与 opencode2api 代理的 HTTP 通信

**Steps**：
1. 实现 `OpenCodeClient` 结构体，封装 reqwest 客户端
2. 实现 `chat_completions()` 方法（OpenAI Chat Completions 格式）
3. 实现 `models()` 方法（获取模型列表）
4. 支持非流式和流式请求
5. 正确处理错误和重试
6. `cargo test` 验证核心逻辑
7. Commit：`✨ feat(rust-client): implement core HTTP client with chat and models`

---

## Task 4：Anthropic 格式支持

**Files**：`src/format.rs`

**为**：支持 Anthropic Messages API 格式输入

**Steps**：
1. 实现 Anthropic → OpenAI 格式转换
2. 支持 `messages`、`model`、`max_tokens`、`stream` 等字段
3. 实现 `anthropic_messages()` CLI 子命令
4. `cargo test` 验证格式转换
5. Commit：`✨ feat(rust-client): add Anthropic Messages format support`

---

## Task 5：SSE 流式输出

**Files**：`src/stream.rs`

**为**：支持流式响应的实时显示

**Steps**：
1. 实现 SSE 解析器，处理 `data:` 行
2. 实时输出文本增量
3. 正确处理 `data: [DONE]` 终止信号
4. 提取并显示 token 用量
5. 端到端测试：`cargo test --test stream`
6. Commit：`✨ feat(rust-client): implement SSE streaming output`

---

## Task 6：构建与发布

**Files**：`Cargo.toml`（更新）、`Makefile`、`README.md`（更新）

**为**：支持多平台构建和用户安装

**Steps**：
1. 添加 `cross` 或 `cargo build` 多目标构建配置
2. 创建 `Makefile` 支持 `make build`、`make test`、`make release`
3. 更新 README.md 添加 Rust 客户端安装和使用说明
4. `cargo test` 全部通过
5. `cargo clippy` 无警告
6. Commit：`🔧 chore(rust-client): add build config and release support`

---

## 验收认证据

| # | 标准 | 通过命令 | 期望 |
|---|------|----------|------|
| 1 | 编译通过 | `cargo build` | exit 0 |
| 2 | 测试通过 | `cargo test` | 全部通过 |
| 3 | 格式检查 | `cargo clippy` | 无警告 |
| 4 | 客户端连接 | `opencode2api-client chat --model deepseek-v4-flash-free --message "hello" --stream` | 返回模型回复 |
| 5 | 模型列表 | `opencode2api-client models` | 返回模型列表 |
| 6 | 配置设置 | `opencode2api-client config set base-url http://127.0.0.1:8010/v1` | 配置持久化 |
| 7 | Git 文件 | `git ls-files` | 仅包含项目文件，无敏感信息 |

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
| Phase 2 | Web 管理面板（如果需要） |
| Phase 3 | 多代理节点自动切换 |
| Phase 4 | 插件系统支持自定义工具 |

---

## 备注

- 本项目不是 OpenAI、Anthropic 或 OpenCode 的官方项目
- 遵守上游服务条款，仅在有权限的环境中使用
- 密钥不进 git
