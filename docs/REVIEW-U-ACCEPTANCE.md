# 验收报告：U 系列实施检查（2026-08-14）

> 验收人：海鸥（总控，子代理实施）
> 验收基线：`main@f0351e9`（U1~U5 全部合入）
> 测试状态：`go test -count=1 ./...` **全绿**（9 包）；`go test -race` 关键包（core/manager、vendors/opencode、root）全绿；`npm run build`（tsc + vite）全绿

---

## 一、总体结论

**U 系列（EXECUTION-PLAN 第二阶段）—— 全部实施并合并，自动化验收通过 ✅**

| 阶段 | 核心实现 | 位置 | 状态 |
|---|---|---|---|
| U1 poll 自杀 bug | stopAck 命中 `stopping/done` 不再 `return`，继续走 `setTimeout(poll, 800)` 保活轮询；stopAck 期间仍 running 时保持进度显示 | `src/pages/NodesPage.tsx` poll（`6149b72`） | ✅ 缺陷已修 |
| U2 状态筛选 | `filter: 'all'\|'running'\|'stopped'` state + members 过滤链追加状态条件 + 三态按钮组（样式对齐路由模式按钮），批量操作基于过滤后集合 | `src/pages/PoolPage.tsx`（`83d198d`） | ✅ |
| U3 轮询配置 | `ui_poll_interval_sec`（`*int`：nil=默认5 / 0=关轮询持久生效 / 1~60）全链路配置（ManagerConfig+ConfigView+AppConfig+example.json+api.ts）；两页轮询改读配置；独享页新增齿轮设置弹窗（折叠面板）+ 自动轮询（轻量，深度校正仍手动）；实例池页新增「界面刷新」折叠面板 | `core/manager/config.go` + `types_config.go` + `PoolPage.tsx` + `InstancesPage.tsx`（`9f8d43f` + `ab38520` 修复） | ✅ |
| U4 日志分页 | `PAGE_SIZE=100` 前端切片 + 分页条（上/下页+页码）；轮询刷新不跳页（page 独立 state + clamp）；`LogRow` React.memo；拉取量跟随 `call_log_max`（只减不增，上限 5000） | `src/pages/LogsPage.tsx`（`1d16501`） | ✅ |
| U5 统计迷你图 | 每实例行成功/失败 `MiniBar`（flex 色块，绿成功/红失败按占比）；按天视图近 14 天纯 CSS 柱状趋势（柱高=请求量，绿红纵向=成功率）；0 值灰态不除零不 NaN；零依赖 | `src/pages/StatsPage.tsx`（`6f4daad`） | ✅ |

**测试覆盖**：`config_test.go` 新增 `TestConfigUiPollIntervalSec`（默认5 / 显式0持久 / 非法回退 / 非整数拒绝 / 落盘读回）全绿；前端 `tsc -b` 类型检查全绿。未发现实现与 REQ 文档的实质偏差。

---

## 二、⚠️ 实施中发现的验收缺陷与修复（U3）

**缺陷**：U3 初版 `ui_poll_interval_sec` 用 `int`，`uiPollIntervalSecOf` 将 0 归一为默认 5 ——
「0 = 关闭轮询」只在 Set 层成立，ConfigView 永远 ≥5，用户存 0 后重载回到 5s，**验收项不成立**。

**修复**（`ab38520`）：按仓库既有先例（`PoolProbeEnabled *bool` / `ShowNodePrefix *bool` 指针区分
「未设置」与「显式值」）改用 `*int`：nil=未设置（默认5）、&0=显式关轮询、1~60=用户值、
非法（<0 或 >60）回退默认。ConfigGet/ConfigView 均不再归一 0；落盘 `"ui_poll_interval_sec": 0`
重载后仍 0。前端 `<=0 不启动轮询` 防御已就位，无前端改动。

---

## 三、端到端验收清单（待用户/部署机浏览器走查）

自动化层（单测/tsc/build/代码走查）已全部通过；以下为 REQ 文档定义的浏览器走查项，
需部署机或用户真机确认，验收人在验收表打勾：

