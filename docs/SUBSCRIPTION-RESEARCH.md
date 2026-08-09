# 订阅解析调研：主流代理软件实现对比

> 调研日期：2026-08-09
> 调研对象：mihomo / clash-verge-rev / sing-box(SFA) / v2rayN
> 目的：为 opencode2api_enhance 的 `src-tauri/src/subscribe.rs` 订阅解析做原生适配提供依据。
> 源码克隆于 `/tmp/opencode/research/`（mihomo: Meta 分支, clash-verge-rev/sing-box/v2rayN: 默认分支, 均 depth 1）。

## 0. 适配状态（2026-08-09 已实施）

依据本调研对 `src-tauri/src/subscribe.rs` 完成的原生适配（cargo test 89/89 全绿）：

| 能力 | 实施内容 | 代码位置 |
|------|---------|---------|
| base64 容错解码 | `decode_base64_loose`：去空白/换行、`_`→`/`、`-`→`+`、补 `=` padding、RawStd→Std→UrlSafe 四引擎递进，失败返回 None 由调用方降级明文 | `subscribe.rs::decode_base64_loose` |
| 节点名 percent-decode | `percent_decode`（`%XX`→字节、`+`→空格），`split_fragment` 解码 fragment、`parse_vmess` 解码 ps | `subscribe.rs::percent_decode` |
| 公告/伪节点过滤 | `is_info_pseudo_node`：公告前缀（官网/更新时间/剩余时长…）+ 全角冒号识别，`parse_subscription` 统一过滤 | `subscribe.rs::is_info_pseudo_node` |
| 重名去重 | `dedupe_node_names`：重复名追加 `-02`/`-03` 后缀（mihomo uniqueName 语义） | `subscribe.rs::dedupe_node_names` |
| IPv6 主机 | `split_host_port` 支持 `[2001:db8::1]:443` | `subscribe.rs::split_host_port` |
| 订阅响应头 | `SubscriptionMeta` + `fetch_subscription_with_meta`：解析 `subscription-userinfo`（流量/到期）、`Content-Disposition`（订阅名）、`profile-web-page-url`（暂未接入前端） | `subscribe.rs::parse_subscription_headers` |
| vmess/ss 容错 | vmess body、ss userinfo/legacy 统一走容错解码（URL-safe 变体不再失败） | `subscribe.rs::parse_vmess/parse_ss` |
| Clash YAML / base64 / 明文三格式识别 | 保留，base64 分支改用容错解码 | `subscribe.rs::parse_subscription` |
| **sing-box JSON 订阅** | 新增 `parse_singbox_json`：识别 `{"outbounds":`（明文+base64 两形态），映射 vless/vmess/trojan/ss/hy2/tuic/wg/socks 的 outbound 字段 | `subscribe.rs::parse_singbox_json` |
| **tuic/wg/socks/hysteria(v1) URI** | 新增 `parse_tuic`/`parse_wireguard`/`parse_socks`/`parse_hysteria`；对应 singbox.rs 三个新 outbound（wireguard 走 `private-key`/`public-key`，hysteria 走 `auth-str`，字段名已按 mihomo 规范核对） | `subscribe.rs` / `singbox.rs` / `clash_yaml.rs` |
| **vless reality 参数贯通** | SubscribeNode 新增 `reality_pbk`/`reality_sid`/`client_fingerprint`，URI/singbox JSON/Clash YAML 三路解析均填充，`to_clash_node` 透传到 sing-box outbound | `subscribe.rs::to_clash_node` |

未实施（按需）：附加订阅拼接、subconverter 链、`SubscriptionMeta` 前端展示、socks5 URI 的 UDP 参数、wireguard 多 peer/本地地址。

## 0.1 结论速览（可直接落地的改进点）

