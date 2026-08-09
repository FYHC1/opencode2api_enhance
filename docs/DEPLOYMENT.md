# 部署说明

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

## 管理器（Web UI）headless 部署（main 分支功能迁移 M7）

同一二进制以管理器方式运行即是完整 Web 服务：直接提供六页管理 UI
（独享 / 实例池 / 节点池 / 统计 / 日志 / 设置）与 `/api/admin/*` API，
无需桌面壳即可在服务器 / 内网使用（需在可执行文件旁放置 `sing-box.exe`）。

```bash
./opencode2api -port 19090 -password "change-me" -config config.json
# 浏览器访问 http://<host>:19090/（需登录）
```

- 默认监听 `:<port>`（全接口）；服务器部署建议显式 `-listen 0.0.0.0`，
  或收紧为 `-listen 127.0.0.1` 仅本机访问。
- 数据目录经 `OPCODE2API_DATA_DIR` 隔离（默认 `<UserConfigDir>/opencode2api-manager`）。

> **安全**：`-password` 非空时 `/api/admin/*` 与前端均要求会话登录；即便如此，
> 管理 API 与实例创建 / 脚本能力并存，**勿直接暴露公网**——建议仅内网使用，
> 或前置 nginx 反代 + IP 白名单 + HTTPS。本项目的 Web 定位保持"单用户 / 内网"。

### systemd 服务模板（Linux / headless）

创建运行目录与数据目录：

```bash
sudo install -d -m 0755 /opt/opencode2api
sudo install -m 0755 opencode2api /opt/opencode2api/opencode2api
sudo install -m 0644 config.example.json /opt/opencode2api/config.json
sudo install -d -m 0755 /var/lib/opencode2api
```

创建 `/etc/systemd/system/opencode2api-manager.service`：

```ini
[Unit]
Description=opencode2api Manager (headless Web UI)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/opencode2api
# headless 默认全接口监听；内网/公网部署显式 -listen 0.0.0.0 并配合防火墙/反代
ExecStart=/opt/opencode2api/opencode2api -port 19090 -password CHANGE_ME -config /opt/opencode2api/config.json -listen 127.0.0.1
Environment=OPCODE2API_DATA_DIR=/var/lib/opencode2api
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now opencode2api-manager
sudo systemctl status opencode2api-manager
```
