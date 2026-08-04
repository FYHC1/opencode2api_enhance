import { useState } from 'react'
import clsx from 'clsx'
import { Server, Layers, Radar, Settings, BarChart3 } from 'lucide-react'
import { TitleBar } from './components/TitleBar'
import InstancesPage from './pages/InstancesPage'
import PoolPage from './pages/PoolPage'
import NodesPage from './pages/NodesPage'
import SettingsPage from './pages/SettingsPage'
import StatsPage from './pages/StatsPage'

type Tab = 'instances' | 'pool' | 'nodes' | 'settings' | 'stats'

const NAV: { id: Tab; label: string; icon: typeof Server }[] = [
  { id: 'instances', label: '实例', icon: Server },
  { id: 'pool', label: '实例池', icon: Layers },
  { id: 'nodes', label: '节点扫描', icon: Radar },
  { id: 'stats', label: '统计', icon: BarChart3 },
  { id: 'settings', label: '设置', icon: Settings },
]

export default function App() {
  const [tab, setTab] = useState<Tab>('instances')
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)

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
          {tab === 'pool' && <PoolPage toast={showToast} />}
          {tab === 'nodes' && <NodesPage toast={showToast} />}
          {tab === 'stats' && <StatsPage toast={showToast} />}
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
    </div>
  )
}
