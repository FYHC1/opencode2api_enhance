# 代理出口模式实现方案（Clash 代理出口 + DoH + 直连 IP）

> 本文档记录「开 Clash 不开系统代理」模式的验证过程与实现方案。
> 目标：解决本机裸连 IP 被 OpenCode 上游限流导致节点批量扫描/实例启动失败的问题。

---

## 一、问题背景

### 1.1 现象

批量扫描 CF 优选 IP 节点时，全部节点报「请求失败: 读取 HTTP POST 响应失败」或「HTTP 0」，可用率为 0%。

但单独测试某个节点（如 CF-04-104.16.34.174）时，返回 `ok`（可用，免费模型最小请求成功，延迟 ~7.6s）。

### 1.2 诊断

| 测试方式 | 结果 | 结论 |
|---------|------|------|
| 批量扫描（concurrency=8/12） | 0/55 可用 | 上游限流 |
| 单节点测试（concurrency=1） | 2/55 可用 | 节点本身链路 OK |
| 系统代理状态 | ProxyEnable=0 | 排除系统代理干扰 |
| 裸连 curl OpenCode 上游 | 批量高频触发 429/EOF | 本机出口 IP 被限流 |

**结论**：本机裸连出口 IP 在 OpenCode 上游被风控/限流。批量扫描时同一出口 IP 高频请求触发风控，导致全部失败；单节点低频测试可通过。

---

## 二、验证过程

### 2.1 开 Clash 不开系统代理

系统代理保持关闭（`ProxyEnable=0`），启动 mihomo 内核（Clash Verge 自带）：

```powershell
$confDir = "$env:APPDATA\io.github.clash-verge-rev.clash-verge-rev"
Start-Process "C:\Program Files\Clash Verge\verge-mihomo.exe" `
  -ArgumentList "-d `"$confDir`"","-f `"$confDir\clash-verge.yaml`""
```

外部控制确认：

```powershell
Invoke-WebRequest "http://127.0.0.1:9097/version" `
  -Headers @{ Authorization = "Bearer…3456" }
# → {"meta":true,"version":"v1.19.29"}
```

### 2.2 通过 Clash 代理访问 OpenCode 上游

```powershell
curl.exe -x http://127.0.0.1:7897 `
  "https://opencode.ai/zen/v1/models" `
  -H "x-opencode-session: test"
# → http_code=200 time=3.79s
```

**结果：通过 Clash 代理访问上游返回 200**，证明 Clash 节点出口 IP 未被限流。

### 2.3 opencode2api 直连 Clash 代理验证

构造 opencode2api 配置，`active_socks5` 指向 Clash mixed-port（7897）：

```json
{
  "socks5_proxies": [{ "addr": "127.0.0.1:7897", "name": "clash" }],
  "active_socks5": "127.0.0.1:7897",
  "model_alias": { ... },
  "show_node_prefix": true
}
```

启动 opencode2api：

```powershell
opencode2api.exe -config oc-clash-test.json -password test123 -port 19200
```

验证：

```powershell
Invoke-WebRequest "http://127.0.0.1:19200/v1/models" `
  -Headers @{ Authorization = "***" }
