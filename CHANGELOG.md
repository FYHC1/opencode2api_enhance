# Changelog

## 0.1.0（桌面化改造）

- 由 Go 代理网关改造为 **Tauri 2 桌面应用**「opencode2api 管理器」（Windows exe，纯桌面）
- 完整迁移 `opencode2api_enhance` 的多实例管理器功能：Clash 外部控制取节点、节点扫描、实例增删启停/批量、sing-box 出口
- 前端重写为 React + Tailwind，参照 Windsurf Account Manager 浅色官网风格：无边框窗口、自定义标题栏、侧边栏三页（实例/节点扫描/设置）、系统托盘常驻
- Rust 后端采用 AM 架构：`main.rs` 薄壳 + `lib.rs`（AppState/command/托盘）+ 功能域模块（clash_yaml/singbox/opencode_cfg/instance/probe/embed/commands）
- 移除：axum Web 服务、CLI、Docker/CI/多平台发布设施、tauri-plugin-shell
- 内嵌 opencode2api 与 sing-box 二进制（`include_bytes!`），运行时自释放
- 配置与数据存 `%APPDATA%\opencode2api-manager\`
- 新增便携打包脚本与使用说明

## Unreleased

- Projectized the provided Go program.
- Added Go module metadata, local build targets, and release packaging script.
- Added CI and tag-driven multi-platform release automation.
- Changed release automation to parallel matrix builds with a final publish job.
- Added README, API, configuration, deployment, release, contribution, and security docs.
- Added build metadata and `-version` flag.