# opencode2api 测试指南

> 目的：无论谁接手这个项目，都能快速上手测试，且**不污染正式版环境**。
> 核心原则：正式版（40000 槽位段 / Program Files 安装 / 默认数据目录）是**红线**，任何开发测试必须走隔离形态。

---

## 一、环境形态总览

| 形态 | 管理端口 | 网关 | 数据目录 | 用途 |
|---|---|---|---|---|
| **正式版**（NSIS 安装） | 40000 | 40080 | `%APPDATA%\opencode2api-manager` | 生产，勿动 |
| **Portable 测试包**（推荐） | **48200** | 48280 | `%APPDATA%\opencode2api-manager-test` | 日常功能测试 |
| **dev 模式**（tauri dev） | 44100 | 44180 | （dev 独立） | 前端/壳联调 |
| **Docker/Web**（headless） | 借环境变量映射（如 44000→容器 40000） | 44080→40080 | 卷 `manager-data` | 服务器/容器测试 |

槽位规则（`src-tauri/src/lib.rs`）：每槽 4100 宽——base 管理、+80 网关、+90 探针 API、+100~+2099 实例段、+2100~+4099 sing-box 段。

**Portable 判定**：exe 旁存在 `portable.txt` → 自动走 48200 槽 + 独立数据目录。免安装，删文件夹即卸载。

---

## 二、Portable 测试（日常推荐）

### 一条命令生成测试包

```powershell
powershell -ExecutionPolicy Bypass -File scripts\prepare-portable.ps1
```

会完成：编译最新 Go core → 编译 Rust 壳（嵌入 core+sing-box）→ 组装 `portable-out\` 并校验。

生成目录布局（缺一不可）：

```
portable-out/
├── opencode2api.exe      # Rust 壳（内嵌 core + sing-box，运行时自动释放 bin/）
├── WebView2Loader.dll    # tauri 运行时必需（裸 exe 不自动带）
├── portable.txt          # 空标记 → 切 48200 槽 + 独立数据目录
└── bin/
    └── dist/             # 最新前端构建产物（core 静态托管用）
```

### 常用参数

```powershell
# 复用已有编译产物，只重新组装（改前端后最快）
npm run build
powershell ... -SkipCoreBuild -SkipRustBuild