- [ ] U1：勾 100 节点 → 扫描 → 停止 → 再勾选扫描 → **进度条正常推进、结果弹窗正常**（缺陷复现路径）
- [ ] U1：连续两次扫描均正常（轮询在停止后仍存活）
- [ ] U2：Running/Stopped 混合 → 切「运行中」/「已停止」/「全部」；搜索+筛选叠加；筛选后「全部启动」只作用于可见成员
- [ ] U3：独享页开启实例 → kill 进程 → 默认 5s 内状态自动变「已停止」（无需点刷新）；间隔改 10s 后刷新节奏变慢；设 0 后不再自动刷新；两页设置弹窗折叠面板展开收起流畅
- [ ] U4：构造 5000+ 条日志 → 翻页无卡顿、轮询后停留当前页
- [ ] U5：统计页目视——条图随数据变化正确渲染，0 数据空态正常

> 环境红线说明：本次验收全程未启动任何真实服务（无浏览器可用）；所有改动经
> `go test`（httptest 随机端口）+ `npm run build`（tsc 类型检查）验证，不触碰正式版进程。

---

## 四、附：U 系列抽查明细（验收证据）

### U1 poll 保活（`src/pages/NodesPage.tsx`）
- `stopAckRef` 命中 `stopping/done`：清零 + `setScanning(false)`，**不 return**，继续到底部 `if (alive) setTimeout(poll, 800)` ✅
- stopAck 期间仍 running：`setScan(p)` + `setScanning(true)` 保持进度显示 ✅
- 无 stopAck：原逻辑（`setScan` / `setScanning(running|stopping)` / `done && prev==='running'` 弹窗）不变 ✅
- 卸载清理 `alive=false` 双保险，无 setState-after-unmount ✅

### U2 状态筛选（`src/pages/PoolPage.tsx`）
- `members` 过滤链：`join_gateway → search → filter` 三级串行（AND）✅
- 三态按钮组 `[全部][运行中][已停止]`，选中 `bg-zinc-900 text-white` 与路由模式按钮一致 ✅
- 批量操作 `base = members.filter(...)` 天然作用于过滤后集合，零改动 ✅
- 默认 `all` 短路返回 true，与改动前行为一致（回归）✅

### U3 轮询配置
- `ConfigSet`：0→存 `&0`；1~60→存值；<0/>60→nil（回退）；非整数拒绝 ✅
- `ConfigGet`：nil→"5"；非 nil→实际值 ✅
- `ConfigViewOf`：`uiPollIntervalSecOf(cfg)` 指针语义（nil→5、0~60→原值、其他→5）✅
- 两页轮询 `if (uiPollSec <= 0) return` 不启动；`setInterval(uiPollSec*1000)` ✅
- 独享页轮询仅调 `load`（静默），深度校正仍由手动「刷新」按钮（CHECK_BATCH=5）承担 ✅
- 折叠面板 `openSections` 统一扩展（PoolPage 加 `ui` 键；InstancesPage 新建 `{ ui }`），每个配置分组一个 section，预留扩展 ✅

### U4 日志分页（`src/pages/LogsPage.tsx`）
- `totalPages = max(1, ceil(len/100))`；`currentPage = min(page, totalPages)` 全程 clamp ✅
- 轮询刷新只更新 `logs`，`page` state 独立不动 → 不跳页 ✅
- 清空日志 `setPage(1)` 复位；空列表 1 页且分页条双按钮禁用 ✅
- 默认显示最新页（数据最新在前，第 1 页 = 最新）✅

### U5 统计迷你图（`src/pages/StatsPage.tsx`）
- `MiniBar`：`total===0` 灰态占位（无除零）；绿/红宽度 = ok/total、fail/total ✅
- 按天趋势：柱高 = total/maxTotal；绿红纵向 = okPct/(100-okPct)，合计恒 100% ✅
- `Card` 加可选 `children`（向后兼容），趋势条复用 Card ✅
- 数字表格/下钻/按天切换/轮询/重置未动；下钻行 colSpan 6→7 对齐新列 ✅

---

## 五、遗留与建议

1. **无实现遗留**：U1~U5 全部合入 main，自动化验证全绿，无「上阶段遗留」项。
2. **端到端浏览器走查**（上文三清单）需部署机/用户真机执行后打勾。
3. **M3~M6（CI 三平台产物/docker/macOS）** 仍待部署机验证，可穿插进行。
4. 若后续要支持「0=关轮询」之外的更多轮询语义，可在 `ui_poll_interval_sec` 指针模式上扩展，无需改结构。
