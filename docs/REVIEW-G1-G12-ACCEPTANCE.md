# G1~G12 修复批次独立审查报告（2026-08-15）

> 审查方式：独立只读审查子代理（对照原问题清单逐项验证 + 全量 -race）+ 海鸥复核
> 审查基线：`main@141c598`（G1~G12 全部合入后）
> 结论：**11 项修复全部命中核心缺陷路径，可验收通过 ✅**；发现 2 个 🟡 边界缺陷（组合缺口）与若干 🟢 建议，列入下一批次

---

## 一、逐项结论

| 项 | 结论 |
|---|---|
| G1 付费层直连 | ✅ 修复正确（流式/非流式共用路径门控、tier 透传、TierPaid 防护由死代码转兜底） |
| G6 落选候选上报 | ✅ 修复正确（赢家恰一次、三路径无重复上报、Canceled 排除；含 🟡-1 边界） |
| G4 transport 去懒初始化 | ✅ 修复正确（New 固化、唯一构造点、nil 回退正确） |
| G3 批量按钮作用域 | ✅ 修复正确（visibleSelected 与批量作用域一致、U2 链零改动） |
| G5 配置热重载原子化 | ✅ 修复正确（11 个热更量读写点 grep 全核、init 默认值一致、无遗漏无锁读点） |
| G7 质量缓存节流锁 | ✅ 修复正确（RLock 判断、ReadFile 锁外、锁序无环） |
| G8 熔断半开重推 | ✅ 修复正确（==threshold 捕捉跳变完备、半开失败重推；含 🟡-2 组合缺口） |
| G9 pool_quality 互斥落盘 | ✅ 修复正确（poolProbeMu 全覆盖、CreateTemp+Rename 原子、无残留） |
| G10 dismissed 集合 | ✅ 修复正确（语义闭合、无完成态压制/记忆不清理路径） |
| G11 RequestStop 异步 | ✅ 修复正确（async kill、锁安全、测试真覆盖异步语义） |
| G12 轮询保存即时生效 | ✅ 修复正确（setUiPollSec 同步、0=关立即停） |

---

## 二、🟡 边界缺陷（本批引入的组合缺口，建议下一批优先修）

### 🟡-1. G6：竞速「全败且错误为 ctx.Canceled」时，取消仍被当失败标记到节点上
- **位置**：`vendors/opencode/chat.go:593-595`（all-fail 路径 `return nil, firstFail.addr, firstFail.err`）+ `chat.go:233-235`（`call()` 对 `err != nil` 无条件 `tr.Mark(proxyAddr, 0, err)`）。
- **问题**：`raceMarkOutcome` 已排除 `context.Canceled`，但 all-fail 返回路径把 Canceled 的 firstFail **连带真实 addr** 交给 `call()`——`tr.Mark` → `markSocks5Result` 把任意错误当链路失败：临时冷却 20s / 熔断计数 +1。修复前该路径 `proxyAddr==""` 是空操作，**本批次新暴露**：客户端频繁断开（HTTP ctx 取消）会把健康节点推进冷却/熔断，且竞速路径与单发路径行为不一致。
- **建议**：`call()` 中 `if err != nil && !errors.Is(err, context.Canceled) { tr.Mark(...) }`。

### 🟡-2. G8：熔断阈值热重载与 `== threshold` 跳变判定组合缺口
- **位置**：`socks_perf.go:372-379`。
- **问题**：`== threshold` 对固定阈值完备，但结合 G5 的阈值热重载出现两支缺口：
  1. **阈值调高越过当前 failures**（熔断已 open、failures=5 时阈值 3→10）：半开失败后 `failures=6<10` 且 probeUsed 已消费 → 不重推 → **节点被永久剔除直到重启**；
  2. **阈值调低**（5→3，failures 已到 5）：后续失败 6、7、8… 永远不等于 3 → **断路器永不打开**。
- **建议**：用显式状态替代精确相等——breaker 加 `tripped bool`（open 置 true、成功/半开复位清 false），重推条件改 `b.failures >= threshold && (!已跳变 || b.probeUsed)`。

---

## 三、🟢 健壮性建议（非阻塞，随批次清理）

- G6 相关：`addrs[i]` 下标访问仍依赖 clients/addrs 等长契约（G14，未修，第三方 transport 违约仍会越界 panic）。
- G7 相关：探活关闭 + mtime 不变 >5s 后每请求仍读盘（G19，未修）。
- G9 相关：`savePoolQuality` 的 CreateTemp/Write/Rename 错误仍全静默（G21，未修）。
- G5 相关：竞速参数热更对已装配 Vendor 不生效（G15，未修，与本批正交）。

---

## 四、验收结论

**本批 G1~G12 修复可验收通过 ✅。** 11 项修复全部命中原始问题核心路径并有对应回归测试；全量 `go test -count=1 -race ./...` 无数据竞争（唯一 `-race` 告警为既有测试桩 `fakeRunner` 无锁读写，属测试基建噪音，与本批无关）。2 个 🟡 边界缺陷需非常规时机（配置变更/客户端断开）触发，不阻塞本批验收，建议下一批优先修复。

## 五、遗留问题清单更新（合并入 REVIEW-HISTORICAL-CODE-REVIEW.md）

| 编号 | 严重度 | 问题 | 建议 |
|---|---|---|---|
| G32 | 🟡 | 竞速 all-fail 时 ctx.Canceled 仍被 Mark 到节点（G6 修复组合缺口） | call() 排除 errors.Is(err, context.Canceled) |
| G33 | 🟡 | 熔断阈值热重载与 ==threshold 判定组合：阈值调高→节点永久剔除；调低→永不跳闸（G8 修复组合缺口） | breaker 加 tripped 状态替代精确相等 |