| 能力 | mihomo | v2rayN | clash-verge-rev | 我们现状 | 建议 |
|------|--------|--------|-----------------|----------|------|
| base64 容错解码 | ✅ RawStd→Std 递进，失败返原文 | ✅ 去换行/`-_→+/`/去空格/补`=`/失败返空 | 依赖 mihomo | ✅ 已适配（四引擎递进+降级明文） | 已对齐 |
| 节点名 percent-decode | ✅ `url.Parse().Fragment` 自动解码 | ✅ `UriFormat.Unescaped` | 依赖 mihomo | ✅ 已修复 | 已对齐 |
| 公告/伪节点过滤 | ✅ 无 `://` 的行直接跳过 | ✅ scheme 前缀分发，不识别的返回 null | 依赖 mihomo | ✅ 已修复 | 已对齐 |
| 重名去重 | ✅ `uniqueName` 加 `-02` 后缀 | ✅ 自带 | 依赖 mihomo | ✅ 已适配（`dedupe_node_names`） | 已对齐 |
| 订阅响应头 `subscription-userinfo` | ❌(拉取方不管) | ❌ | ✅ 解析流量/到期 | ✅ 已解析（未接前端） | 已对齐 |
| 订阅响应头 `Content-Disposition` 文件名 | ❌ | ❌ | ✅ 解析 | ✅ 已解析 | 已对齐 |
| IPv6 `[::1]:port` 支持 | ✅ `url.Parse` | ✅ `Uri` | 依赖 mihomo | ✅ 已适配 | 已对齐 |
| 协议覆盖 | hysteria/tuic/wg/socks5/anytls… | 全 | 依赖 mihomo | vless/vmess/trojan/ss/hy2/hy1/tuic/wg/socks/anytls | 已对齐主流 |
| vless host 二次 base64 解码 | ✅ | ❌ | 依赖 mihomo | ❌ 未实施 | 边缘，可加 |

## 1. mihomo（MetaCubeX/mihomo，Go）

### 解析入口
`common/convert/converter.go::ConvertsV2Ray(buf []byte) ([]map[string]any, error)`

```go
data := DecodeBase64(buf)                    // 容错解码，失败返回原文
arr := strings.Split(string(data), "\n")
for _, line := range arr {
    line = strings.TrimRight(line, " \r")
    if line == "" { continue }
    scheme, body, found := strings.Cut(line, "://")
    if !found { continue }                   // ← 无 :// 的行（公告/注释/纯文本）天然跳过
    scheme = strings.ToLower(scheme)
    switch scheme { case "hysteria": … case "hysteria2","hy2": … case "tuic": …
                    case "trojan": … case "vless": … case "vmess": … case "ss": … }
}
```

### 节点名解码
- vless/trojan/hysteria2/tuic：`url.Parse(line)` 后取 `url.Fragment`——**Go 标准库 url.Parse 自动对 fragment 做 percent-decode**，中文/emoji 节点名无需手工处理。
- vmess：JSON 解码后取 `values["ps"]`（v2rayN 风格 base64 JSON）或 `handleVShareLink`（Xray VMessAEAD 风格，fragment 同上）。
- 重名：`uniqueName(names, name)` —— 首次 `index=0`，重复则 `name-02`、`name-03`…（注意从 02 开始）。

### base64 容错解码（common/convert/base64.go）
```go
func tryDecodeBase64(buf []byte) ([]byte, error) {
    // 先 RawStdEncoding（无 padding），再 StdEncoding（有 padding）
    n, err := encRaw.Decode(dBuf, buf)
    if err != nil { n, err = enc.Decode(dBuf, buf); if err != nil { return nil, err } }
    return dBuf[:n], nil
}
func DecodeBase64(buf []byte) []byte {   // 容错入口：解码失败返回原文，不报错
    result, err := tryDecodeBase64(buf); if err != nil { return buf }; return result
}
```
即：**先尝试无 padding 的 RawStd，失败再试标准带 padding**；整体解码失败时把原文当明文处理（可能含未编码的 vless:// 行）。

### vless 关键参数映射（common/convert/v.go::handleVShareLink）
- `security` → tls/reality；`fp` → client-fingerprint（缺省 "chrome"）；`alpn`（逗号分隔）
- `sni` → servername；`pbk`+`sid` → reality-opts{public-key, short-id}
- `type`（network，缺省 tcp）；`path`/`host` → ws-opts / http-opts / grpc-opts
- `flow`（converter.go 中 strings.ToLower）；`encryption`
- `packetEncoding`：none / packet(xudp) / 其他默认 xudp=true
- 特殊：`tryDecodeBase64([]byte(urlVLess.Host))` —— 某些分享链接把 host 整体 base64 编码，尝试二次解码。

### 订阅拉取方
mihomo 本身是内核，订阅拉取与转换由上层（clash-verge / Clash Verge Rev 等）通过 proxy-providers / parser 触发。`ConvertsV2Ray` 即 proxy-provider 的 parser 之一。

## 2. v2rayN（2dust/v2rayN，C#）

