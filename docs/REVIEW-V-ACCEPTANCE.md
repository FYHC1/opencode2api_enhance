# 验收报告：V 系列实施检查（2026-08-15）

> 验收人：海鸥（只读分析，未改代码）
> 验收基线：`main@145a677`（V1 已合入）
> 测试状态：`go test -count=1 ./...` **全绿**（9 包，core/manager 8.9s 含新测试）

---

## 一、总体结论

| 阶段 | 交付 | 状态 |
|---|---|---|
| **V1** 停止扫描并发接逻辑 | ✅ 已合入 `11b1cfe`，实现通过 | ✅ 验收通过 |
| **V2** 全局任务悬浮窗 | ❌ **未交付**——全仓无 `tasks`/`onTask`，`App.tsx` 仍是单 release 面板 | ⚠️ 未做 |

**同事只交付了 V1，V2 没做**（可能理解为"先做 V1"或遗漏）。V1 质量好，V2 需补派。

---

## 二、V1 验收明细（通过 ✅）

**需求落点**：N2 死配置修复 + probeNode 取消支持（`docs/REQ-GLOBAL-TASK-PANEL.md` 第一节）。

### 实现核实
| 要求 | 实现 | 状态 |
|---|---|---|
| `probeNode` 取消支持 | `waitForPortAbort(..., c.isStopping)` 探测中可被停止中断；中断路径 `runner.Kill(sbPID/ocPID)` 清理探针（`probe_node.go:47/77`） | ✅ |
| 停止按并发上限 kill | `interruptProbes()`：semaphore 限流（`limit = stopScanConcurrencyOf(cfg)`），同时最多 kill limit 对，防资源尖峰（`probe.go:168-210`） | ✅ |
| 配置真正生效 | `stopScanConcurrencyOf` 在 `interruptProbes` 使用——**死配置已复活** | ✅ |
| RequestStop 返回统计 | `StoppingCount`/`StoppedCount` 字段 + `interruptProbes` 填值（`probe.go:83-84/206-207`） | ✅ |
| 幂等/登记清理 | kill 后清空 `activeProbes` 登记；probeNode defer 再 kill 幂等无害 | ✅ |
| 测试 | `probe_stop_test.go` 4 个测试：并发上限边界、并发上限回退、中断运行中探针、停止后跳过新节点 | ✅ |

**测试**：`go test` 全绿（含新 `probe_stop_test.go`）；未破坏既有 probe 测试。

**验证（需真机）**：挂起节点 → 停止 → 正在跑的探针快速被杀（不再等 25s）；
配置 4 并发时中断进程数 ≤4。

---

## 三、V2 未交付（需补派）

**需求**：`docs/REQ-GLOBAL-TASK-PANEL.md` 第二节——通用多任务悬浮栈
（scan/stop-scan/restart/batch/release 跨页常驻）。

**现状**：`src/App.tsx:130` 仍是单 release 面板（`release.active` 单任务）；
无 `tasks` 数组、无 `onTask`、NodesPage/PoolPage 未接入 scan/stop-scan/restart/batch。

**预计工作量**：App.tsx 多任务栈 + 三个页面接入 + 类型映射，约 1-2 天。

---

## 四、验收结论与建议

1. **V1 验收通过**——可标记 ✅；`stop_scan_concurrency` 从死配置变为真配置，停止会变快。
2. **V2 未交付**——补派给同事，或确认是否已排期。
3. 建议话术：**"V1 验收通过，做得干净。V2 全局悬浮窗还没看到，是排期问题还是遗漏？"**