# → 200, count=9（9 个免费模型）
```

**结论：opencode2api 通过 Clash 代理出口访问 OpenCode 上游完全正常。**

### 2.4 sing-box detour 方案（待验证/有坑）

尝试 sing-box 配置：节点 outbound 加 `detour: "clash"`，让节点连接经过 Clash 代理。

- `http` outbound 指向 127.0.0.1:7897：报 EOF（sing-box http outbound 对 vless+ws+tls detour 兼容性差）
- `socks` outbound 指向 127.0.0.1:7897：待验证

**坑点记录**：
- Clash 字段 `client-fingerprint` 不是 sing-box 合法字段（sing-box 用 `tls.utls.fingerprint`）
- Clash 字段 `network: ws` 不是 sing-box 合法字段（sing-box 用 `transport: { type: "ws", path, headers }`）

---

## 三、实现方案

### 3.1 方案选型

| 方案 | 改动点 | 优点 | 缺点 |
|------|--------|------|------|
| **A. opencode2api socks5 直连 Clash** | 改 `opencode_cfg.rs` 的 `active_socks5` | 最简单，已验证可行 | 所有实例共享 Clash 单一出口 |
| **B. sing-box detour 上游 Clash** | 改 `singbox.rs` 加 clash outbound + detour | 保留每实例独立 CF IP | detour 兼容性待验证（http 报 EOF） |
| **C. Clash selector 切节点 + 全局 socks5** | A + Clash 外部控制切节点 | 兼顾出口多样性与简单性 | 需要外部控制轮询切换 |

**推荐组合：A + C**（MVP 先实现 A，再加 C 的 selector 切换）。

- MVP（A）：设置里加「上游代理」配置项（如 `http://127.0.0.1:7897`），opencode2api 的 `active_socks5` 直接指向它，跳过 sing-box。
- 进阶（C）：配合 Clash 外部控制，每个实例启动前切换 selector 到不同节点，实现出口 IP 分散。

### 3.2 代码改动点

#### 3.2.1 配置结构（config.rs）

新增配置项：

```rust
pub struct Config {
    // ... 现有字段 ...

    /// 上游代理出口（如 http://127.0.0.1:7897 或 socks5://127.0.0.1:7897）
    /// 配置后 opencode2api 的 active_socks5 直接指向它，跳过 sing-box
    pub upstream_proxy: Option<String>,

    /// 是否启用 DoH（sing-box DNS 配置用）
    pub doh_enabled: Option<bool>,

    /// DoH 服务器（默认 https://1.1.1.1/dns-query）
    pub doh_server: Option<String>,

    /// 直连 IP 模式：跳过 DNS 解析，直接用 IP 连接
    pub direct_ip_mode: Option<bool>,
}
```

#### 3.2.2 opencode2api 配置生成（opencode_cfg.rs）

`build_opencode_config` 逻辑调整：

```rust
pub fn build_opencode_config(singbox_port: u16) -> Result<String> {
    let cfg = crate::config::Config::load().unwrap_or_default();

    // 新增：若配置了上游代理，active_socks5 直接指向上游代理（跳过 sing-box）
    let active_socks5 = if let Some(ref upstream) = cfg.upstream_proxy
        .filter(|s| !s.trim().is_empty()) {
        upstream.trim().to_string()
    } else {
        format!("127.0.0.1:{}", singbox_port)
    };

    let config = json!({
        // ... 现有字段 ...
        "socks5_proxies": [{ "addr": active_socks5, "name": "upstream" }],
        "active_socks5": active_socks5,
        // ...
    });
    // ...
}
```

**注意**：socks5_proxies 的 addr 格式。opencode2api 的 Go 代码里 socks5 拨号器是否支持 http:// 前缀？需要确认。如果不支持 http://，需要解析出 host:port，或要求用户填 socks5://。当前 Clash mixed-port 同时支持 socks5 和 http，建议统一用 `socks5://127.0.0.1:7897` 格式，解析时去掉 scheme。

#### 3.2.3 sing-box 配置生成（singbox.rs）

新增 DoH 与直连 IP 支持：

```rust
pub fn build_singbox_config(node: &ClashNode, listen_port: u16) -> Result<String> {
    let cfg = crate::config::Config::load().unwrap_or_default();
    let outbound = build_outbound(node)?;

    // DNS 配置（DoH）
    let dns = if cfg.doh_enabled.unwrap_or(false) {
        let server = cfg.doh_server.as_deref()
            .unwrap_or("https://1.1.1.1/dns-query");
        json!({
            "servers": [{ "tag": "doh", "address": server }],
            "strategy": "ipv4_only"   // 直连 IP 模式：只解析 IPv4
        })
    } else {
        json!(null)
    };

    let mut config = json!({
        "log": { "level": "warn", "timestamp": true },
        "inbounds": [{ "type": "socks", "listen": "127.0.0.1", "listen_port": listen_port }],
        "outbounds": [outbound, { "type": "direct", "tag": "direct" }],
        "route": { "final": "proxy" }
    });

    if !dns.is_null() {
        config["dns"] = dns;
    }
    // ...
}
```

