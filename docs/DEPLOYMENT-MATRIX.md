# 各端产物与部署矩阵

> 目标：**每个端一份自包含产物，复制到对应平台即可部署，端与端之间无相互依赖**。
> 一键产出：`bash scripts/build-all.sh`（前端只构建一次，供各端复用）→ 全部写入 `dist/<end>/`。
> Windows/macOS/Linux 安装包由 **GitHub Actions** 产出（提交信息含 `CI` 推送到 main 触发），
> 产物在 Actions 页面 artifacts 下载；`build-all.sh` 会为 linux/mac 目录生成占位说明。

| 端 | 产物位置 | 部署方式 | 运行依赖 | 启动方式 | 数据与配置 |
|---|---|---|---|---|---|
| **Web（headless）** | `dist/web/` | 复制整个目录到任意机器（Win/Linux/mac 通用） | **无**（Go 静态编译，纯浏览器管理） | `./opencode2api -port 40000 -password "" -listen 0.0.0.0`，浏览器访问 `http://<IP>:40000` | `OPCODE2API_DATA_DIR` 指定数据目录；端口三件套可环境变量覆盖；公网配反向代理 |
| **Docker** | `dist/docker/` | 复制到服务器 → `docker compose up -d --build` | Docker | 同 Web，容器映射 44000(管理)/44080(网关→容器 40080) | 数据卷 `manager-data` 持久化；升级不丢 |
| **Windows** | `dist/win/*.exe`（NSIS 安装包） | 安装包安装（perMachine）或直接运行 | Windows + WebView2（安装包已内置） | 桌面七页 UI + 托盘 | `%APPDATA%\opencode2api-manager\`（正式版） |
| **Linux 桌面** | `dist/linux/*.deb` / `*.AppImage`（CI 产物） | 安装 deb 或运行 AppImage | Linux 桌面（webkit2gtk） | 桌面七页 UI | 数据目录同 Web 约定 |
| **macOS 桌面** | `dist/mac/*.dmg`（CI 产物） | 打开 dmg 拖入 App；首次右键打开（未公证） | macOS | 桌面七页 UI | 数据目录同 Web 约定 |

## 端与端关系

- **无相互依赖**：Web/Docker/Windows/Linux/mac 各自是完整可部署产物，同一份代码构建，行为一致（同一套七页 UI + core）；
- **共享数据格式**：各端读写同一套 `instances.json` / `config.json` / `runtime/` 结构，数据目录可整体搬迁；
- **混用提示**：同一台机器上多端并存时，用 `OPCODE2API_DATA_DIR` / 端口环境变量隔离（见 `docs/AI-TESTING-GUIDE.md`）。

## 常见问题

- **Q：Web 端要依赖电脑上的 exe 吗？** 不依赖。`dist/web/` 是独立的 Go 二进制 + 前端，任何机器（含无桌面环境）都能跑。
- **Q：Docker 端和 Web 端什么关系？** Docker 端就是 Web 端的容器化封装（同一二进制），多一层容器隔离与持久化卷。
- **Q：各端安装包从哪来？** 本机只能构建当前平台的包（Windows 出 NSIS）；Linux/mac 包由 GitHub Actions 三平台矩阵自动产出（`CI` 提交触发）。
