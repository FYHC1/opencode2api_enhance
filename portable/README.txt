# opencode2api 管理器（便携版）使用说明

## 这是什么
一个本地多实例代理管理器。每个实例 = 一个 opencode2api 代理进程 + 一个 sing-box 出口，绑定不同代理节点，用于分散 OpenCode 请求、绕过 IP 限流。

## 使用方法
1. 双击 `opencode2api-manager.exe` 启动（首次运行自动在 exe 旁生成 `bin/` 目录，内含 opencode2api 与 sing-box 子程序）
2. **设置**页填写 Clash 外部控制地址（默认 `http://127.0.0.1:9097`，即 Clash Verge 的 External Controller + 密钥）
3. **节点扫描**页点击「一键扫描全部」，等待扫描完成，勾选「可用」节点
4. 点击「添加选中为实例」→ 确定，生成实例
5. **实例**页点击「启动」，状态变「运行中」，即可用
   `http://127.0.0.1:{实例端口}/v1` 作为 API 地址

## 常用端口说明
- 每个实例的 API 地址：`http://127.0.0.1:{端口}/v1`
- 实例 sing-box 端口 = 实例端口 + 10000
- 关闭窗口时应用会最小化到系统托盘，实例继续运行

## 数据位置
- 配置与实例数据保存在 `%APPDATA%\opencode2api-manager\`
- 运行时日志在各实例目录 `…\runtime\{实例名}\logs\`

## 注意事项
- 本项目不是 OpenAI / Anthropic / OpenCode 官方项目，请遵守上游服务条款
- 多实例共享统计文件，token 用量统计可能交叉（仅影响展示，不影响功能）