**直连 IP 模式**：sing-box 的 `dns.strategy: ipv4_only` + 节点 server 是 IP 时自然跳过解析（sing-box 对 IP 地址不做 DNS）。主要是配合 DoH 避免本地 DNS 污染影响非 IP 节点（域名节点）。

#### 3.2.4 Tauri command（commands.rs）

新增/修改：

```rust
/// 设置上游代理（空字符串清除）
pub fn config_set_upstream_proxy(proxy: String) -> Result<(), String> { ... }
```

#### 3.2.5 前端设置页（SettingsPage.tsx）

新增「代理出口」区块：

- 上游代理输入框（placeholder: `http://127.0.0.1:7897` 或 `socks5://127.0.0.1:7897`，留空 = 直连）
- DoH 开关 + DoH 服务器输入框
- 直连 IP 模式开关
- 说明文案：「配置上游代理后，实例流量将走代理出口（如 Clash），绕过本机裸连 IP 限流」

#### 3.2.6 HTTP 路由（server.rs）

`config_set` 已支持任意 key，无需新增路由。前端调用 `api.configSet('upstream_proxy', value)` 即可。

---

## 四、使用流程（实现后）

1. 启动 Clash Verge（或 mihomo 内核），确保外部控制端口（默认 7897 mixed-port）可用
2. **不开系统代理**（Clash Verge 的「系统代理」开关保持关闭）
3. 打开 opencode2api 管理器 → 设置 → 代理出口
4. 上游代理填 `http://127.0.0.1:7897`（或 socks5://）
5. 保存。之后所有实例的流量经 Clash 节点出口访问 OpenCode，绕过本机裸连 IP 限流
6. 节点扫描页正常扫描，不再因限流全部失败

---

## 五、待验证事项

1. **sing-box detour socks5 outbound**：是否兼容 vless+ws+tls（http outbound 报 EOF，socks5 待验证）
2. **opencode2api socks5_proxies 是否支持 http:// scheme**：当前验证用的是 socks5（Clash mixed-port 同时支持），需确认 Go 端拨号器对 scheme 的处理
3. **Clash selector 切换节点实现出口分散**：通过外部控制 API `PUT /proxies/CF-Selector { "name": "CF-Opt-01" }` 切换节点，每个实例启动前切换
4. **DoH 对延迟的影响**：DoH 解析增加首次连接延迟，但可避免本地 DNS 污染

---

## 六、相关文件

- `src-tauri/src/singbox.rs` — sing-box 配置生成
- `src-tauri/src/opencode_cfg.rs` — opencode2api 配置生成
- `src-tauri/src/config.rs` — 配置结构
- `src-tauri/src/commands.rs` — Tauri command 层
- `src/pages/SettingsPage.tsx` — 设置页 UI
- `src-tauri/src/probe.rs` — 节点扫描（扫描时同样走 active_socks5，自动受益）

---

## 七、验证记录（2026-08-10）

| 验证项 | 命令/方式 | 结果 |
|--------|----------|------|
| 裸连批量扫描 | scan concurrency=8 | 0/55 可用（限流）|
| 裸连单节点 | scan concurrency=1 | 2/55 可用（CF-04、CF-07）|
| 系统代理状态 | ProxyEnable=0 | 已关闭，排除干扰 |
| Clash 代理访问上游 | curl -x 7897 opencode.ai/zen/v1/models | 200, 3.79s |
| opencode2api 直连 Clash | active_socks5=7897 | 200, 9 个免费模型 |
| sing-box detour http | outbound detour→http://7897 | EOF（不兼容）|
| sing-box detour socks5 | outbound detour→socks5://7897 | 待验证 |
