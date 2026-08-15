import { useEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import { invoke } from '@tauri-apps/api/core'
import { Server, Layers, Radar, Settings, BarChart3, ScrollText, LogOut, X } from 'lucide-react'
import { api } from './lib/api'
import { TitleBar } from './components/TitleBar'
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

// V2: 全局任务悬浮栈——任务类型（决定进度条颜色）与任务项
type TaskType = 'release' | 'scan' | 'stop-scan' | 'restart' | 'batch'

type TaskItem = {
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

const TASK_COLORS: Record<TaskType, string> = {
  release: 'bg-red-500',
  'stop-scan': 'bg-amber-500',
  restart: 'bg-amber-500',
  scan: 'bg-teal-500',
  batch: 'bg-teal-500',
}

const TASK_TEXT: Record<TaskType, string> = {
  release: '正在释放，不影响你继续操作…',
  scan: '正在扫描，不影响你继续操作…',
  'stop-scan': '正在停止，不影响你继续操作…',
  restart: '正在重启实例池…',
  batch: '正在处理，不影响你继续操作…',
}

export default function App() {
  const [tab, setTab] = useState<Tab>('instances')
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)
  // V2: 全局任务悬浮栈（跨页面常驻，多任务并存堆叠）
  const [tasks, setTasks] = useState<TaskItem[]>([])
  // D1：退出二次确认（退出并释放 / 退出不释放 / 取消）
  const [exitOpen, setExitOpen] = useState(false)
  const [exiting, setExiting] = useState(false)

  const showToast = (msg: string, ok = true) => {
    setToast({ msg, ok })
    setTimeout(() => setToast(null), 3600)
  }

  // V2: 任务操作——upsertTask 按 id 新增/更新（同 id 覆盖，异 id 并存堆叠）；
  // removeTask 收尾移除（完成自动收起/停止过渡取代）；clearTask 完成/失败自动收起重置（短暂保留完成态后移除）。
  // G10: ✕ 关闭（后台继续）语义——dismissedRef 记忆已关闭的 id，busy 期间 upsert 被过滤（防 poll 秒速加回）；
  // 收到该 id 非 busy 收尾上报或收尾移除时清除记忆（完成态短暂显示后收起，同 id 下一轮新任务不受影响）。
  const dismissedRef = useRef<Set<string>>(new Set())

  const upsertTask = (task: TaskItem) => {
    if (dismissedRef.current.has(task.id)) {
      if (task.busy) return // 已关闭且后台仍 busy：不加回
      dismissedRef.current.delete(task.id) // 非 busy 收尾上报：任务生命周期结束，清除关闭记忆
    }
    setTasks((prev) => {
      const item = { ...task, lastUpdate: Date.now() }
      const idx = prev.findIndex((t) => t.id === task.id)
      if (idx === -1) return [...prev, item]
      const next = prev.slice()
      next[idx] = item
      return next
    })
  }

  const removeTask = (id: string) => {
    dismissedRef.current.delete(id) // 收尾移除即生命周期结束，清除关闭记忆
    setTasks((prev) => prev.filter((t) => t.id !== id))
  }

  // G10: ✕ 关闭（后台继续）——仅对仍 busy（后台在跑）的任务记录 dismissed 防 poll 加回；
  // 已完成卡片 ✕ 只移除不记录，避免压制同 id 的下一轮新任务。
  const dismissTask = (id: string, busy = false) => {
    removeTask(id)
    if (busy) dismissedRef.current.add(id)
  }

  const clearTask = (id: string): number => {
    return window.setTimeout(() => removeTask(id), 1200)
  }

  // V2: 完成/失败自动收起重置——仅非忙态且 done>=total（或 0/0 重置信号）短暂保留后移除；
  // busy 任务（停止扫描等 done==total 仍进行中）不参与自动收起；空 tasks 不渲染。
  useEffect(() => {
    if (tasks.length === 0) return
    const timers = tasks
      .filter((t) => !t.busy && (t.total <= 0 || t.done >= t.total))
      .map((t) => clearTask(t.id))
    return () => timers.forEach(clearTimeout)
  }, [tasks])

  // V2: busy scan 任务超时兜底——每 5s 检查超过预计时长无更新的 scan 任务，
  // 置为非忙（0/0 由上方收起 effect 移除），防「扫描中切页后无人上报 done」冻结。
  useEffect(() => {
    const timer = window.setInterval(() => {
      setTasks((prev) => {
        const now = Date.now()
        let changed = false
        const next = prev.map((t) => {
          if (t.type !== 'scan' || !t.busy) return t
          if (now - (t.lastUpdate ?? now) <= scanStaleMs(t.total)) return t
          changed = true
          return { ...t, busy: false, done: 0, total: 0 }
        })
        return changed ? next : prev
      })
    }, 5000)
    return () => window.clearInterval(timer)
  }, [])

  // V2: onRelease 兼容包装——PoolPage 现有释放调用（{active,done,total}）零改动映射到任务栈；
  // busy 由 done/total 推导：进行中忙态文案，完成（done=total）后显示「已完成」并自动收起。
  const onRelease = (r: { active: boolean; done: number; total: number }) => {
    if (!r.active) {
      removeTask('release')
      return
    }
    upsertTask({ id: 'release', type: 'release', title: '释放实例', done: r.done, total: r.total, busy: r.done < r.total })
  }

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
  const doExitRelease = async () => {
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
      {tasks.length > 0 && (
        <div className="fixed bottom-5 right-5 z-50 w-72 space-y-2 pointer-events-none">
          {tasks.map((t) => {
            const donePct = t.total > 0 ? Math.min((t.done / t.total) * 100, 100) : 0
            // 已完成仅当非忙态且 done>=total（停止扫描等忙态 done==total 时仍显示进行中文案）
            const finished = !t.busy && (t.total <= 0 || t.done >= t.total)
            return (
              <div
                key={t.id}
                className="pointer-events-auto bg-white rounded-xl border border-zinc-200 shadow-xl p-3.5"
              >
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[13px] font-semibold text-zinc-900">{t.title}</span>
                  <span className="flex items-center gap-1.5">
                    <span className="text-[12px] text-zinc-500 tabular-nums">
                      {t.done}/{t.total}
                    </span>
                    <button
                      type="button"
                      onClick={() => dismissTask(t.id, t.busy)}
                      className="p-0.5 rounded text-zinc-400 hover:text-zinc-700 hover:bg-zinc-100"
                      title="关闭（后台继续）"
                    >
                      <X size={13} />
                    </button>
                  </span>
                </div>
                <div className="h-1.5 bg-zinc-100 rounded-full overflow-hidden">
                  <div
                    className={clsx('h-full rounded-full transition-all duration-300', TASK_COLORS[t.type])}
                    style={{ width: `${donePct}%` }}
                  />
                </div>
                <div className={clsx('mt-1.5 text-[11px]', t.error ? 'text-red-500' : 'text-zinc-400')}>
                  {t.error ? '失败，详见页面提示' : finished ? '已完成' : TASK_TEXT[t.type]}
                </div>
              </div>
            )
          })}
        </div>
      )}
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
