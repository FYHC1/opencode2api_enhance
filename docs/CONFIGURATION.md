# 配置说明

默认配置文件是 `config.json`。首次运行可以从示例复制：

```bash
cp config.example.json config.json
```

## 字段

### `model_alias`

模型别名映射。键是客户端请求的模型名，值是实际传给上游的模型名。

```json
{
  "model_alias": {
    "deepseek-v4-flash": "deepseek-v4-flash-free",
    "mimo-v2.5": "mimo-v2.5-free",
    "ling-3.0-flash": "ling-3.0-flash-free",
    "nemotron-3-ultra": "nemotron-3-ultra-free",
    "north-mini-code": "north-mini-code-free",
    "laguna-s-2.1": "laguna-s-2.1-free"
  }
}
```

### `reasoning_effort_map`

把客户端传入的 `reasoning_effort` 映射到上游可接受的值。

```json
{
  "reasoning_effort_map": {
    "minimal": "low",
    "medium": "medium",
    "high": "high"
  }
}
```

### `force_disable_thinking`

设为 `true` 时，服务会尽量禁用 thinking/reasoning，并从返回中移除 reasoning 内容。

### `socks5_proxies`

SOCKS5 代理列表。

```json
{
  "socks5_proxies": [
    {
      "name": "local",
      "addr": "127.0.0.1:1080",
      "username": "",
      "password": ""
    }
  ]
}
```

### `active_socks5`

启用的代理。

- 空字符串：直连
- 某个 `addr`：固定使用该代理
- `__round_robin__`：在多个代理之间轮询

### `providers`（厂商注册）

每个厂商一个条目：`type`（对应厂商类型）、`id`、`name`、`enabled`、`params`（厂商自定义参数）。
**未配置 `providers` 时自动注册全部内建厂商**（扩增即生效；`custom` 除外——必须带条目参数）；
配置后按配置注册（`enabled: false` 可关闭某厂商）。

```json
{
  "providers": [
    {
      "id": "opencode",
      "type": "opencode",
      "name": "OpenCode",
      "enabled": true
    },
    {
      "id": "windsurf",
      "type": "windsurf",
      "name": "Devin/Windsurf",
      "enabled": true,
      "params": {
        "min_available": 3,
        "quota_threshold": 20,
        "cooldown_seconds": 86400,
        "store_file": ""
      }
    },
    {
      "id": "myglm",
      "type": "custom",
      "name": "智谱 GLM",
      "enabled": true,
      "params": {
        "base_url": "https://open.bigmodel.cn/api/paas/v4",
        "api_key": "sk-...",
        "protocol": "openai",
        "via_proxy": false
      }
    }
  ]
}
```

#### windsurf 池型厂商参数（`params`）

| 参数 | 默认 | 说明 |
|---|---|---|
| `min_available` | **3** | 账号池保持的最小可用号数。**不足时由后台并行补齐（不阻塞用户请求）**：请求前快速检查，可用 ≥1 立即放行，差额由一个后台注册 goroutine 补齐（single-flight 防并发风暴）。配高一些（如 5-10）可支撑更大并发/更多无感换号余量 |
| `quota_threshold` | 20 | 全池最低剩余额度（%）≤ 此值时触发后台预注册新号 |
| `cooldown_seconds` | 86400（24h） | 换号/耗尽后的账号冷却时长，到期自动回池复用 |
| `store_file` | 数据目录下 `windsurf_accounts.json` | 账号库持久化路径（跨重启复用账号，不重复注册） |

#### custom 自定义模型源参数（`params`）

用户自带 key 接入第三方供应商（管理面板「自定义模型」页可视化编辑，以下为配置等价形式）。
**一条 `type: "custom"` 条目 = 一个源，可配多条**；模型在 `/v1/models` 中带 `{id}/` 前缀
（如 `myglm/glm-4.7`），调用时网关自动剥前缀转发上游。

| 参数 | 必填 | 说明 |
|---|---|---|
| `base_url` | ✅ | 上游 API 根地址（含版本路径，如 `https://api.openai.com/v1`、`https://api.anthropic.com/v1`、`https://generativelanguage.googleapis.com/v1beta`；尾斜杠容忍） |
| `protocol` | — | 出站协议：`openai`（默认，OpenAI 兼容）/ `anthropic` / `responses`（OpenAI Responses API）/ `gemini` |
| `api_key` | — | 上游密钥，由网关持有，调用方无需携带（本地无鉴权网关可留空） |
| `via_proxy` | — | `true` 时出站走节点池代理（应对地区限制供应商）；默认 `false` 直连 |

> 注意：以 `_` 开头的 params 键保留给 core 运行时注入（Transport 等），配置中的同名键会被忽略。

**账号池行为**：请求 swe 时——有可用号立即用（绝不等待注册）；池空时同步注册 1 个恢复服务，其余后台补齐；流中报错自动无感换号（需要备用号，由 `min_available` 保证）；失败账号标记 Dry+冷却，冷却结束自动回池。

### `routing`（模型路由）

```json
{
  "routing": {
    "model_provider_map": {
      "swe-1-6-slow": "windsurf"
    },
    "default_provider": "opencode"
  }
}
```

- `model_provider_map`：强制指定某模型走某厂商（同名多厂商时的优先选择）
- `default_provider`：兜底厂商
- 未命中映射时，按聚合器倒排索引找"提供该模型的厂商"，再无则走默认厂商

### 订阅导入边界（2026-08-16 拍板）

订阅导入（节点池页「订阅导入」及后台按间隔自动拉取）**一律只更新节点池**：
拉取订阅节点写入订阅缓存，**不创建任何实例**（不再区分独享/进池/仅节点池导入目标）。
需要使用时，请在节点池勾选节点后手动【设为独享】或【入池】添加实例。
删除订阅时，该订阅分组的节点池节点会一并清除。

## 管理面板

打开 `http://127.0.0.1:8000/` 可进入管理面板。面板可以修改配置、刷新模型和查看 token 统计。

默认管理密码是 `123456`，生产部署必须修改：

```bash
./opencode2api -password "your-strong-password"
```
