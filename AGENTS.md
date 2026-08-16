# AGENTS.md — 本仓库 AI 代理工作守则

> 所有在本仓库工作的 AI 代理 / 助手，开工前请先阅读以下文件（按优先级）：

1. **`docs/AI-TESTING-GUIDE.md`**（⭐ 必读）—— 端口与环境隔离避让指南。
   本机可能同时运行正式版（`D:\Program Files\opencode2api\`，生产服务）、dev、便携测试包等
   多个环境。**任何真实服务启动/测试前必须执行 §3 的端口与进程检查，并按 §5 模板做三件套
   环境隔离（`OPCODE2API_DATA_DIR` / `OPCODE2API_GATEWAY_PORT` / `OPCODE2API_INSTANCE_BASE_PORT`），
   禁止占用其它环境端口、禁止 kill 非自己启动的 opencode2api/sing-box 进程。**
   单元测试与自动化 E2E（`go test`）用 httptest 随机端口，安全，随时可跑。

2. **`docs/ARCHITECTURE-V2-PLAN.md`** —— 架构 V2 改造计划（唯一事实来源），含阶段/验收表/
   决策记录。改架构前必读。

3. **`docs/CONFIGURATION.md`** / **`docs/ROUTING.md`** / **`docs/API.md`** —— 配置、路由、API 契约。

## 硬性纪律

- **测试纪律**：每步改动后 `go test -count=1 ./...` 全绿才提交；不触网的 mock/httptest 为准。
- **架构纪律**：厂商特有信息进 `vendors/` 或配置，不写死在 core；core 只认 `core/contract`。
- **UI 纪律（⭐ 全平台唯一界面）**：**七页 UI（独享 / 实例池 / 节点池 / 自定义模型 / 统计 / 日志 / 设置）是
  本项目全平台唯一事实界面**（2026-08-16 决策 #13 新增「自定义模型」页）。不管哪个终端设备（Win exe /
  Web 浏览器 / 未来的 macOS / Linux），不管用什么语言或技术栈开发客户端，界面都必须与 exe 这七个菜单**一致**——
  复用 `src/` 同一套前端，或按该 UI 逐一对应实现；**禁止另起一套不同风格的界面**（历史上有过
  `feature/web-self-service` 分支另做简单页面，已废弃，不要效仿）。新增页面/菜单需先与这七页对齐，
  改动七页布局需显式确认。
- **提交纪律**：一阶段一提交，测绿才推进。
- **环境纪律**：见 `docs/AI-TESTING-GUIDE.md`，红线不可破。
