# 阶段 W1：opencode 免费通道身份修复 + windsurf 厂商零残留移除

> **状态：** 已完成（代码/构建/vet/全量单测全绿；验收 #10 真实对话复测待上游出口风控解除）
> **For agentic workers:** 按 Task 顺序执行；每 Task 测完再进下一 Task。
> **交接提示词**见文末「给接手 AI 的完整提示词」。
> **元规范:** `docs/ai-framework/phased-plan-driven.md`

**Goal:** ①让本项目的 opencode 免费通道身份串与官方客户端语义一致（12 位十六进制前缀可反解为时间戳），消除 `403 FreeTierError` 的一个确凿成因；②把 windsurf 厂商从代码/测试/配置/文档中完整移除，含成为死代码的 `contract.PoolVendor`，零残留。
**Architecture:** 两件事互不相干，分两个 Task 组串行交付。403 修复只动 `vendors/opencode` 一个包（自包含）；windsurf 移除是横跨 main 包 + `core/contract` + `core/manager` + 文档的删除性重构。
**Tech Stack:** Go 1.x（标准库 `time`/`encoding/hex`/`math/bits`），无新依赖。
**实施档位：** 全能（默认）
**子代理：** 启用（清点/审查用只读子代理；实现由主代理执行）

---

## 前置阅读（必须）

| 优先级 | 文件 |
|--------|------|
| P0 | `docs/ai-framework/phased-plan-driven.md` |
| P0 | 本文件 |
| P0 | `docs/issue-log/OPEN.md`、`docs/issue-log/2026-10-08.md` |
| P1 | `AGENTS.md`、`CODE_REVIEW.md` |
| P1 | `vendors/opencode/opencode.go`、`vendors/opencode/chat.go`、`vendors/opencode/responses.go` |

**仓库路径：** `D:\AI_Projects\opencode2api_guide\opencode2api_enhance`
**基线分支：** 从 `main` 拉 `fix/phase-w1-opencode-403-and-drop-windsurf`

---

## 背景：403 根因取证（本阶段的事实基础）

### 上游 PR 分析（9router，4 个 PR 的取舍）

| PR | 内容 | 本项目是否采纳 | 理由 |
|----|------|----------------|------|
| **#4786** | `translateSessionId()` 的 12 位十六进制前缀由「SHA-256 摘要前 6 字节」改为**真正的倒序时间戳** `~(BigInt(ts)*0x1000n + counter)`；`deriveRequestId()` 的 `msg_` 前缀改为**正序时间戳** `BigInt(ts)*0x1000n + 1n`；UA 1.18.31→1.19.0；另含 `/responses` 加固与 Claude 工具入参归一化 | ✅ **仅采纳第 1 项（ID 时间戳编码）** | 与本项目缺陷同构：我们的前缀是**纯随机**，同样不含时间语义。UA 我们本就自动探测（npm），有自己的下限兜底 |
| **#4787** | `resolveOpencodeSession()` 不再对**已经合法**的客户端原生 `ses_` 做二次翻译（保留客户端会话） | ⏸️ 本阶段不做（列为下阶段候选） | 需要把入站请求头穿透 `core` → `contract.Message.Extra`，改动面超出「必要」；且是否受益取决于下游客户端是否真的发 `x-opencode-session` |
| **#4784** | 免费档模型（id 以 `-free` 结尾，或 `big-pickle`）在**带 key 通道**上强制改用 `Authorization: Bearer public` + 时间戳稳定会话 + `x-opencode-request` | ❌ 不需要 | 本项目 `chatViaVendor*` 对 opencode **一律**强制 `AuthRoutePublic`（`upstream.go:161-166`、`:196-199`），从不透传客户端 key；该 PR 修的是「带 key 却请求免费模型」的场景，我们不存在 |
| **#4782** | 给翻译后的会话加 LRU 缓存（同一输入复用同一 id） | ❌ 不需要 | 本项目会话是**每进程一个**（`ocOnce`），本就不存在「每请求新造 id」的问题；缓存是被 #4786 取代的中间态 |

同时读到的权威上游事实（issue #4780 + PR #4786 描述）：

- 上游 Zen 在 opencode **1.19.0** 之后不再只做正则校验，而是把 12 位十六进制前缀**解码**为标识符时间戳：
  - `ses_` 用 `Identifier.descending("session")` → `~(BigInt(timestamp) * 0x1000n + counter)`（counter=1 时合法 `ses_` 前缀以 `ffe` 结尾）；
  - `msg_` 用 `Identifier.ascending("message")` → `BigInt(timestamp) * 0x1000n + 1n`（counter=1 时合法 `msg_` 前缀以 `001` 结尾）。
