# Code Review：V1 + V2 实施审查（2026-08-15）

> 审查方式：独立审查子代理（无共享上下文）+ 海鸥复核（关键问题逐一核实）
> 审查对象：`11b1cfe`（V1 停止扫描并发）+ `b57cb7e`（V2 全局任务悬浮窗）
> 结论：**passed: false** —— V1 并发核心扎实，V2 任务栈状态机有 3 个明确 bug，需修复后重验

---

## 一、V1 审查结论（✅ 扎实）

- `activeProbes` 登记/快照/清空全程持锁，无数据竞争
- semaphore 正确限制 kill 并发为 `stop_scan_concurrency`
- `wg.Wait` 保证 kill goroutine 无泄漏
- `waitForPortAbort` 让停止响应 ≤200ms
- `go test -race`（含 `-count=8` 压测）全绿
- 4 个测试覆盖并发上限/回退/中断路径到位

**V1 无需改动**（两个低危打磨项可选，见下）。

## 二、V2 逻辑问题（需修复，按严重度）

### 🔴 高

**1. 停止扫描后 `scan` 任务永久残留**（`NodesPage.tsx:213-246` + `App.tsx:90-96`）
- 流程：扫描上报 `scan` 任务（busy:true, 3/10）→ 点停止**只**上报 `stop-scan` → 后端 done 后 `scan` 任务**没有任何 upsert/remove 路径** → 悬浮窗永远显示"扫描节点 3/10 正在扫描…"
- 修复：进入 stopping/done 时对 `scan` 任务补发 `busy:false` 收尾或 `removeTask('scan')`

**2. 自动收起 filter 漏检查 `busy`**（`App.tsx:93`）
- `filter((t) => t.total <= 0 || t.done >= t.total)` 会把**忙态**且 done==total 的任务安排 1.2s 移除——stop-scan 恒等计数（done==total）全靠 poll 800ms 续命，poll 一旦超 1.2s 任务闪没又被加回
- 渲染层（200 行）正确排除了 busy，effect 漏了——**两处不一致**
- 修复：filter 改为 `!t.busy && (t.total <= 0 || t.done >= t.total)`

**3. 扫描期间切走页面，完成后 `scan` 任务永久冻结**
- poll 随组件卸载停止，完成后无人上报 done；返回页面 idle 分支不触发 onTask
- 修复：App 层对 scan 任务加超时兜底回收，或 idle+done 分支补收尾上报

### 🟡 中

**4. `StoppingCount==StoppedCount` 恒等，stop-scan 进度条瞬间 100%**（`probe.go:205-208`）
- `interruptProbes` 在 `wg.Wait()` 后一次性把两个计数都设为 `len(pairs)` → 进度条立即满条，误导
- 且重复 RequestStop 会把计数清零（不检查状态，空 map → 0/0）
- 修复：kill 循环中每完成一对就更新 `StoppedCount`；RequestStop 非 Running 时直接 return

**5. `NodesPage.tsx:217` done 误用 `stopping_count`**（应为 `stopped_count`）
- 复制粘贴笔误；当前两值恒等无可见差异，修复 #4 后必须一并改

**6. ✕ 关闭语义失效**（`App.tsx:213-221`）
- `removeTask` 后下一轮 poll（≤800ms）就 upsert 加回——任务"关不掉"
- 修复：加 `dismissed` 集合，真正"后台继续但隐藏"

### 🟢 低

**7. `RequestStop` 同步阻塞**（`probe.go:159`）
- `interruptProbes` 串行 kill + `wg.Wait()`，`/scan/stop` HTTP 会阻塞数百 ms~数秒
- Windows PID 复用窗口内二次 kill 有极小误杀风险（幂等性 OK）

## 三、建议（非阻塞）

- 测试缺口：无 register 与 interruptProbes 竞态窗口测试；无 Kill error 吞错路径测试；前端任务栈状态机无单测（建议补 vitest）
- 测试稳定性：全量 `-race` 跑 4 次出现 1 次偶发 FAIL（端口型测试 `TestScanStopInterruptsRunningProbes` 占 26100-26104，CI 可能偶发冲突）
- 风格：`clearTask` 命名易混淆；`interruptProbes` 中 `limit<1` 分支冗余

## 四、修复优先级建议

1. **问题 1/2/5**（3 处小改动，1 小时）——悬浮窗状态机收敛，阻塞交付
2. **问题 4**（后端计数渐进更新）——stop-scan 进度真实化
3. 问题 3/6/7 视排期（3 和 6 是体验完善，7 是低危性能）

**修复后重跑**：`go test -race ./core/manager/` + `npm run build` + 悬浮窗状态机走查（扫描→停止→切页→完成 四场景）。
