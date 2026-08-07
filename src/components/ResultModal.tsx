import clsx from 'clsx'
import { X } from 'lucide-react'

/**
 * 扫描结果弹窗：扫描完成后展示可用节点数量，并提供【入池】【独享】两个操作入口。
 */
export default function ResultModal({
  okCount,
  total,
  busy,
  onClose,
  onPool,
  onSolo,
}: {
  /** 可用节点数（青绿色数字） */
  okCount: number
  /** 本次扫描总节点数 */
  total: number
  /** 正在执行入池/独享（动作中，按钮禁用） */
  busy: boolean
  onClose: () => void
  onPool: () => void
  onSolo: () => void
}) {
  const noOk = okCount <= 0
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-900/40"
      onClick={onClose}
    >
      <div
        className="bg-white rounded-2xl shadow-xl w-[380px] p-6 relative"
        onClick={(e) => e.stopPropagation()}
      >
        {/* 右上角关闭 */}
        <button
          type="button"
          onClick={onClose}
          className="absolute top-3 right-3 text-zinc-400 hover:text-zinc-600 rounded-md p-1 transition-colors"
          aria-label="关闭"
          title="关闭"
        >
          <X size={18} />
        </button>

        <div className="mt-2">
          <div className="text-[15px] font-medium text-zinc-800">
            已扫描到{' '}
            <span className={clsx('tabular-nums', noOk ? 'text-zinc-400' : 'text-teal-700')}>{okCount}</span>{' '}
            个可用节点{total > 0 ? `（共扫描 ${total} 个）` : ''}
          </div>

          {noOk && <div className="text-[12px] text-zinc-400 mt-2">没有可用节点，可调整后重新扫描</div>}

          <div className="flex gap-3 mt-5">
            <button
              type="button"
              onClick={onPool}
              disabled={noOk || busy}
              className="flex-1 py-2.5 rounded-lg text-[13px] font-medium text-white bg-green-600 hover:bg-green-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              {busy ? '处理中…' : '入实例池'}
            </button>
            <button
              type="button"
              onClick={onSolo}
              disabled={noOk || busy}
              className="flex-1 py-2.5 rounded-lg text-[13px] font-medium text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              {busy ? '处理中…' : '设为独享'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}