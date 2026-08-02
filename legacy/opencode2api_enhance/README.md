# legacy/opencode2api_enhance — 历史源码归档

> 本目录是 **`opencode2api_enhance`（旧增强版）的完整源代码归档**，收拢进单仓库保存。
> ⚠️ **仅供历史参考，不参与编译、不用于生产。**

## 来源

- 原目录：`D:\AI_projects\opencode2api_enhance`（独立 Git 仓库，现并入本仓）
- 归档时间：2026-08-02（仓库收拢）

## 内容说明

| 内容 | 是什么 | 状态 |
|---|---|---|
| `main.go` + `*.go` | Go 代理（**旧版**） | ⚠️ 已过时，主仓根目录 `main.go` 才是新母本（含 401 门禁）|
| `src/*.rs` | 早期 Rust CLI 客户端源码 | 部分已迁移至本仓 `src-tauri/src/`（主仓多了 `commands.rs/lib.rs/web.rs`）|
| `src-tauri/`, `build.rs`, `Cargo.toml` | Rust build 配置 | 历史 |
| `docs/` | 文档（API/配置/部署/发布 + AI 工作流） | 参考价值 |
| `scripts/run-multi.bat` 等 | 批量启停脚本 | 参考价值 |

## 重要提醒

1. **`main.go` 务必取主仓根目录的**（`legacy/` 这份是旧版，不要用它编译）。
2. 前端面板（`/login`、`/api/config` 等）在 Go `main.go` 内联 HTML 中，主仓版为准。
3. 归档时已排除：`bin/`（二进制）、`deploy/`、`docker/`、`Dockerfile`、`src-tauri/target/`、`*.log`、实例私有配置（`config.json`、`config-8010/8011/8012.json`）、`stats.json`、`.git`。

如需删除历史包袱，可在确认后清理本目录，主仓功能不受任何影响。