- **会话年龄无关（9router 报告，本仓库未独立验证）**：其称合法编码的 30 天前会话仍 200；不含时间语义的摘要/随机前缀被判为「非 OpenCode 客户端」→ `403 FreeTierError`。
- 免费档配额**按会话记账**，故会话必须**稳定**（不能每请求新造）。

### 本项目实测证据

1. **代码缺陷（确凿）**：`vendors/opencode/opencode.go:431`
   `func canonicalID() string { return randomHex(12) + randomBase62(14) }`
   12 位前缀是 `crypto/rand` 纯随机，**不含时间语义**；`chat.go:150-151` 与 `responses.go:276-277` 的 `x-opencode-session` / `x-opencode-request` 都用它。官方同位置是倒序/正序时间戳。
2. **生产日志（2026-10-13）**：`opencode upstream error status=403 FreeTierError` 共 14 次，**全部集中在 12:08 之后**，且报错文案变为新版（含 `Download it at https://opencode.ai and keep building.`）；此前 09-17~10-13 的历史 403 用的是旧文案。说明上游今天中午上线了新的判定。
3. **生产成功率（同日前半段）**：`call_log.jsonl` 中 `space-bunny-free` 167 ok / 13 fail、`big-pickle` 13 ok / 4 fail —— 即新判定是**滚动生效**的，不是一次性全灭。
4. **官方 CLI 对照（关键）**：本机 `opencode serve`（127.0.0.1:4783，Loomy 自带）日志显示
   - 12:08:47 经 provider `oc-local-40080`（= **我们的网关** `http://192.168.12.17:40080/v1`）请求 `big-pickle`，客户端自带**合法**会话 `ses_ecc8d2af1ffe…` 与合法请求 id `msg_13372d58c001…`，仍 403；
   - 13:18~13:22 改走直连上游（provider `opencode`）后报的是 `Rate limit exceeded`（配额类），**不是** FreeTierError。
5. **诚实结论（必须写进交付说明）**：ID 格式偏差是**确凿且必须修**的；但今天这一波 403 里还叠加了**上游侧的配额/出口 IP 门禁**成分（连官方 CLI 也被拒）。因此本阶段修完后**需做真实对话复测**才能判定是否已恢复；若仍 403，则问题不在我们的身份串，而在出口 IP/配额。

**探测过程中发现的环境污染（如实登记）**：为定位根因，本机直连 IP 与生产 WARP 出口（104.28.152.153 / 104.28.160.34）被反复探测过，之后**连官方 CLI 的合法会话也从这些出口拿到 403**。这可能是上游对短时间高频探测的临时风控，需观察其是否自行恢复；**不得**把它误判成本阶段修复的失败。

---

## Global Constraints（冲突时以本节为准）

1. 单测/自动化一律走 `httptest` 随机端口；**本阶段不启动任何真实服务**（AGENTS.md 端口纪律）。
2. 密钥/凭证不进 git。
3. **明确不做（本阶段）**
   - ❌ PR #4787 的「保留客户端原生会话」（需改 core 请求头穿透，列入下阶段候选）
   - ❌ PR #4786 §2 `/responses` 加固（工具名清洗、孤儿 function_call/output 配对、`max_output_tokens` 夹取）
   - ❌ PR #4786 §3 Claude Code 工具入参归一化（`normalizeClaudeToolInput`）
   - ❌ 不改 `minFreeTierVersion` / UA 探测机制（我们本就自动探测 + 高兜底，改它无证据支撑）
   - ❌ 不改动 `src/`、`src-tauri/`、`README.md:14,22` 里纯风格致敬的 "Windsurf" 字样（已核实与厂商无关）
   - ❌ 不改历史归档：`CHANGELOG.md`、`docs/issue-log/2026-08~10-08*.md`、`*.bak`、`config_history.jsonl`、`postmortem/*`、`architecture.md.bak`
4. **Git：** 小步 commit；**默认不 push**。
5. **YAGNI：** 不为「未来可能的账号池厂商」保留 `contract.PoolVendor`（用户已拍板零残留）。

---

## 阶段开头：上阶段遗留

无（本阶段为独立修复任务，非阶段序列的下一棒）。

---

## 跳过项（因档位/范围未做，**非缺陷**）

