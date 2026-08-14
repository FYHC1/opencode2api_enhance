# 需求：实例池页增加状态筛选（对齐独享页）

> 需求提出：2026-08-14（用户，客户要求）
> 状态：待实施
> 关联：`src/pages/PoolPage.tsx`（实例池页）、`src/pages/InstancesPage.tsx`（独享页，已有筛选可参照）

---

## 〇、需求一句话

**独享页已有「搜索 + 状态筛选」双能力，实例池页只有搜索——给实例池页补上状态筛选，两页交互对齐。**

## 一、现状（已核实代码）

| 页面 | 文件 | 搜索 | 状态筛选 |
|---|---|---|---|
| 独享页 | `src/pages/InstancesPage.tsx` | ✅ `search`（名称/节点/IP/端口） | ✅ `filter`：`all / running / stopped`（第 56 行，第 157~168 行过滤逻辑） |
| 实例池页 | `src/pages/PoolPage.tsx` | ✅ `search`（名称/节点/IP/端口，第 67~94 行） | ❌ **无**——仅有搜索框，成员全量展示 |

**独享页可参照的实现**（`InstancesPage.tsx`）：
```tsx
const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all')
// 过滤逻辑：
if (filter === 'running' && i.status !== 'Running') return false
if (filter === 'stopped' && i.status !== 'Stopped') return false
```

## 二、功能开发（文件级）

**改动文件**：`src/pages/PoolPage.tsx`（唯一改动点，后端无需变动——成员列表数据 `members` 已有全部状态字段）

1. **新增状态筛选 state**（对齐独享页）：
   ```tsx
   const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all')
   ```

2. **members 过滤链追加状态条件**（现有第 87~94 行 search 过滤之后）：
   ```tsx
   const members = instances
     .filter((i) => i.join_gateway)
     .filter((i) => { /* 现有 search 逻辑不变 */ })
     .filter((i) => {
       if (filter === 'running' && i.status !== 'Running') return false
       if (filter === 'stopped' && i.status !== 'Stopped') return false
       return true
     })
   ```

3. **UI 放置**：搜索框左侧（表头右侧按钮组）加三态筛选按钮组，样式与路由模式按钮组一致
   （`bg-zinc-900 text-white` 选中态 / `bg-white border-zinc-200` 未选中态）：
   ```
   [全部] [运行中] [已停止]
   ```
   按钮组 + 搜索框 + 「全部启动/停止/测试/释放」按钮，同一排。

4. **与既有批量操作联动确认**（重要，勿破坏）：
   - 现有「全部启动/停止/测试/释放」操作 `members` 已是过滤后集合（第 350/415/476 行 `base = members.filter(...)`）——
     **筛选后「全部 X」只作用于当前筛选可见的成员**（与独享页行为一致），此为预期行为，实现时保持即可。
   - 「一键释放全部」按钮语义不变（仍按当前筛选可见成员）。

## 三、测试

- 前端组件逻辑：
  - 默认 `all`：成员全量显示（与现状一致，回归）
  - `running`：仅 Running 成员可见；`stopped`：仅 Stopped 成员可见
  - 搜索 + 筛选叠加：两者为 AND 关系（名称匹配 且 状态匹配）
  - 筛选后「全部启动/停止」仅作用于可见成员（与独享页一致）
- `npm run build` 通过；TS 类型检查无错误。

## 四、验证

- 浏览器走查（六页 UI 一致性）：
  1. 实例池页有实例 Running + Stopped 混合 → 切「运行中」只显示 Running；切「已停止」只显示 Stopped；切「全部」恢复
  2. 搜索 + 筛选组合生效
  3. 筛选「已停止」后点「全部启动」→ 只启动已停止的可见成员（无 Running 成员被误操作）
  4. 与独享页交互手感一致
- **验收**：实例池页状态筛选可用；与独享页行为/样式一致；批量操作与筛选联动正确。

## 五、备注

- 轻量化原则：不引入额外依赖，纯 React state + 数组 filter，与独享页实现完全同构。
- 不做后端改动：`/api/admin/instances` 已返回全部实例及状态，筛选纯前端。
