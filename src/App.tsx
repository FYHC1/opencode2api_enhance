import { useState } from 'react'
import clsx from 'clsx'
import { Server, Layers, Radar, Settings, BarChart3, ScrollText } from 'lucide-react'
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

export default function App() {
  const [tab, setTab] = useState<Tab>('instances')
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)
  // 全局释放进度（实例池一键释放全部时跨页面常驻显示）
  const [release, setRelease] = useState<{ active: boolean; done: number; total: number }>({
    active: false,
    done: 0,
    total: 0,
  })

  const showToast = (msg: string, ok = true) => {
    setToast({ msg, ok })
    setTimeout(() => setToast(null), 3600)
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
          {tab === 'pool' && <PoolPage toast={showToast} onRelease={setRelease} />}
          {tab === 'nodes' && <NodesPage toast={showToast} />}
          {tab === 'stats' && <StatsPage toast={showToast} />}
          {tab === 'logs' && <LogsPage toast={showToast} />}
          {tab === 'settings' && <SettingsPage toast={showToast} />}
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

      {/* 全局释放进度悬浮面板（跨页面常驻，释放完成自动消失） */}
      {release.active && (
        <div className="fixed bottom-5 right-5 z-50 w-64 bg-white rounded-xl border border-zinc-200 shadow-xl p-3.5">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[13px] font-semibold text-zinc-900">释放实例中</span>
            <span className="text-[12px] text-zinc-500 tabular-nums">
              {release.done}/{release.total}
            </span>
          </div>
          <div className="h-1.5 bg-zinc-100 rounded-full overflow-hidden">
            <div
              className="h-full bg-red-500 rounded-full transition-all duration-300"
              style={{ width: release.total > 0 ? `${(release.done / release.total) * 100}%` : '0%' }}
            />
          </div>
          <div className="mt-1.5 text-[11px] text-zinc-400">
            {release.done >= release.total ? '已完成' : '正在释放，不影响你继续操作…'}
          </div>
        </div>
      )}
    </div>
  )
}