# 自定义输出目录
powershell ... -OutDir "D:\tmp\oc-beta"
```

### 启动与验证

1. 双击 `portable-out\opencode2api.exe`（或浏览器访问 `http://127.0.0.1:48200`）；
2. 健康检查：`curl http://127.0.0.1:48200/health` → 200；
3. 页面资源：`curl http://127.0.0.1:48200/app-icon.png` → 200；
4. 测试完毕：关窗口/托盘退出；删 `portable-out\` 即卸载（数据目录按需删 `%APPDATA%\opencode2api-manager-test`）。

### 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 启动报"缺 WebView2Loader.dll" | 裸 exe 不自动带 dll | 脚本已自动拷贝；手动拷 `src-tauri\target\release\WebView2Loader.dll` 到 exe 旁 |
| 页面报"前端资源缺失" | `bin\dist\` 缺失（cargo 不处理 resources） | 脚本已自动拷 `dist\`；手动拷仓库 `dist\` → `bin\dist\` |
| 配置项空白/不生效 | dev/旧包里的 core 是旧编译（如缺池配置字段） | 用脚本重新编译 core + 壳（勿 -SkipRustBuild） |

---

## 三、dev 模式（前端/壳联调）

```powershell
npm run tauri:dev
```

- 槽位 44100 / 44180，独立 dev 数据目录，不碰正式版；
- 第一次 Rust debug 编译 1~3 分钟；改前端热更新、改壳需重编译；
- 注意：dev 运行时若发现配置字段空白，先重新编译 core（`bin\opencode2api.exe`）再重编译壳，见上方常见问题。

---

## 四、Docker / Web（headless）

```powershell
docker compose build          # 构建镜像（node→Go core+sing-box→alpine）
docker compose up -d          # 启动容器
# 本地宿主访问（避开正式版 40000）：http://127.0.0.1:44000
# 网关：http://127.0.0.1:44080/v1（实例池有运行成员才有响应）
```

- 容器内：管理 40000 / 网关 40080（服务器正式部署端口）；宿主映射 44000/44080 仅为本地避让；
- 数据卷 `manager-data` 挂载 `/data`；**卷属主必须 app(uid 100)**，否则保存配置会"保存失败"（`docker compose up` 已带一次性 chown 处理）；
- Docker Desktop 若反复推出：先 `docker version` 验引擎，再启动应用；
- Web/headless 默认无鉴权启动（`-password ""`），公网部署用反向代理 + 改端口映射。

---

## 五、CI 三平台产物

- 工作流：`.github/workflows/build-release.yml`，三平台矩阵（windows/ubuntu/macos）产出 NSIS / deb+AppImage / dmg 并上传 artifacts；
- **两种触发方式**：
  1. **push main 且提交信息含大写 `CI`** → 构建，产物上传到该 run 的 Artifacts（不发布 Release）；
  2. **push `v*` tag**（如 `git tag v1.3.2 && git push origin v1.3.2`）→ 构建 + **自动发布 GitHub Release**（`softprops/action-gh-release`，自动生成 Release Notes，汇总三平台产物为附件）。
- 产物下载：GitHub → Actions → 对应 run → Artifacts；或 **GitHub → Releases**（tag 触发时）直接下载附件。

### 各平台选用哪个文件（以 v1.3.2 为例）

| 产物文件 | 适用平台 | 说明 |
|---|---|---|
| `opencode2api_xxx_x64-setup.exe` | **Windows** x64 | NSIS 安装包（currentUser 安装），日常桌面使用 |
| `opencode2api_xxx_amd64.deb` | **Linux** Debian/Ubuntu 系（x64） | `sudo dpkg -i` 或 `sudo apt install ./xxx.deb` 安装；桌面版有窗口+托盘，需图形环境 |
| `opencode2api_xxx_amd64.AppImage` | **Linux** 各发行版（x64） | 免安装：`chmod +x` 后 `./xxx.AppImage` 直接运行，无需 root |
| `opencode2api_xxx_amd64.dmg` | **macOS** Intel（x64） | 双击挂载后拖入 Applications；Gatekeeper 拦截时右键→打开 |
| `opencode2api_xxx_aarch64.dmg` | **macOS** Apple Silicon（M 系列） | 同 dmg，为 arm64 架构 |
| `Source code (zip/tar.gz)` | 任意 | 源码归档，非运行产物 |

> Linux 若兼有 `.deb` 与 `.AppImage`：日常用 `.deb`（更规范，享包管理器更新）；想免安装/跨发行版用 `.AppImage`。
> macOS 按芯片选：Intel 用 `amd64`，M 系列用 `aarch64`。

---

## 六、开发测试纪律

每次改动后、提交前：

```powershell
go test -count=1 ./...        # Go 全量（9 包）必须全绿
npm run build                 # 前端构建必须通过
```

- **提交规范**：日常提交走分支 → 合并 main → 推送；提交信息避免误含大写 `CI`（除非故意触发构建）；
- **红线**：不 kill 非自己启动的进程；不碰正式版端口/数据；Portable/dev/Docker 之间存在端口隔离，测试互不干扰；
- **改 Go core 后**：若又要打 exe/portable，必须重编译 core 再重编译壳（壳内嵌编译时的 core），否则功能不生效。

---

## 七、相关文档

- `docs/PERFORMANCE-MODE.md`：性能模式说明（面向用户）
- `docs/DEPLOYMENT-MATRIX.md`：各端部署形态与产物一览
- `docs/HANDOFF.md`：历史交接（框架纪律）