| 跳过项 | 原因 | 待补做 |
|--------|------|--------|
| PR #4787 等价的客户端原生会话透传 | 用户选择「先做 A」 | ⬜ 待真实对话复测后定 |
| `/responses` 加固与 Claude 工具入参归一化 | 与 403 无关（属 400 类） | ⬜ 待用户确认 |
| 真实服务端到端（启动网关打上游） | 端口纪律 + 今日出口被风控 | ⬜ 待出口恢复后由用户复测 |

---

## File Structure（预期变更）

### Task 组 1（403 修复）

| 文件 | 动作 | 职责 |
|------|------|------|
| `vendors/opencode/opencode.go` | 修改 | `canonicalID()` 拆为 `sessionIDString()`（倒序时间戳前缀）与 `requestIDString()`（正序时间戳前缀）；补编解码工具与注释 |
| `vendors/opencode/chat.go` | 修改 | `x-opencode-request` 改用 `requestIDString()` |
| `vendors/opencode/responses.go` | 修改 | 同上 |
| `vendors/opencode/free_tier_gate_test.go` | 修改 | 现有 `TestCanonicalIDFormat` 改到新函数；新增「前缀可反解为当前时间」断言 |
| `vendors/opencode/identity_time_test.go` | 新建 | 时间戳编解码的往返/边界/单调性单测 |

### Task 组 2（windsurf 零残留）

| 文件 | 动作 | 职责 |
|------|------|------|
| `vendors/windsurf/**`（21 文件，含 `connect/`） | 整目录删除 | 厂商实现与自注册 |
| `models_source.go` | 修改 | 删 blank import（`:19`）、删 `case "windsurf"` 死分支（`:178-179`） |
| `custom_providers.go` | 修改 | `:415` 内建列表 → `{"opencode"}`；`:442` 守卫 → 仅 `opencode` |
| `core/manager/config.go` | 修改 | `:225` 内建列表 → `{"opencode"}` |
| `core/contract/contract.go` | 修改 | 删 `PoolVendor`、`PoolStatus`、`AcctID` 及 `Vendor` 上的相关注释 |
| `upstream.go` | 修改 | 删两处 `v.(contract.PoolVendor)` 断言与 `EnsureReady` 分支 |
| `poolvendor_adapter_test.go` | 删除 | 只测已删接口 |
| `custom_providers_e2e_test.go` | 修改 | 去掉 `!ids["windsurf"]` 断言；`fakePoolVendor` 夹具改中性名/随接口删除调整 |
| `core/manager/custom_propagate_test.go` | 修改 | 去掉 windsurf 子句 |
| `config_merge_test.go`、`auto_model_test.go`、`models_agg_test.go`、`core/router/router_test.go`、`core/aggregator/aggregator_test.go`、`core/aggregator/lastgood_test.go`、`writeheader_panic_test.go`、`upstream_messages_test.go` | 修改 | 假厂商/假模型名换中性名（仅字面量，逻辑不动） |
| `config.example.json` | 修改 | 删 `"swe-1-6-slow": "windsurf"` 路由映射 |
| `docs/CONFIGURATION.md`、`docs/CUSTOM-MODELS.md`、`docs/FAQ.md`、`docs/ROUTING.md`、`README.md` | 修改 | 删/改 windsurf 章节与描述 |

---

## 配置或 API 契约

删除后，`contract` 包的公开接口收缩：

```go
// 删除：PoolVendor / PoolStatus / AcctID
// 保留不变：Vendor、ErrRules、Model、Message、Transport、Racer、Tier、VendorHealth、Register/Create/RegisteredTypes
```

`routing.model_provider_map` 不再接受 `swe-1-6-slow`；老配置若仍写 `windsurf`，行为已确认为**告警日志 + 静默跳过 + 请求回落 opencode**（`models_source.go:134-136`、`custom_providers.go:113-116`、`upstream.go:129-136`），不 panic、不启动失败。

---

## Task 1：给 opencode 身份串补上时间语义（403 主修）

**Files:** `vendors/opencode/opencode.go`、`chat.go`、`responses.go`、`free_tier_gate_test.go`、新建 `identity_time_test.go`

**行为:**

1. 新增常量与函数（`opencode.go` 工具区）：
   - `encodeDescendingID(ms int64, counter uint64) string`：`v := ^(uint64(ms)*0x1000 + counter)`，取低 48 位，大端 6 字节 → 12 位小写十六进制。
   - `encodeAscendingID(ms int64, counter uint64) string`：同上但**不取反**。
   - `sessionIDString() string`：`encodeDescendingID(now, 1) + randomBase62(14)`。
   - `requestIDString() string`：`encodeAscendingID(now, 1) + randomBase62(14)`。