### 解析入口
`v2rayN/ServiceLib/Handler/Fmt/FmtHandler.cs::ResolveConfig(string config, out string msg)`
按 scheme 前缀分发（`str.StartsWith("vmess://")` / `"ss://"` / `"trojan://"` / `"vless://"` / `"hysteria2://"` / `"tuic://"` / `"wg://"` / …），**不匹配任何前缀 → 返回 null 跳过**（等价于公告/垃圾行过滤）。

### 节点名解码
每协议一个 Fmt 类（`Handler/Fmt/`）：VLESSFmt / VmessFmt / TrojanFmt / ShadowsocksFmt / Hysteria2Fmt / TuicFmt / WireguardFmt …
```csharp
item.Remarks = url.GetComponents(UriComponents.Fragment, UriFormat.Unescaped);  // 显式 Unescaped
item.Address = url.IdnHost;   // IDN 支持（中文域名）
item.Password = Utils.UrlDecode(url.UserInfo);
```

### base64 容错解码（Common/Utils.cs::Base64Decode）—— 最健壮
```csharp
plainText = plainText.Trim()
    .ReplaceLineBreaks("")     // 去所有换行
    .Replace('_', '/')         // URL-safe 变体 → 标准
    .Replace('-', '+')
    .Replace(" ", "");         // 去空白
if (plainText.Length % 4 > 0)
    plainText = plainText.PadRight(plainText.Length + 4 - (plainText.Length % 4), '=');  // 补 padding
Convert.FromBase64String(plainText);
```
`IsBase64String` 用 `Convert.TryFromBase64String` 检测。订阅下载后若 `IsBase64String(result)` 才解码，否则当明文。

### ss:// 双格式（ShadowsocksFmt.cs）
- `ResolveSSLegacy`：正则 `ss://(?<base64>...)(?:#(?<tag>...))?` 提取 base64 → `Base64Decode` → `method:password@host:port`
- `ResolveSip002`：标准 URI 解析，userinfo `UrlDecode` 后含 `:` 直拆，否则整体 Base64Decode 再拆。
- 两者都失败返回 null（不抛错）。

### 订阅拉取（Handler/SubscriptionHandler.cs）
- `DownloadAdditionalSubscriptions`：主订阅若 base64 先解码；`MoreUrl`（逗号分隔附加订阅）逐个下载拼接。
- 下载后 `ProcessDownloadResult`：`result.Length < 99` 时直接当文本显示（提示用户内容太短，可能不是订阅）。
- 支持 subconverter 转换链（`ConvertTarget` → sub-store 类 URL）。

## 3. clash-verge-rev（Rust + Tauri）

### 架构：订阅解析委托给 mihomo 内核
`src-tauri/src/config/prfitem.rs::from_url` 只负责**拉取原始内容 + 解析响应头**，内容格式校验（必须是含 `proxies` 或 `proxy-providers` 的 YAML）后**原样保存**；真正的 base64/URI 订阅转换由内置 mihomo 内核加载时完成（Clash 的 proxy-provider parser 机制）。

### 响应头解析（我们目前缺失的能力）
```rust
// 1. subscription-userinfo：流量/到期
//    (任意前缀 + "subscription-userinfo" 后缀的 header，如 x-amz-meta-subscription-userinfo)
let extra = PrfExtra {
    upload:   help::parse_str(sub_info, "upload").unwrap_or(0),
    download: help::parse_str(sub_info, "download").unwrap_or(0),
    total:    help::parse_str(sub_info, "total").unwrap_or(0),
    expire:   help::parse_str(sub_info, "expire").unwrap_or(0),
};
// 2. Content-Disposition：订阅文件名（filename*= 优先，percent-decode）
// 3. profile-update-interval：刷新间隔（小时→分钟）
// 4. profile-web-page-url：订阅主页
```

### 深链订阅导入（utils/resolve/scheme.rs）
`clash://install-config?url=...&name=...` 深链解析：`extract_subscription_url` 取 `url=` 参数后 `percent_decode_str` **最多两层**解码（防嵌套 URL 双重解码：`Url::parse` 成功即停）。

### 数据流
```
订阅URL → PrfItem::from_url → 存 YAML 文件 → mihomo 内核 proxy-providers 加载
        → base64/URI 订阅由 ConvertsV2Ray 转换 → 节点列表
```

## 4. sing-box（SagerNet/sing-box + SFA）

### 核心：无订阅解析
sing-box 核心只消费**结构化 JSON outbound**，不解析 vless:// 等分享链接。grep 全仓无 `vless://` 解析代码。

