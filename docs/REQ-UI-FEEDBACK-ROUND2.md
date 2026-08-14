# 缺陷/需求合集：U 系列验收反馈（2026-08-15，用户真机走查）

> 来源：用户真机走查反馈（U 系列自动化验收通过后的人工复测）
> 状态：待派工实施
> 关联：`docs/EXECUTION-PLAN.md`（U 系列已合入）、`docs/REVIEW-U-ACCEPTANCE.md`（自动化验收）

---

## 〇、问题总览

| # | 页面/模块 | 问题 | 类型 | 优先级 |
|---|---|---|---|---|
| N1 | 节点池 | 停止扫描交互混乱（按钮闪烁/进度条卡死/stop 描述残留） | 交互重做 | 🔴 高 |
| N2 | 节点池 | 新增「节点扫描配置」折叠面板（扫描并发 8 / 停止并发 4） | 需求 | 高 |
| U2' | 实例池 | 状态筛选改**下拉框**（对齐独享页 select 形态） | 样式修正 | 中 |
| U3' | 独享/实例池 | 「界面刷新」文案修正（各自负责各自，不互相提及） | 文案 | 低 |
| U4' | 日志页 | 无日志时 `TypeError: e is not iterable` | 缺陷 | 🔴 高 |
| U5-① | 日志页 | 错误日志信息显示不全，用 title 展示更多 | 改进 | 中 |
| U5-② | 日志页 | **独享实例仍无日志** | 缺陷 | 🔴 高 |
| U5-③ | 日志页 | Anthropic 兼容协议（/v1/messages）401 找不到 key；且 401 无日志记录 | 缺陷 | 高 |
| E1' | 网关 | curl 无 key 报 invalid api key —— **非 bug（正常鉴权），仅需使用说明** | 澄清 | — |

---

## N1：节点池停止扫描交互重做（🔴 用户原话整理）

**现状问题**（用户复述）：
1. 选择节点 → 点击扫描 → 扫描进行中 → 点击【停止扫描】；
2. 【停止扫描】按钮变成【扫描选中节点】，部分节点出现 "stop xxx" 描述信息；
3. 期间【扫描选中节点】按钮会闪烁——变一下【停止扫描】再变回【扫描选中节点】；
4. 下方扫描进度条 5/10 **卡住不动**——直到用户重新选 1 批新节点再扫描，5/10 才变化。

**根因分析（供实施参考）**：
- 按钮闪烁：`scanning` state 由轮询驱动（`p.status === 'running' || 'stopping'`），
  停止请求发出后 `stopScan` 立即 `setScanning(false)`，但后端还在 stopping，
  轮询又把 `scanning` 拉回 true → 闪烁。
- 进度条卡 5/10：`stopScan` 里 `setScan(null)` 但轮询在 stopping 期间继续 `setScan(p)`（U1 修复后），
  而 stopping 状态下后端不再推进 current → 进度条停在 5/10 显示残留。

**用户要求的交互（以此为准）**：
1. 点击【停止扫描】后 → 按钮立即变【**正在停止中**】（禁用态，不闪烁）；
2. 下方的扫描进度条**停止时立刻消失**（只有扫描中才显示）；
3. 全部停止完成后 → 按钮变回【扫描选中节点】；
4. 三种按钮态（扫描选中节点 / 停止扫描 / 正在停止中）用 `title` 显示**并发数**：
   「扫描选中节点（8 并发）」「停止扫描（4 并发）」「正在停止中…」；
5. 页面增加**设置按钮**，内含折叠面板「**节点扫描配置**」（可展开收起），两项配置：
   - 扫描选中节点：并发数（默认 **8**）
   - 停止扫描：并发数（默认 **4**）

**改动文件**：`src/pages/NodesPage.tsx`（按钮态状态机 + 进度条显隐 + 设置弹窗/折叠面板）
+ 配置项（`scan_concurrency` 已有，新增 `stop_scan_concurrency` 默认 4）。

**验收**：停止后按钮稳定为「正在停止中」不闪烁；进度条立即消失；全停后恢复「扫描选中节点」；
title 显示并发数；设置面板展开收起正常、并发数生效。

---

## N2：节点扫描配置折叠面板

- 位置：节点池页设置按钮 → 折叠面板「节点扫描配置」。
- 内容两项（见 N1-5）：扫描并发（默认 8，复用现有 `scan_concurrency`）、停止并发（默认 4，新增 `stop_scan_concurrency`）。
- 样式对齐 U3 折叠面板约定（border + 标题栏 + ChevronDown，默认收起）。

---

## U2'：实例池状态筛选改下拉框

**现状**：独享页是 `<select>` 下拉框（`InstancesPage.tsx:478`，选项 全部实例/运行中/已停止）；
实例池页做成了**按钮组**（`PoolPage.tsx:751`）。