2. 删除 `canonicalID()`；`sessionID()` / `refreshOCSession()` 改用 `"ses_" + sessionIDString()`；`chat.go` / `responses.go` 的请求头改用 `"msg_" + requestIDString()`。
3. 保留「会话每进程稳定」的既有语义（`ocOnce`），仅把前缀从随机改为时间戳——符合上游「配额按会话记账、会话需稳定」。
4. 会话维持**不退化为每请求新建**（不得为了「新鲜时间戳」而丢掉稳定性）。

**Steps:**

1. 改代码。
2. 跑：`go build ./...`
   期望：exit 0。
3. 跑：`go test -count=1 ./vendors/opencode/`
   期望：全绿；新增用例断言 `ses_` 前缀可反解出的毫秒与 `time.Now()` 相差 < 60s，且 counter=1 时前缀以 `ffe` 结尾；`msg_` 同理（升序，counter=1 时以 `001` 结尾）。
4. Commit：`🐛 fix(opencode): 身份串前缀改用时间戳编码，修免费通道 403`

---

## Task 2：补身份串时间的防回归测试

**Files:** `vendors/opencode/identity_time_test.go`、`vendors/opencode/free_tier_gate_test.go`

**行为:**

1. 往返测试：`decode(encode(ms,c)) == (ms*0x1000+c) & 0xFFFFFFFFFFFF`（覆盖 0、`math.MaxInt64/0x1000`、当前时间）。
2. 语义测试：
   - 倒序编码：counter=1 时 `ses_` 前缀以 `ffe` 结尾；
   - 正序编码：counter=1 时 `msg_` 前缀以 `001` 结尾；
   - 反解出的时间戳与当前时间差 < 60s；
   - 正则仍满足 `^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$` / `^msg_...$/`，总长 30。
3. 把 `free_tier_gate_test.go` 的 `TestCanonicalIDFormat` 改为对新函数的格式断言，并保留 500 次循环。

**Steps:**

1. 写测试。
2. 跑：`go test -count=1 ./vendors/opencode/ -run 'Identity|FreeTier|Canonical' -v`
   期望：全部 PASS。
3. 跑：`go test -count=1 ./...`
   期望：exit 0。
4. Commit：`✅ test(opencode): 断言身份串前缀可反解为时间戳`

---

## Task 3：删除 windsurf 厂商目录与登记点

**Files:** `vendors/windsurf/**`、`models_source.go`、`custom_providers.go`、`core/manager/config.go`

**行为:**

1. 删 `vendors/windsurf/` 整目录（含 `connect/`）。
2. `models_source.go`：删 `:19` blank import；删 `vendorParams` 的 `case "windsurf"` 死分支（`:178-179`）。
3. `custom_providers.go:415` 与 `core/manager/config.go:225`：`[]string{"opencode", "windsurf"}` → `[]string{"opencode"}`。
4. `custom_providers.go:442`：冲突守卫改为 `if in.ID == "opencode"`。

**Steps:**

1. 执行删除与改动。
2. 跑：`go build ./...`
   期望：exit 0（导入该包的地方已全部处理）。
3. Commit：`🔥/🐛 fix(vendor): 移除 windsurf 厂商及其登记点`

---

## Task 4：删除死掉的 PoolVendor 契约

**Files:** `core/contract/contract.go`、`upstream.go`、`poolvendor_adapter_test.go`

**行为:**

1. `core/contract/contract.go`：删 `PoolVendor`、`PoolStatus`、`AcctID`。
2. `upstream.go`：删 `chatViaVendor`（`:171-175`）与 `chatViaVendorStream`（`:205-209`）里的 `v.(contract.PoolVendor)` 断言块与相关注释。
3. 删 `poolvendor_adapter_test.go`。

**Steps:**

1. 改代码。
2. 跑：`go build ./...`、`go vet ./...`
   期望：exit 0。
3. Commit：`♻️ refactor(contract): 删除零实现的 PoolVendor 契约`

---

## Task 5：测试夹具换中性名 + 语义断言调整

**Files:** `custom_providers_e2e_test.go`、`core/manager/custom_propagate_test.go`、`config_merge_test.go`、`auto_model_test.go`、`models_agg_test.go`、`core/router/router_test.go`、`core/aggregator/aggregator_test.go`、`core/aggregator/lastgood_test.go`、`writeheader_panic_test.go`、`upstream_messages_test.go`