### 订阅处理在客户端侧
- `experimental/libbox/remote_profile.go`：仅处理 `sing-box://import-remote-profile?url=...` **深链**（导入 remote profile URL，不做内容解析），fragment 作名称。
- SFA（sing-box-for-android）用预编译 `libbox.aar`（Go→Kotlin 绑定），`app/build.gradle.kts` 引入；订阅内容识别/转换在 SagerNet/box 共享 Go 代码中（libbox.aar 内编译，源码库未公开在本仓库）。
- SFA 的 `ProfileManager.kt`（Kotlin）仅做 profile 元数据 CRUD，内容解析全部下沉到 libbox。

### 对适配的启示
sing-box 系订阅格式（v2ray base64 / Clash YAML / sing-box JSON 三种）转换逻辑在私有库，公开仓库无从直接参考；但其**输出规范**（outbound JSON 结构）即我们 singbox.rs 生成的目标，可反向校验我们的字段映射（reality/flow/sni/pbk/sid/type/path/headerType/packetEncoding/alpn/fp）。

## 5. 对照我们的 subscribe.rs（2026-08-09 适配后）

已具备：percent-decode 节点名、公告伪节点过滤、四种订阅格式识别（Clash YAML / sing-box JSON / base64 / 明文）、容错 base64 解码（四引擎递进+降级明文）、IPv6 主机、重名去重、订阅响应头元信息、vless/vmess/trojan/ss（legacy+sip002）/hy2/hy1/tuic/wg/socks 解析、vless reality 参数（pbk/sid/fp）贯通到 sing-box 生成。

适配状态与剩余差距：

1. ✅ **P0 base64 容错**：`decode_base64_loose`——`_`→`/`、`-`→`+`、去空白/换行、自动补 `=` padding、RawStd→Std→UrlSafe 递进；解码失败返回 None 降级明文解析。
2. ✅ **P0 IPv6 主机**：`split_host_port` 识别 `[2408:...]:443`（`[` 前缀 + `]` 闭合），端口后 query 兼容。
3. ✅ **P1 重名去重**：`dedupe_node_names`（`uniqueName` 语义，重复名追加 `-02`）。
4. ✅ **P1 响应头**：`fetch_subscription_with_meta` 解析 `subscription-userinfo`（流量/到期）、`Content-Disposition`（订阅名）、`profile-web-page-url`；**`SubscriptionMeta` 尚未接入前端 UI**（暂缓）。
5. ✅ **P1 ss:// 双格式**：legacy（整体 base64）与 sip002（userinfo 可能再编码）均覆盖，base64 统一走容错解码。
6. ✅ **sing-box JSON 订阅**：`parse_singbox_json`（明文+base64 两形态），outbound 字段映射 vless/vmess/trojan/shadowsocks/hy2/tuic/wireguard/socks/http；tag percent-decode；reality/utls 指纹提取。
7. ✅ **协议扩展**：tuic / hysteria(v1) / wireguard / socks URI 解析 + singbox.rs 对应 outbound 生成（wireguard `private-key`/`public-key`、hysteria `auth-str` 字段名按 mihomo 规范核对）。
8. ✅ **vless reality 贯通**：SubscribeNode 新增 `reality_pbk`/`reality_sid`/`client_fingerprint`，URI/singbox JSON/Clash YAML 三路解析填充，`to_clash_node` 透传 `reality_opts`。
9. ⏳ **P2 vless host 二次 base64 解码**（mihomo 特有，个别订阅用）——未实施。
10. ⏳ **P2 附加订阅拼接**（v2rayN MoreUrl）、subconverter 链（clash-verge 的 ConvertTarget）——未实施，暂不需要。

## 6. 参考源码位置索引

| 仓库 | 关键文件 |
|------|----------|
| mihomo | `common/convert/converter.go`（ConvertsV2Ray, uniqueName）、`common/convert/v.go`（handleVShareLink）、`common/convert/base64.go`（DecodeBase64） |
| v2rayN | `ServiceLib/Handler/Fmt/FmtHandler.cs`（分发）、`.../Fmt/VLESSFmt.cs` 等（每协议）、`ServiceLib/Common/Utils.cs`（Base64Decode/IsBase64String）、`ServiceLib/Handler/SubscriptionHandler.cs`（拉取/拼接） |
| clash-verge-rev | `src-tauri/src/config/prfitem.rs`（from_url, 响应头解析）、`src-tauri/src/utils/resolve/scheme.rs`（深链+双层解码） |
| sing-box | `experimental/libbox/remote_profile.go`（仅深链）；核心无订阅解析 |
