# 验收报告：V 系列实施检查（2026-08-15）

> 验收人：海鸥（总控，子代理实施）
> 验收基线：`main@afaa1fc`（V1 + V2 均已合入）
> 测试状态：`go test -count=1 ./...` **全绿**（9 包）；`-race` 关键包全绿；`npm run build` 全绿

---

## 一、总体结论

| 阶段 | 交付 | 状态 |
|---|---|---|
| **V1** 停止扫描并发接逻辑 | ✅ 已合入 `11b1cfe` | ✅ 验收通过 |
| **V2** 全局任务悬浮窗 | ✅ 已合入 `b57cb7e`（本报告初次验收时未交付，随后已补派完成） | ✅ 验收通过 |

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

## 三、V2 验收明细（补派后通过 ✅）

**需求落点**：`docs/REQ-GLOBAL-TASK-PANEL.md` 第二节——通用多任务悬浮栈。

### 实现核实（`b57cb7e`，4 文件 +175/−36）
| 要求 | 实现 | 状态 |
|---|---|---|
| 多任务栈 | `App.tsx` `tasks: TaskItem[]` + `upsertTask`/`removeTask`/`clearTask`；异 id 并存纵向堆叠，✕ 单独关闭不影响其他任务与后台执行 | ✅ |
| 类型→颜色/文案 | `TASK_COLORS`（release/stop-scan/restart=red/amber/amber、scan/batch=teal）+ `TASK_TEXT` 映射 | ✅ |
| 跨页常驻 | tasks 在 App 顶层 + 面板在 App 渲染，切页常驻；返回页面后 poll 恢复更新 | ✅ |
| 完成自动收起 | `useEffect` 对 `done>=total || total<=0` 1200ms 后移除；空 tasks 不渲染 | ✅ |
| release 迁移 | `onRelease` 兼容包装映射到 id='release'，PoolPage 现有释放调用零改动；`doExitRelease` 改用 upsertTask | ✅ |
| scan/stop-scan 上报 | `NodesPage.tsx`：扫描中上报 current/total；停止中上报 `stopping_count/stopped_count`（V1 契约），done 后 busy:false 收起；失败 error:true 红文案 | ✅ |
| restart/batch 上报 | `PoolPage.tsx`：`doRestart` 0→1 两态（单次调用无中间回调）；批量测试逐条增量 done；fail>0 红文案 | ✅ |
| 不打扰 | 容器 `pointer-events-none` + 卡片 `pointer-events-auto`；失败行红色 + 页面 toast | ✅ |

**测试**：`npm run build`（tsc + vite）全绿；`go test` 全绿（后端未动）；逻辑走查多任务并存/✕ 单独关闭/0-0 不崩溃/跨页常驻。

**验证（需真机）**：扫描 20 节点切统计页悬浮窗持续显示；停止扫描切页显示停止进度；一键重启池显示阶段进度；多任务并存堆叠互不干扰。

---

## 四、验收结论与建议

1. **V1 验收通过**——`stop_scan_concurrency` 从死配置变为真配置（probeNode 取消支持 + 并发上限 kill），停止会变快。
2. **V2 验收通过**——全局任务悬浮窗交付，五类长操作（scan/stop-scan/restart/batch/release）跨页常驻可关。
3. 端到端（需部署机/用户）：挂起节点停止时探针快速被杀（不再干等 25s）；多任务并存悬浮栈真机目视。