**行为:**

1. 语义调整（必改）：
   - `custom_providers_e2e_test.go`：去掉 `!ids["windsurf"]`，只断言 `opencode` + 自定义源；
   - `core/manager/custom_propagate_test.go`：去掉 `hasProvider(gps,"windsurf")` 子句。
2. 仅换名（保持覆盖，不删用例）：假厂商 `windsurf` → `aux`，假模型 `swe-1-6-slow` → `aux-model-1`，逐文件保持同一文件内一致。
3. 若某用例断言的是「已删接口」的行为（`poolvendor_adapter_test.go` 已在 Task 4 删），其余一律保留。

**Steps:**

1. 改测试。
2. 跑：`go test -count=1 ./...`
   期望：exit 0，无 skip 掩盖。
3. Commit：`✅ test: 测试夹具去 windsurf 化并调整内建厂商断言`

---

## Task 6：配置与文档去 windsurf

**Files:** `config.example.json`、`docs/CONFIGURATION.md`、`docs/CUSTOM-MODELS.md`、`docs/FAQ.md`、`docs/ROUTING.md`、`README.md`

**行为:** 删除 windsurf 厂商章节/示例/路由映射；`README.md:14,22` 的风格致敬文案**保留**（与厂商无关，已核实）；历史归档不动。

**Steps:**

1. 改文档。
2. 跑：`rg --no-ignore -i windsurf --glob '!node_modules' --glob '!.git' --glob '!dist' --glob '!portable-out' --glob '!src-tauri/target*'`
   期望：仅剩 `src/index.css:4`、`src/components/TitleBar.tsx:8`、`src-tauri/src/lib.rs:16`、`README.md:14,22`、`CHANGELOG.md`、`*.bak`、`config_history.jsonl`、`postmortem/*`、`docs/issue-log/*` 等**白名单**项。
3. Commit：`📝 docs: 移除 windsurf 厂商相关文档`

---

## Task 7：全量回归 + 日志

**行为:** 跑全量 `go test -count=1 ./...` 与 `go build ./...`；更新 `docs/issue-log/2026-10-13.md`（新建）与 `docs/issue-log/OPEN.md`。

**Steps:**

1. 跑全量并记录输出。
2. 删 `poolvendor_adapter_test.go` 后确认没有别的用例依赖该接口。
3. Commit：`📝 docs(issue-log): 记录 403 修复与 windsurf 移除`

---

## 代码审查（阶段级环节，验收前）

**审查方：** 只读子代理（非实现者）
**审查面：** 风格 / 测试完整性 / 依赖与架构红线 / 安全（密钥、越权）/ API 契约一致性

| 审查项 | 结论（✅/⚠️/❌） | 问题清单 |
|--------|------------------|----------|
| 风格 | ⬜ | |
| 测试完整性 | ⬜ | |
| 依赖与架构红线 | ⬜ | |
| 安全 | ⬜ | |
| API 契约 | ⬜ | |

**结论：** ⬜

---

## 验收标准总表

