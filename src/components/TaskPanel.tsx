import { memo } from 'react'
import clsx from 'clsx'
import { X } from 'lucide-react'
import type { TaskItem } from '../App'

// V2: 任务颜色（按类型区分进度条）与悬浮文案
const TASK_COLORS: Record<TaskItem['type'], string> = {
  release: 'bg-red-500',
  'stop-scan': 'bg-amber-500',
  restart: 'bg-amber-500',
  scan: 'bg-teal-500',
  batch: 'bg-teal-500',
}

const TASK_TEXT: Record<TaskItem['type'], string> = {
  release: '正在释放，不影响你继续操作…',
  scan: '正在扫描，不影响你继续操作…',
  'stop-scan': '正在停止，不影响你继续操作…',
  restart: '正在重启实例池…',
  batch: '正在处理，不影响你继续操作…',
}

// V2: 全局任务悬浮面板（跨页面常驻；✕ 仅隐藏该条，后台继续）
// M10: memo 隔离——只随 tasks 引用变化重渲染，App 其它状态变化不影响本面板
export default memo(function TaskPanel({
  tasks,
  onDismiss,
}: {
  tasks: TaskItem[]
  onDismiss: (id: string, busy?: boolean) => void
}) {
  return (
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
                  onClick={() => onDismiss(t.id, t.busy)}
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
  )
})