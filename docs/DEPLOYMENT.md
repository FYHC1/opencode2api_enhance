# 部署说明

## opencode2api 管理器（Rust 桌面/headless）

> 本章针对本仓库的**管理器**（Tauri + Rust，实例池/网关/节点扫描/订阅/健康巡检）。
> 下方「Docker Compose / 上游 Go 核心」章节属于上游 opencode2api 代理本体，两者可独立部署。

### 1. 桌面模式（Linux）

- 安装发行包（.deb / AppImage）后直接启动，图形界面操作实例池、节点扫描、设置。
- 桌面模式内置本地 HTTP 服务（`127.0.0.1:19090`，端口可用 `OPCODE2API_HTTP_PORT` 覆盖），前端经它取数。
- 开机自启可选（设置页 / 系统自启服务）。

### 2. Headless 模式（无图形界面 / 服务器）

1. 安装二进制（`opencode2api`）到 `/usr/local/bin/`。
2. 准备数据目录并授权：

```bash
sudo install -d -m 0755 /var/lib/opencode2api
sudo useradd -r -s /usr/sbin/nologin opencode2api 2>/dev/null || true
sudo chown -R opencode2api:opencode2api /var/lib/opencode2api
```

3. 安装 systemd 服务：

```bash
sudo install -m 0644 docs/systemd/opencode2api-manager.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now opencode2api-manager
sudo systemctl status opencode2api-manager
```

4. 浏览器访问 `http://<host>:19090`（headless 模式托管打包好的前端 `dist/`，纯浏览器完成全部管理）。

> 前端静态文件位置：启动目录下 `dist/`（release 打包时取 `../dist`，开发目录回退 `./dist`）。

### 3. 配置说明

配置文件 `config.json` 位于数据目录（`OPCODE2API_DATA_DIR` 或系统配置目录 `~/.config/opencode2api-manager/`）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `base_url` | string | 上游 API 基地址 |
| `default_password` | string | 实例默认密码（实例未单独设置时回退 `123456`） |
| `clash_external_url` | string | Clash 外部控制地址 |
| `clash_auth_token` | string | Clash 外部控制密钥 |
| `timeout_ttft_min_ms` / `timeout_ttft_max_ms` | int | 首字超时切换区间（毫秒，0 = 默认） |
| `timeout_silence_min_ms` / `timeout_silence_max_ms` | int | 静默超时切换区间（毫秒，0 = 默认） |
| `failover_probe_min` / `failover_probe_max` | int | 故障转移探测区间（0 = 默认） |
| `call_log_max` | int | 调用日志保留上限 |
| `show_node_prefix` | bool | 对话流是否展示「节点 · 模型」前缀 |
| `gateway_port` | int | 统一网关监听端口（0 = 回退默认） |
| `gateway_key` | string | 统一网关鉴权密钥（空 = 默认 `sk-unified-local`；设置须 ≥8 字符） |
| `http_port` | int | 管理器 HTTP 服务端口（0 = 回退 19090） |
| `subscribe_url` | string | 订阅 URL（空 = 未配置） |
| `subscribe_interval_min` | int | 订阅自动拉取间隔（分钟，0 = 不自动拉取） |
| `health_check_interval_sec` | int | 健康巡检间隔（秒，0 = 关闭巡检） |
| `health_restart_threshold` | int | 连续失败 N 次自动重启（0 = 不自动重启） |
| `log_filter_keywords` | string | 调用日志过滤关键词（逗号分隔） |

环境变量（优先级高于配置文件）：

| 变量 | 说明 |
| --- | --- |
| `OPCODE2API_DATA_DIR` | 数据目录（隔离/服务器部署必设） |
| `OPCODE2API_HTTP_PORT` | 管理 HTTP 端口覆盖（headless 也可用 `serve --port`） |
| `OPCODE2API_SSE_DEBUG` | debug 构建下 SSE 调试开关 |

### 4. 安全

- **网关密钥**：设置页配置 `gateway_key`（≥8 字符），上游客户端请求统一网关时需携带 `Authorization: Bearer <key>`。
- **Headless 监听**：默认监听 `0.0.0.0:19090`（公网可达）。生产环境务必：
  - 前置反向代理（nginx + 可选 TLS）限制来源，或
  - 防火墙放行规则仅允许内网 IP 访问 19090。
- 管理面板含启停实例/清数据等高权限操作，不建议直接暴露公网。

### 5. systemd 运维

```bash
sudo systemctl enable opencode2api-manager   # 开机自启
sudo systemctl start opencode2api-manager
sudo systemctl status opencode2api-manager
journalctl -u opencode2api-manager -f        # 查看日志
```

---

## Docker Compose

项目提供三套 compose 模版：

- `deploy/compose/compose.yml`：单独运行，直连上游。
- `deploy/compose/compose.tor.yml`：通过 Tor SOCKS5 代理访问上游。
- `deploy/compose/compose.warp.yml`：通过 Cloudflare WARP SOCKS5 代理访问上游。

快速启动：

```bash
export OPENCODE2API_PASSWORD="change-me"
docker compose -f deploy/compose/compose.yml up -d
curl http://127.0.0.1:8000/health
```

使用 Tor：

```bash
docker compose -f deploy/compose/compose.tor.yml up -d
```

使用 WARP：

```bash
docker compose -f deploy/compose/compose.warp.yml up -d
```

默认镜像是 `ghcr.io/6kmfi6hp/opencode2api:latest`。如果使用 fork 或私有镜像，设置：

```bash
export OPENCODE2API_IMAGE="ghcr.io/OWNER/opencode2api:latest"
```

更多说明见 `deploy/compose/README.md`。

## 使用 release 二进制

从 GitHub Releases 下载对应系统的包：

```bash
tar -xzf opencode2api_v0.1.0_linux_amd64.tar.gz
cd opencode2api_v0.1.0_linux_amd64
cp config.example.json config.json
./opencode2api -port 8000 -config config.json -password "change-me"
```

## systemd 示例

创建运行目录：

```bash
sudo install -d -m 0755 /opt/opencode2api
sudo install -m 0755 opencode2api /opt/opencode2api/opencode2api
sudo install -m 0644 config.example.json /opt/opencode2api/config.json
```

创建 `/etc/systemd/system/opencode2api.service`：

```ini
[Unit]
Description=opencode2api proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/opencode2api
ExecStart=/opt/opencode2api/opencode2api -port 8000 -config /opt/opencode2api/config.json -password CHANGE_ME
Restart=on-failure
RestartSec=3
User=nobody
Group=nogroup
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now opencode2api
sudo systemctl status opencode2api
```

## 反向代理建议

如果需要公网访问，建议：

- 只暴露 API 路由，管理面板放在 VPN 或内网后面
- 使用 HTTPS
- 在反向代理层加限流和访问控制
- 修改默认管理密码
- 定期备份 `config.json`，按需保留或清理 `stats.json`