| # | 标准 | 通过条件 | 验证责任人 | 结果 |
|---|------|----------|----------|------|
| 1 | `ses_` 前缀含时间语义 | 单测断言反解时间与 `now` 差 < 60s | 自动化（执行方） | ✅ `TestSessionIDStringCarriesFreshTimestamp`（200 轮 + 计数器范围）PASS |
| 2 | `msg_` 前缀含时间语义 | 单测断言反解时间与 `now` 差 < 60s | 自动化（执行方） | ✅ `TestRequestIDStringCarriesFreshTimestamp`（200 轮）PASS |
| 2b | **前缀时间语义有外部锚点** | 官方 CLI 真实标识符的**日志时间**与「前缀反解时间」差 ≤ 30s（双向独立数据，非自我循环） | 自动化（执行方） | ✅ `TestOfficialIDSamplesMatchLogTimestamps`（4 个真实样本，实测偏差 0~8s）PASS |
| 2c | 同毫秒计数器语义 | 同毫秒自增、跨毫秒归 1；同毫秒内 id 不重复 | 自动化（执行方） | ✅ `TestIDCounterResetsPerMillisecond` / `TestRequestIDStringUniqueInSameMillisecond` PASS |
| 3 | 格式不变 | `^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`、`^msg_...$/`、总长 30 | 自动化（执行方） | ✅ `TestCanonicalIDFormat`（500 轮）PASS |
| 4 | 会话稳定性不回退 | 既有 `TestChatFreeTierSessionAutoInit` 等仍全绿 | 自动化（执行方） | ✅ `ok vendors/opencode`（含 `-race`）PASS |
| 4b | 真实铸造路径带时间语义 | 实际发往上游的头（chat / responses 两路）前缀可反解为当前时间 | 自动化（执行方） | ✅ `assertIdentityFresh` 接入两条铸铁路径 |
| 5 | windsurf 无功能性残留 | `rg --no-ignore -i windsurf` 在**源码**中无功能性引用 | 自动化（执行方） | ✅ 无功能性引用（仅注释提及「已移除」、归档、本地笔记、生成物、UI 风格注释，见下「白名单」） |
| 6 | 编译 | `go build ./...` exit 0 | 自动化（执行方） | ✅ exit 0 |
| 7 | 单测 | `go test -count=1 ./...` exit 0 | 自动化（执行方） | ✅ 10 个包全 ok，exit 0 |
| 8 | 静态检查 | `go vet ./...` exit 0 | 自动化（执行方） | ✅ exit 0 |
| 9 | 老配置兼容 | 文档化「告警+静默跳过+回落 opencode」，不 panic（已由代码路径核实） | 人工（执行方） | ✅ 已核实代码路径并写入 issue-log |
| 10 | 真实对话复测 | 出口 IP 恢复后，用真实客户端打 opencode 免费模型，观察 403 是否消失 | 端到端（用户） | ⬜ **未做**（出口被上游风控，见「跳过项」） |
| 11 | 代码审查 | 审查结论 ✅ 或 ⚠️（问题已登记） | 独立角色 | ⚠️ 有条件通过 —— 审查提出 1 个 ❌（黄金向量测试自我循环）+ 多个 ⚠️，**已全部整改**，见下「独立审查与整改」 |
| 12 | 红线 | `git ls-files` 无敏感文件 | 自动化（执行方） | ✅ 无敏感文件；`git diff main..HEAD` 无密钥字面量 |

### 代码审查发现与整改（独立只读子代理，2026-10-13）

| # | 级别 | 发现 | 整改 |
|---|------|------|------|
| R1 | ❌ | `TestEncodeIDPrefixMatchesOfficialCLI` 是**自我循环**：`realCLISessionMs` 本身由「反解该前缀」得到，而 48 位截断使 `encode(decode(P))==P` 对**任意** P 成立 → 该测试无法证伪任何自洽公式 | 删除该测试，改为 `TestOfficialIDSamplesMatchLogTimestamps`：时间取自官方 CLI **日志行的独立时间戳**（非公式反推），4 个样本实测偏差 0~8s |
| R2 | ⚠️ | 升序 `msg_` 分支**无任何外部锚点**（只有自我循环的指纹/往返） | 新增的两个 `msg_` 真实样本（`133b6a43c001` / `13372d58c001`）+ 日志时间锚点已覆盖 |
| R3 | ⚠️ | 计数器恒为 1（`opencode.go`），同毫秒内多个 request id 共享前缀，与官方「同毫秒自增」语义不符 | 新增 `idCounter`（同毫秒自增、跨毫秒归 1；会话/请求各一个，对应官方两个独立生成器）+ `TestIDCounterResetsPerMillisecond` |
| R4 | ⚠️ | `free_tier_gate_test.go` 的 `validSessionID` 不是时间有效编码，且 `assertFreeTierIdentityHeaders` 只做正则 → 请求路径上的时间语义退化不会被发现 | 夹具换成官方真实会话串；新增 `assertIdentityFresh` 并接入 chat（`AutoInit`）与 responses 两条铸铁路径 |
| R5 | ⚠️ | `upstream.go:5` 注释仍称「EnsureReady 立即收到取消」，但该钩子已随 `PoolVendor` 删除 | 改写该注释 |
| R6 | ⚠️ | `opencode.go` 注释把 9router 说成「上游」（9router 是第三方网关，非 OpenCode 官方仓库）；「会话年龄无关」是借用未验证的断言 | 改为「9router（decolua/9router，第三方网关）」；年龄一句显式标注「本仓库未独立验证」 |
| R7 | ⚠️ | 计划/日志把 48 位截断下的时间断言写成「恒以 ffe / 001 结尾」（只在 counter=1 时成立） | 全部限定为「counter=1 时」，并在代码注释里说明 counter=0xFFF 时为 `fff`/`000` |
| R8 | ⚠️ | 「代码层 0 命中」字面不实（注释/路径中仍有 windsurf 字样） | 改为「无功能性引用」 |
| R9 | ℹ️ | `docs/ACCEPTANCE-E2E.md` 的 C6 是在已删厂商上执行的 runbook | 该节标注**已作废** |
| R10 | ℹ️ | 前缀编码的大段说明在 `e52cbbe` 后变成 detached 注释，`go doc` 不再呈现 | 已重新挂到 `encodeIDPrefix` 文档注释（首行以函数名开头） |

