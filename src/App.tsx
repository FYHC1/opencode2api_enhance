import { useCallback, useEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import { invoke } from '@tauri-apps/api/core'
import { Server, Layers, Radar, Settings, BarChart3, ScrollText, LogOut } from 'lucide-react'
import { api } from './lib/api'
import { TitleBar } from './components/TitleBar'
import TaskPanel from './components/TaskPanel'
import InstancesPage from './pages/InstancesPage'
import PoolPage from './pages/PoolPage'
import NodesPage from './pages/NodesPage'
import SettingsPage from './pages/SettingsPage'
import StatsPage from './pages/StatsPage'
import LogsPage from './pages/LogsPage'

type Tab = 'instances' | 'pool' | 'nodes' | 'settings' | 'stats' | 'logs'

const NAV: { id: Tab; label: string; icon: typeof Server }[] = [
  { id: 'instances', label: '独享', icon: Server },
  { id: 'pool', label: '实例池', icon: Layers },
  { id: 'nodes', label: '节点池', icon: Radar },
  { id: 'stats', label: '统计', icon: BarChart3 },
  { id: 'logs', label: '日志', icon: ScrollText },
  { id: 'settings', label: '设置', icon: Settings },
]

// V2: 全局任务悬浮栈——任务类型（决定进度条颜色）与任务项（TaskPanel 组件复用类型）
export type TaskType = 'release' | 'scan' | 'stop-scan' | 'restart' | 'batch'

export type TaskItem = {
  id: string
  type: TaskType
  title: string
  done: number
  total: number
  busy?: boolean
  /** 失败标记：悬浮窗内该行文案变红（失败明细 toast 已由页面上报） */
  error?: boolean
  /** 最近一次 upsert 时间戳（V2 超时兜底：busy scan 任务超过预计时长无更新的收尾移除） */
  lastUpdate?: number
}

// V2: scan 任务无更新超时——扫描中切走页面后 NodesPage poll 停止、无人上报 done 时，
// 防止 busy scan 任务在悬浮窗永久冻结；按扫描规模估算（下限 60s，页面内 poll 每 800ms
// 刷新 lastUpdate，正常运行不会触发）。
const SCAN_STALE_BASE_MS = 60_000
const SCAN_STALE_PER_NODE_MS = 5_000
const scanStaleMs = (total: number) => Math.min(600_000, SCAN_STALE_BASE_MS + total * SCAN_STALE_PER_NODE_MS)

export default function App() {
  const [tab, setTab] = useState<Tab>('instances')
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)
  // V2: 全局任务悬浮栈（跨页面常驻，多任务并存堆叠）
  const [tasks, setTasks] = useState<TaskItem[]>([])
  // D1：退出二次确认（退出并释放 / 退出不释放 / 取消）
  const [exitOpen, setExitOpen] = useState(false)
  const [exiting, setExiting] = useState(false)

  // 任务镜像 ref：供超时兜底 interval 离线判断（避免在 setTasks 更新器里做副作用）
  const tasksRef = useRef<TaskItem[]>([])
  useEffect(() => {
    tasksRef.current = tasks
  }, [tasks])

  // 杂项: toast 单 timer——新提示先清旧句柄再排，连续两条时早先的 timer 不会提前清掉后面的
  const toastTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // M10: showToast 稳定化（内部只依赖 ref/函数式 setState）——页面组件引用不变，配合 memo 隔离重渲染
  const showToast = useCallback((msg: string, ok = true) => {
    setToast({ msg, ok })
    if (toastTimerRef.current) clearTimeout(toastTimerRef.current)
    toastTimerRef.current = setTimeout(() => setToast(null), 3600)
  }, [])

  // V2/G10/L10: 任务操作——upsertTask 按 id 新增/更新（同 id 覆盖，异 id 并存堆叠）；removeTask 收尾移除。
  // ✕ 关闭（后台继续）：dismissedRef 记忆「id → 轮次令牌」，仅同轮 busy 上报被过滤（防 poll 秒速加回）；
  // 收尾上报/移除即本轮结束（清记忆 + 推进轮次）——同 id 新一轮 busy 从宽放行，不压制重新开始的任务。
  // 完成态自动收起：每任务独立驱逐 timer——自「该任务非忙收尾 upsert」起 1.2s 移除，不因其它任务更新而无限重置。
  const taskRoundSeq = useRef(1)
  const taskRoundRef = useRef<Map<string, number>>(new Map()) // id → 当前轮次令牌
  const dismissedRef = useRef<Map<string, number>>(new Map()) // id → 已关闭的轮次令牌
  const finishTimersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())

  // 同 id 换新驱逐 timer 前清旧句柄（新一轮收尾重新计时）
  const clearFinishTimer = useCallback((id: string) => {
    const h = finishTimersRef.current.get(id)
    if (h) clearTimeout(h)
    finishTimersRef.current.delete(id)
  }, [])

  const removeTask = useCallback(
    (id: string) => {
      dismissedRef.current.delete(id)
      // 移除即本轮结束：推进轮次，新一轮 busy 上报与旧关闭记忆令牌不同 → 放行
      taskRoundRef.current.set(id, taskRoundSeq.current++)
      clearFinishTimer(id)
      setTasks((prev) => prev.filter((t) => t.id !== id))
    },
    [clearFinishTimer],
  )

  const upsertTask = useCallback(
    (task: TaskItem) => {
      const id = task.id
      // 未开轮的 id 首见即开一轮（令牌定轮）
      if (!taskRoundRef.current.has(id)) taskRoundRef.current.set(id, taskRoundSeq.current++)
      const round = taskRoundRef.current.get(id)!
      if (task.busy) {
        // G10: 已关闭的同轮 busy 上报过滤；新一轮（令牌不同）从宽放行
        if (dismissedRef.current.get(id) === round) return
      } else {
        // 收尾上报：本轮生命周期结束——清关闭记忆 + 推进轮次
        dismissedRef.current.delete(id)
        taskRoundRef.current.set(id, taskRoundSeq.current++)
      }
      clearFinishTimer(id)
      setTasks((prev) => {
        const item = { ...task, lastUpdate: Date.now() }
        const idx = prev.findIndex((t) => t.id === task.id)
        if (idx === -1) return [...prev, item]
        const next = prev.slice()
        next[idx] = item
        return next
      })
      // 完成态 1.2s 自动收起：仅非忙且 done>=total（或 0/0 重置信号），按该任务自身计时
      if (!task.busy && (task.total <= 0 || task.done >= task.total)) {
        finishTimersRef.current.set(id, setTimeout(() => removeTask(id), 1200))
      }
    },
    [clearFinishTimer, removeTask],
  )

  // G10: ✕ 关闭（后台继续）——仅对仍 busy（后台在跑）的任务记录 dismissed 防 poll 加回；
  // 已完成卡片 ✕ 只移除不记录，避免压制同 id 的下一轮新任务。
  const dismissTask = useCallback(
    (id: string, busy = false) => {
      removeTask(id)
      if (busy) dismissedRef.current.set(id, taskRoundRef.current.get(id) ?? 0)
    },
    [removeTask],
  )

  // V2: busy scan/stop-scan 超时兜底——每 5s 检查超过预计时长无更新的任务，
  // 走 upsertTask 收尾（非忙 0/0，由各自驱逐 timer 1.2s 收起），
  // 防「扫描中切页后无人上报 done」冻结；M11: 兜底范围从仅 scan 扩展到 stop-scan。
  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now()
      for (const t of tasksRef.current) {
        if ((t.type !== 'scan' && t.type !== 'stop-scan') || !t.busy) continue
        if (now - (t.lastUpdate ?? now) <= scanStaleMs(t.total)) continue
        // 超时冻结：置非忙 0/0（upsertTask 内部会排完成态驱逐 timer）
        upsertTask({ id: t.id, type: t.type, title: t.title, done: 0, total: 0, busy: false })
      }
    }, 5000)
    return () => window.clearInterval(timer)
  }, [upsertTask])

  // M10: onRelease 稳定化（只依赖稳定回调）——PoolPage 因 props 引用不变而保持 memo，隔离重渲染
  const onRelease = useCallback(
    (r: { active: boolean; done: number; total: number }) => {
      if (!r.active) {
        removeTask('release')
        return
      }
      upsertTask({ id: 'release', type: 'release', title: '释放实例', done: r.done, total: r.total, busy: r.done < r.total })
    },
    [removeTask, upsertTask],
  )

  // 退出（不释放实例）：直接调用壳退出（实例留在后台继续运行）。
  const doExitKeep = async () => {
    setExitOpen(false)
    try {
      await invoke('quit_app')
    } catch {
      showToast('退出需要桌面版环境', false)
    }
  }

  // 退出并释放：先按 4 并发释放全部实例（含独享与池成员），进度全局可见，完成后退出。
  // 杂项: ref 级防重入——setExiting 是异步 state，连点两下会在生效前二次进入（并发释放同一批/重复 quit_app）
  const exitGuard = useRef(false)
  const doExitRelease = async () => {
    if (exitGuard.current) return
    exitGuard.current = true
    setExitOpen(false)
    setExiting(true)
    try {
      const ins = await api.listInstances()
      const names = ins.map((i) => i.name)
      if (names.length === 0) {
        await invoke('quit_app')
        return
      }
      upsertTask({ id: 'release', type: 'release', title: '释放实例', done: 0, total: names.length, busy: true })
      const batchSize = 4
      let done = 0
      for (let i = 0; i < names.length; i += batchSize) {
        const chunk = names.slice(i, i + batchSize)
        await Promise.allSettled(chunk.map((n) => api.removeInstance(n)))
        done += chunk.length
        upsertTask({ id: 'release', type: 'release', title: '释放实例', done, total: names.length, busy: true })
      }
      upsertTask({ id: 'release', type: 'release', title: '释放实例', done: names.length, total: names.length, busy: false })
      await invoke('quit_app')
    } catch (e) {
      setExiting(false)
      showToast(String(e), false)
    } finally {
      exitGuard.current = false
    }
  }

  return (
    <div className="h-full flex flex-col">
      <TitleBar />

      <div className="flex-1 flex min-h-0">
        {/* 侧边栏导航 */}
        <aside className="w-44 shrink-0 border-r border-zinc-200/80 bg-white/60 backdrop-blur flex flex-col py-4 px-2 gap-1">
          {NAV.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              type="button"
              onClick={() => setTab(id)}
              className={clsx(
                'flex items-center gap-2.5 px-3 py-2 rounded-lg text-[13px] font-medium transition-colors',
                tab === id
                  ? 'bg-zinc-900 text-white shadow-sm'
                  : 'text-zinc-600 hover:bg-zinc-100',
              )}
            >
              <Icon size={16} strokeWidth={2} />
              {label}
            </button>
          ))}
        </aside>

        {/* 内容区 */}
        <main className="flex-1 min-w-0 overflow-y-auto">
          {tab === 'instances' && <InstancesPage toast={showToast} />}
          {tab === 'pool' && <PoolPage toast={showToast} onRelease={onRelease} onTask={upsertTask} />}
          {tab === 'nodes' && <NodesPage toast={showToast} onTask={upsertTask} onRemove={removeTask} />}
          {tab === 'stats' && <StatsPage toast={showToast} />}
          {tab === 'logs' && <LogsPage toast={showToast} />}
          {tab === 'settings' && <SettingsPage toast={showToast} onRequestExit={() => setExitOpen(true)} />}
        </main>
      </div>

      {/* Toast */}

      {toast && (
        <div
          className={clsx(
            'fixed bottom-5 left-1/2 -translate-x-1/2 z-50 px-4 py-2 rounded-lg text-[13px] shadow-lg',
            toast.ok ? 'bg-zinc-900 text-white' : 'bg-red-600 text-white',
          )}
        >
          {toast.msg}
        </div>
      )}

      {/* V2: 全局任务悬浮栈（跨页面常驻；多任务并存纵向堆叠；✕ 仅隐藏该条，后台继续） */}
      {/* M10: 独立 memo 面板——App 其它状态（toast/tab/退出弹窗）变化不带动面板与页面重渲染 */}
      {tasks.length > 0 && <TaskPanel tasks={tasks} onDismiss={dismissTask} />}
    {/* D1：退出二次确认弹窗 */}
      {exitOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-900/40"
          onClick={() => !exiting && setExitOpen(false)}
        >
          <div className="bg-white rounded-2xl shadow-xl w-[440px] p-6 space-y-4" onClick={(e) => e.stopPropagation()}>
            <div className="text-[15px] font-semibold text-zinc-900">退出程序</div>
            <p className="text-[13px] text-zinc-600 leading-relaxed">
              退出前可以选择是否先释放全部实例（停止并删除，含独享与池成员）。
              <br />
              「退出不释放」会直接退出，实例进程留在后台继续运行。
            </p>
            <div className="space-y-2">
              <button
                type="button"
                onClick={() => void doExitRelease()}
                disabled={exiting}
                className="w-full flex items-center gap-2 px-4 py-2.5 rounded-lg text-[13px] font-medium text-red-700 bg-red-50 hover:bg-red-100 disabled:opacity-50 transition-colors"
              >
                <LogOut size={15} />
                {exiting ? '正在释放实例并退出…' : '退出并释放全部实例'}
              </button>
              <button
                type="button"
                onClick={() => void doExitKeep()}
                disabled={exiting}
                className="w-full flex items-center gap-2 px-4 py-2.5 rounded-lg text-[13px] font-medium text-zinc-700 bg-zinc-100 hover:bg-zinc-200 disabled:opacity-50 transition-colors"
              >
                <LogOut size={15} />
                退出不释放（实例留在后台）
              </button>
            </div>
            <button
              type="button"
              onClick={() => setExitOpen(false)}
              disabled={exiting}
              className="w-full px-4 py-2 rounded-lg text-[13px] text-zinc-600 bg-white border border-zinc-200 hover:bg-zinc-50 disabled:opacity-50 transition-colors"
            >
              取消
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
