# opencode2api 管理器

> ⚠️ **仅供学习参考，非授权禁止商用**
> 本项目仅用于个人学习、研究与技术交流。**允许**非商业目的的学习、修改与传播分享；**禁止**任何形式的商业用途（收费服务、盈利产品、打包转售、变相收费等）。详见 [LICENSE](LICENSE)。使用即视为同意许可条款。
## 致谢

感谢[Sujinxin123](https://github.com/Sujinxin123)——本项目 v1.0.0 的「统一网关、免费额度实测健康检查、代理池健康检查、配置热更新、模型必填、同模型重试、批量并行启停」等核心能力均移植自该项目，是本次合并的基石。

特别感谢[FYHC1](https://github.com/FYHC1)——v1.0.2 修复了两个关键问题：sing-box 生成配置含非法 `transport {type:tcp}` 导致扫描时 sing-box 启动即退出，以及统一网关进程工作目录错位导致统计界面读不到网关流量（含按节点拆分 token 统计明细）。这两项修复直接消除了日常使用中的两个「硬伤」：节点扫描后 sing-box 不再一启动就崩，统一网关的 token 消耗在统计界面实时可见、并能按节点下钻到用量明细——每个节点的流量消耗一目了然。

- Go 代理核心源于 [`6Kmfi6HP/opencode2api`](https://github.com/6Kmfi6HP/opencode2api)
- 前端设计样式参考 Windsurf Account Manager



本地**多实例代理管理器**桌面应用（Windows exe）。每个"实例" = 一个 opencode2api 代理进程 + 一个 sing-box 出口，绑定不同代理节点，把 OpenAI / Anthropic / Responses 风格的请求转发到 OpenCode 上游，并可通过多实例 × 多节点分散请求、绕过按 IP 的频率限制。

UI 参照 Windsurf Account Manager 的浅色官网风格：Tauri 2 无边框窗口 + 自定义标题栏 + 侧边栏六页（独享 / 实例池 / 节点池 / 统计 / 日志 / 设置），关闭窗口最小化到托盘、实例继续运行。

> 本项目不是 OpenAI、Anthropic 或 OpenCode 的官方项目。请遵守上游服务条款，并只在你有权限的环境中使用。

## 效果图

![opencode2api 管理器界面](docs/images/screenshot.png)

## 功能

- **独享实例**：增/删/启/停/测试，批量操作；API 地址一键复制
- **实例池**：入池实例聚合到统一网关（一键启停 / 一键重启 / 路由模式 smart·failover·round_robin），网关地址与密钥一键复制
- **节点池**：一键扫描全部节点（经 Clash 外部控制 + 本地 Verge profiles），按分组展示，结果分类（ok / config / socks / tls / upstream / timeout / other）与延迟；勾选节点可直接【入池】或【设为独享】批量添加（节点行点击选中、分组行点击展开收起）
- **多代理节点**：每实例自动生成 sing-box 配置走所选节点（trojan / vless / vmess / shadowsocks / ws），opencode2api 的 SOCKS5 指向 sing-box
- **Clash 集成**：配置 Clash 外部控制地址与密钥即可拉取节点；也可读取 Clash Verge 本地 profiles 目录
- **订阅导入**：节点池支持「从订阅导入」——自动识别 Clash YAML / V2Ray base64 / 明文链接三种格式，容错解码（URL-safe 变体/缺 padding/含换行均可）、节点名 percent-decode（中文/emoji）、公告伪节点过滤、重名去重、IPv6 主机，解析能力对齐 mihomo/v2rayN 等主流客户端（详见 [订阅解析调研](docs/SUBSCRIPTION-RESEARCH.md)）
- **Token 统计**：按实例聚合用量，支持按节点下钻明细；重置统计（可清除已删除节点历史）
- **调用日志**：全流程日志（成功/失败/切换/超时），按天筛选、时段/节点分析、一键清空
- **触摸保活**：系统托盘常驻，关闭窗口实例继续代理
- **设置**：Clash 外部控制、网关超时切换区间、节点前缀展示、开机自启、清除数据、二进制状态
- **性能模式**：链路级主动探活 + 质量加权路由 + 熔断自动恢复——坏节点自动剔除、恢复自动回归，全程无感；另含残留进程一键清理。因对话卡顿而生，一篇想说清楚的[性能模式说明](docs/PERFORMANCE-MODE.md)

## 用法

1. 启动 `opencode2api-manager.exe`（首次运行自动在 exe 旁生成 `bin/` 目录，内含 opencode2api 与 sing-box 子程序）
2. **设置**页填 Clash 外部控制地址（默认 `http://127.0.0.1:9097`）与密钥
3. **节点池**页 →「扫描选中节点」或全选 → 勾选可用 →【入池】聚合到统一网关 或【设为独享】
4. **独享 / 实例池**页 →「启动」→ 用 `http://127.0.0.1:{实例端口}/v1`（独享）或统一网关地址（入池）作为 API 地址

## Linux / Headless 部署

- **Headless（无图形界面 / 服务器）**：同一 Go core 二进制以管理器方式运行即完整 Web 服务（`./opencode2api -port 40000 -password "change-me" -config config.json`），默认监听 `:<port>` 全接口、托管前端 `dist/`，纯浏览器完成全部管理；仅本机访问可加 `-listen 127.0.0.1`（管理 API 含启停实例等高权限操作，公网部署务必配合反向代理限制来源），见 [部署文档](docs/DEPLOYMENT.md)。
- **桌面（Linux）**：安装 .deb / AppImage 即可；桌面模式内置本地 HTTP 服务，前端经它取数，行为与 Windows 版一致。
- 数据目录与配置：`OPCODE2API_DATA_DIR` 隔离数据；`OPCODE2API_MANAGER_PORT` 覆盖管理端口；`config.json` 支持网关端口/密钥、订阅、健康巡检、日志过滤等配置项。

## 轻量化原则

本项目保持轻量、克制的设计取向：

- **少依赖**：不引入图表库、状态管理库等重组件；分析/可视化用纯 CSS 实现，新功能优先用现有技术栈（Tauri command + React + Tailwind）完成。
- **按需添加**：只加有实际使用价值的功能，不为「看起来丰富」堆功能；功能取舍以使用场景为准。
- **体积敏感**：UI 与运行时保持精简，避免无意义的依赖膨胀拖慢启动、增大打包体积。

## 常见问题

使用中遇到问题？先看 [常见问题（FAQ）](docs/FAQ.md) —— 包含 `max_tokens` 超限报错、Token 统计疑问等。

## 构建与打包

依赖：Node.js ≥ 18、Rust（stable-x86\_64-pc-windows-msvc）、Windows 需要 MSVC Build Tools + Windows SDK。`bin/` 下的两个内嵌 exe 不入库，本地构建前需自行准备，见下文「内嵌二进制（bin/）」。

```bash
npm install
bash scripts/build-windows.sh        # WSL 构建 dist，Windows 直接构建 MSVC exe
bash scripts/make-portable.sh        # 组装 dist/opencode2api-manager-<ver>-portable.zip
```

`scripts/build-windows.sh` 是跨 WSL/Windows 的推荐构建入口：前端在 WSL 侧构建，避免 Windows 侧 Vite 配置解析问题；Rust/Tauri 二进制在 Windows 侧直接运行 `build-win.bat` 构建。Windows 产物位于 `src-tauri/target-win-direct/x86_64-pc-windows-msvc/release/opencode2api.exe`。`build-win.bat` 可以单独从 Windows CMD 直接运行；`schtasks` 仅适用于需要后台运行长构建的场景，不是必需步骤。

### 内嵌二进制（bin/）

`bin/` 被 `.gitignore` 忽略，`opencode2api.exe` 与 `sing-box.exe` 均不入库，本地构建前需自行准备：

- `opencode2api.exe`：由本仓库 Go 源码构建（`go build -trimpath -ldflags "-s -w" -o bin/opencode2api.exe .`）
- `sing-box.exe`：从 [sing-box 官方 Release](https://github.com/SagerNet/sing-box/releases) 下载，当前与 CI 一致固定 v1.13.15

远程构建无需手动准备：GitHub Actions 会自动构建 Go 核心、下载 sing-box 并完成打包（见 `.github/workflows/build-release.yml`）。

开发热更：

```bash
npm install
npm run tauri:dev
```

## 数据目录

运行时数据（配置文件、实例清单、日志）存 `%APPDATA%\opencode2api-manager\`：


| 路径               | 说明                                  |
| ---------------- | ----------------------------------- |
| `config.json`    | 应用配置（Clash 外部控制、默认密码）               |
| `instances.json` | 实例清单                                |
| `runtime\`       | 各实例的运行目录与日志                         |
| （exe 旁）`bin\`    | 释放的 opencode2api.exe / sing-box.exe |


## 架构

```
┌─────────────────────────────────────────────┐
│  Tauri 2 前端（React + Tailwind）            │
│  独享/实例池/节点池/统计/日志/设置（HTTP fetch）│
└──────────────────┬──────────────────────────┘
                   │ http://127.0.0.1:<port>/api/admin/*
┌──────────────────▼──────────────────────────┐
│  Go core 管理器（core/manager + main 包）     │
│  实例/网关/节点/扫描/统计/日志/配置 + 协议转换   │
│  vendors/opencode · vendors/windsurf（多厂商）│
└──────────────────┬──────────────────────────┘
                   │ 子进程管理
┌──────────────────▼──────────────────────────┐
│  实例 = opencode2api.exe (Go) + sing-box.exe │
│  用户 → :端口/v1 → opencode2api → sing-box → 节点│
└─────────────────────────────────────────────┘
```

- **Tauri 壳**：只做窗口/托盘/内嵌二进制释放/拉起 core 管理器（`src-tauri/src/lib.rs`），管理职责全部在 Go core
- **Go core**：一份实现服务所有端（桌面 exe / Web 浏览器），经 `/api/admin/*` HTTP 暴露；协议转换（OpenAI/Anthropic/Responses）与厂商契约在 main 包 + `core/contract`
- **多厂商**：`vendors/opencode`（第一厂商）、`vendors/windsurf`（账号池型：无号自动注册/额度预注册/24h 冷却/无感换号）
- **环境隔离**：正式版 / dev（tauri dev）/ 便携测试（portable.txt）各自独立数据目录与**端口槽位**
  （40000 起每环境一段；sing-box = 实例端口 +2000 紧挨），互不干扰，新开环境无需手动配端口
- **端口配置化**：来源优先级 环境变量 > config.json（gateway_port/instance_base_port/probe_*_port）> 编译默认

## 目录结构

```
src/                      # React 前端（TitleBar + 侧边栏 + 六页）
src/lib/api.ts            # 统一 HTTP 对接层（/api/admin/*）
src-tauri/src/            # Tauri 薄壳（窗口/托盘/自启/内嵌释放）
  embed.rs                # 内嵌二进制释放（按内容哈希校验）
  job.rs                  # Windows Job Object（防孤儿进程）
  lib.rs                  # 入口 + 端口注入 + 拉起 core
core/                     # Go 核心层
  contract/               # 厂商契约
  aggregator/             # 多厂商模型聚合
  router/                 # 模型到厂商分发 + failover
  manager/                # 管理域（实例/网关/节点/扫描/统计/日志/配置）
vendors/                  # 厂商层（一厂商 = 一文件夹）
  opencode/               # 第一厂商
  windsurf/               # 第二厂商（账号池型）
bin/                      # 内嵌子程序源（opencode2api.exe / sing-box.exe）
docs/                     # ARCHITECTURE-V2-PLAN.md（唯一事实来源）、FAQ 等
```