审查同时确认：编码位运算与溢出边界正确、windsurf/`PoolVendor` 移除在代码层完整且行为安全、`TestRebuildVendorsPreservesNonCustom` 未变成空测试、无新增密钥、分支自身文件 gofmt 干净（其余文件的 gofmt 差异在 `main` 上同样存在，属 CRLF 检出所致）。

### 额外达成的证据

| 项 | 内容 | 证据 |
|----|------|------|
| 真实样本外部锚点（**主证据**） | 4 个官方 CLI 真实标识符前缀 × 其日志行独立时间戳，偏差 0~8 秒：`msg_133b6a43c001`（+0.22s）、`msg_13372d58c001`（+0.78s）、`ses_ecc8d2af1ffe`（+0.91s）、`ses_ecc4d1c39ffe`（+7.8s）；另 `ses_ecc985076ffe` 与 issue #4780 记录的创建时刻（03:56:35Z）分毫不差 | `TestOfficialIDSamplesMatchLogTimestamps` PASS；PowerShell 独立复算（低 48 位 795 天回绕，按 2026-10 选 k） |
| 会话/请求计数器独立 | `ses_ecc8d2af1ffe`（04:08:46）与 `msg_13372d58c001`（04:08:46）同毫秒且**都是 counter=1** → 官方确为两个独立生成器 | 同上反解脚本 |
| 官方 CLI 对照 | 本机 `opencode serve` 日志证明：12:08 它经我们的网关（`oc-local-40080`）携带**合法原生会话**仍 403 —— 即我们的网关**丢弃**了客户端会话，正是 PR #4787 场景 | `~/.local/share/opencode/log/opencode.log` 04:08:47Z / 05:22:51Z |

### 抓包实测结论（2026-10-13 16:00，**翻转了本阶段的一条推论**）

用 `OPENCODE_CONFIG_CONTENT`（不改用户配置）把官方 CLI 原生 `opencode` provider 的 `options.baseURL` 指向本地捕获服务器，抓到官方 1.18.14 的**真实上游请求头**：

```
Authorization: Bearer public
Content-Type: application/json
User-Agent: opencode/1.18.14 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14
x-opencode-client: cli
x-opencode-project: global
x-opencode-session: ses_ecbb5c9b3ffe…      ← 结尾 ffe（降序时间戳）✓
x-opencode-request: msg_1344a3698001…      ← 结尾 001（升序时间戳）✓
Connection: keep-alive
Accept: */*
Accept-Encoding: gzip, deflate, br, zstd
```

| 结论 | 说明 |
|------|------|
| ✅ 本修复的编码**被反向验证** | 官方真实串同样是 `ffe`/`001`、counter=1，与本实现逐位同构 |
| ✅ 官方身份头与本项目**几乎逐头一致** | 我们不缺任何 `x-opencode-*` 头（`x-opencode-ticket` 只用于本地 PTY 端点、值恒 `"1"`，与 LLM 无关） |
| ❌ **今日 403 不能用请求头解释** | 官方头 + 官方**真实会话**原样重放上游，仍 **403**；换 `Accept`、换 body、加 `max_tokens`/`temperature`/system 均 403 |
| ❌ 官方 CLI 本体此刻**也不通** | 不经任何覆盖直连上游 → **卡住 3 分钟无响应**；Node `fetch`(undici) 同头重放 → `fetch failed`；流式请求出现连接级失败（`http=000`） |

**必须记录的一处更正**：本阶段中途我曾据「官方 CLI 打印 OK」推断「官方能过门禁、我们不能，故差异在头」。该判断**错误** —— 那次 `OK` 来自官方 CLI 被我指向的**本地捕获服务器**（恒返 200），并非上游。经复核，官方 CLI 直连上游同样拿不到 200。

**修正后的定位**：今日 403 的剩余可能成因是**上游侧新增的服务端门禁/配额**，或**请求体 / 客户端 TLS·HTTP2 指纹**差异 —— 二者都**不在** 4 个 PR 与本阶段修复的覆盖范围内。本阶段修复的价值在于「消除一处与官方格式的确凿偏差」，**不等于**「消除今日 403」。