**要求**：实例池页状态筛选改为**下拉框**，与独享页形态完全一致。

**改动**：`src/pages/PoolPage.tsx` 筛选 UI 从按钮组改 `<select>`（复用独享页样式 class）。

**验收**：两页筛选均为下拉框，交互一致。

---

## U3'：「界面刷新」文案修正

**现状**：实例池页「界面刷新」折叠面板的文字描述**提到独享**（"独享和实例池页面…"之类）。

**要求**：两页各自的界面刷新面板**只描述自己**——实例池页只说实例池、独享页只说独享；
若逻辑无 bug，仅改文案。

**改动**：`src/pages/PoolPage.tsx` / `src/pages/InstancesPage.tsx` 面板描述文字。

---

## U4'：日志页无日志报错 `TypeError: e is not iterable`（🔴 缺陷）

**复现**：日志页无日志（或后端返回空）时报「加载失败：TypeError: e is not iterable」。

**根因（代码级）**：`LogsPage.tsx:241` `setLogs([...recs].reverse())`——
后端无日志时 `getCallLog` 可能返回 `null`/`undefined`（而非空数组），
`[...null]` 展开抛 `TypeError: e is not iterable`；`api.ts:413` `res.json()` 直接透传。

**修复**：`LogsPage.tsx` 加载处防御——`setLogs([...(Array.isArray(recs) ? recs : [])].reverse())`；
建议同时在后端 `/call-log` 无数据时确保返回 `[]` 而非 null。

**验收**：清空日志后进入日志页不报错，显示空态。

---

## U5-①：错误日志信息显示不全

**现状**：日志行错误信息（`err_msg`/`detail`）在列表里被截断/显示不全。

**要求**：用 `title` 属性展示完整信息（悬停可见全文），列表保持紧凑。

**改动**：`src/pages/LogsPage.tsx` 错误行加 `title={完整错误}`。

---

## U5-②：独享实例仍无日志（🔴 缺陷，S4 声称已补）

**现状**：S4 声称日志聚合已含独享实例（`calllog.go` 聚合读取各 Running 独享实例 cwd 下
`call_log.jsonl`），但用户实测**独享实例还是看不到日志**。

**待查**（需实施者排查）：
1. 独享实例 cwd 下是否真的生成了 `call_log.jsonl`？（`InstanceCallLogPath` 路径正确性）
2. 独享实例的 `recordCall` 是否真的被调用？（独享实例是独立 opencode2api 子进程，其写盘路径/配置）
3. 前端聚合读取是否包含独享实例（`Source` 标注是否正确渲染）。

**验收**：独享实例发请求后，日志页可见（含来源标注）。

---

## U5-③：Anthropic 兼容协议（/v1/messages）401 无 key + 无日志（🔴 缺陷）

**现状**：三种入站协议——① OpenAI 兼容（/v1/chat/completions）✅ ② **Anthropic 兼容（/v1/messages）❌ 401 找不到 key** ③ OpenAI Responses（/v1/responses）✅。
且 401 场景**看不到日志记录**（`claude.go` 的 recordCall 可能未在鉴权失败路径调用）。

**待查**（需实施者排查）：
1. `/v1/messages` 的 key 校验逻辑——`apiKeyAuthMiddleware` 应已覆盖（main.go:186），
   但客户端（Anthropic 格式）传的 key 头是 `x-api-key` 而非 `Authorization: Bearer`？
   —— 检查 `auth.go` 是否兼容 `x-api-key` 头，若只认 Bearer 则 Anthropic 客户端天然 401。
2. 鉴权失败（401）时是否记录日志——目前 `recordCall` 可能只在业务层调用，
   鉴权中间件 401 直接返回无日志。**建议：401 也记一条日志**（含客户端 key 前缀/来源）。

**验收**：Anthropic 兼容客户端带正确 key 可通；401 失败有日志记录。

---

## E1'：curl 无 key 报 invalid api key —— 非 bug（澄清）

- `GET /v1/models` 挂 `apiKeyAuthMiddleware`（`main.go:187`），必须带 `Authorization: Bearer <网关密钥>`。
- 用户 `curl http://127.0.0.1:48280/v1/models` 未带头 → 401 是**预期行为**。
- 处理：无需改代码；使用文档/FAQ 注明「访问网关 API 需带 Bearer 密钥」。

---

## 附：派工建议顺序

1. 🔴 U4'（空日志 TypeError，小改防御）→ U5-②（独享日志排查）→ U5-③（Anthropic 401）
2. 🔴 N1+N2（节点池交互重做 + 扫描配置面板，较大）
3. 🟡 U2'（下拉框，小改）
4. 🟢 U3'（文案）、U5-①（title）