### 白名单（有意保留，非残留）

| 类别 | 条目 | 理由 |
|------|------|------|
| 历史归档 | `CHANGELOG.md`、`docs/issue-log/2026-08~10-08*.md`、`docs/EXECUTION-PLAN.md`、`docs/REVIEW-CONCURRENCY.md`、`docs/ACCEPTANCE-E2E.md`、`architecture.md.bak`、`docs/ai-framework/plans/2026-08-31-*.md` | 历史记录，改了反而失真 |
| 生成物 | `config_history.jsonl`（只追加的配置审计流水，`config_trace.go` 只写不读）、`postmortem/*.json(.tmp)` | 删=删审计历史 |
| UI 风格致敬 | `src/index.css:4`、`src/components/TitleBar.tsx:8`、`src-tauri/src/lib.rs:16`、`README.md:14,22` | 指「Windsurf Account Manager 的视觉风格」，前端无任何内建厂商列表（已核实） |
| 本阶段文档 | 本计划文件、`docs/issue-log/2026-10-13.md` | 描述「移除 windsurf」这件事本身 |

---

## 风险与降级

| 风险 | 缓解 |
|------|------|
| 今天 403 的主因若是上游出口 IP 风控而非身份串 | 已在交付说明中前置声明；Task 7 后安排真实对话复测（验收 #10）。**不以「仍 403」否定 ID 修复** |
| 时间戳用毫秒、上游用 BigInt 毫秒 | 严格按 issue #4780 描述 `~(BigInt(ts)*0x1000n+counter)` 实现，并用「官方 CLI 真实标识符 + 其日志行独立时间戳」作外部锚点自证 |
| 会话若每请求重建 → 触发 429 | 保持 `ocOnce` 每进程稳定语义，不引入每请求新会话 |
| 删除 `PoolVendor` 破坏外部扩展 | 已核实仓库内零生产实现；`custom` 的 `PoolStatus` 返回类型不同，不受影响 |
| 老配置含 `windsurf` | 已核实为告警+跳过+回落，不 panic；文档写明 |
| 测试文件被 gitignore（`.gitignore:52 *_test.go`） | 本阶段测试改动**不会进 git**；在交付说明中显式提示，避免误以为已入库 |

---

## 给接手 AI 的完整提示词

你是负责 **opencode2api_enhance** 的实现代理。请**完整执行本阶段**，不要只写方案。

### 基线
- 目录：`D:\AI_Projects\opencode2api_guide\opencode2api_enhance`
- 从 `main` 创建并切换：`fix/phase-w1-opencode-403-and-drop-windsurf`
- 唯一实施计划：`docs/ai-framework/plans/2026-10-13-opencode-403-and-windsurf-removal.md`
- 必读：`docs/ai-framework/phased-plan-driven.md`、`AGENTS.md`、`docs/issue-log/OPEN.md`

### 做
1. Task 1-2：opencode 身份串加时间语义 + 防回归测试。
2. Task 3-6：windsurf 目录、登记点、`PoolVendor` 契约、测试夹具、配置文档全部清干净。
3. Task 7：全量回归 + 写 issue-log。

### 不做
- PR #4787 的客户端会话透传（下阶段）
- PR #4786 的 `/responses` 加固与 Claude 工具入参归一化
- 提交密钥；未授权的 `git push`

### 工作方式
1. 先跑基线 `go build ./...` + `go test -count=1 ./...` 确认干净。
2. **严格按计划 Task 顺序**；每 Task 测试后 commit。
3. 证据优先：完成前必须重跑计划中的验证命令并贴输出。
4. 用简体中文回复进度；代码标识符保持原样。

### 交卷
分支名、提交列表、验收表自评、测试/构建输出、残留风险。

---

## 残留手工验收清单

1. 出口 IP 风控恢复后，用 OpenCode 官方 CLI（`oc-local-40080` → `http://<局域网IP>:40080/v1`）或任意真实客户端，对 `big-pickle` / `space-bunny-free` 发起一次真实对话，确认不再 403。
2. 确认部署配置 `config.json` 里遗留的 `{"id":"windsurf","type":"windsurf","enabled":true}` 条目已被清理（面板不展示该内建项，需手工编辑或重建配置）。
3. 确认 `docs/issue-log/2026-10-08.md` 中「移除 windsurf」条的决策点已闭环并状态改为已关闭。
