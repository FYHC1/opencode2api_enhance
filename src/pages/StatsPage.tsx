import { Fragment, useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { BarChart3, ChevronDown, ChevronRight, RefreshCw, Inbox } from 'lucide-react'
import { api, type StatsSummary } from '../lib/api'

/** 千分位格式化 */
const fmt = (n: number) => n.toLocaleString('en-US')

function Card({
  label,
  value,
  accent,
}: {
  label: string
  value: string
  accent?: boolean
}) {
  return (
    <div className="flex-1 min-w-[150px] bg-white rounded-[16px] border border-zinc-200 shadow-sm p-4">
      <div className="text-[12px] text-zinc-500 mb-1">{label}</div>
      <div className={clsx('text-[22px] font-semibold tabular-nums', accent ? 'text-teal-700' : 'text-zinc-900')}>
        {value}
      </div>
    </div>
  )
}

export default function StatsPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [stats, setStats] = useState<StatsSummary | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const load = useCallback(
    async (silent = true) => {
      try {
        const s = await api.getStats()
        setStats(s)
        setError(null)
      } catch (e) {
        if (!silent) toast(String(e), false)
        else setError(String(e))
      }
    },
    [toast],
  )

  // 自动轮询（静默，5s）
  useEffect(() => {
    void load()
    const t = setInterval(() => void load(true), 5000)
    return () => clearInterval(t)
  }, [load])

  // 手动刷新（带 loading）
  const doRefresh = async () => {
    setRefreshing(true)
    await load(false)
    setRefreshing(false)
  }

  const toggleExpand = (name: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const instances = stats?.instances ?? []
  const isEmpty = !stats || instances.length === 0

  return (
    <div className="p-6 flex flex-col gap-5">
      {/* 顶部工具条 */}
      <div className="flex items-center justify-between">
        <h1 className="text-[16px] font-semibold text-zinc-900 flex items-center gap-2">
          <BarChart3 size={18} className="text-teal-700" />
          Token 统计
        </h1>
        <button
          type="button"
          onClick={doRefresh}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-zinc-900 text-white text-[12px] font-medium hover:bg-zinc-700 transition-colors"
        >
          <RefreshCw size={13} className={refreshing ? 'animate-spin' : ''} />
          {refreshing ? '刷新中…' : '刷新'}
        </button>
      </div>

      {/* 总览卡片 */}
      <div className="flex flex-wrap gap-4">
        <Card label="总请求数" value={fmt(stats?.total_requests ?? 0)} />
        <Card label="总输入 Token" value={fmt(stats?.total_prompt_tokens ?? 0)} />
        <Card label="总输出 Token" value={fmt(stats?.total_completion_tokens ?? 0)} />
        <Card label="总 Token" value={fmt(stats?.total_tokens ?? 0)} accent />
      </div>

      {error && !stats && (
        <div className="text-[13px] text-red-600 bg-red-50 border border-red-100 rounded-xl px-4 py-3">
          加载失败：{error}
        </div>
      )}

      {/* 实例表格 */}
      <div className="bg-white rounded-[16px] border border-zinc-200 shadow-sm p-5">
        {isEmpty && !error ? (
          <div className="py-12 flex flex-col items-center gap-2 text-zinc-400">
            <Inbox size={28} strokeWidth={1.5} />
            <span className="text-[13px]">暂无统计数据，启动实例并产生对话后会自动记录</span>
          </div>
        ) : (
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-[12px] text-zinc-500 border-b border-zinc-100">
                <th className="py-2 pr-3 font-medium">实例</th>
                <th className="py-2 pr-3 font-medium text-right">请求数</th>
                <th className="py-2 pr-3 font-medium text-right">输入 Token</th>
                <th className="py-2 pr-3 font-medium text-right">输出 Token</th>
                <th className="py-2 pr-3 font-medium text-right">总计</th>
                <th className="py-2 font-medium">状态</th>
              </tr>
            </thead>
            <tbody>
              {instances.map((ins) => {
                const open = expanded.has(ins.name)
                return (
                  <Fragment key={ins.name}>
                    <tr
                      onClick={() => ins.models.length > 0 && toggleExpand(ins.name)}
                      className={clsx(
                        'border-b border-zinc-50 hover:bg-zinc-50/60 transition-colors',
                        ins.models.length > 0 ? 'cursor-pointer' : '',
                      )}
                    >
                      <td className="py-2.5 pr-3 font-medium text-zinc-800 flex items-center gap-1.5">
                        {ins.models.length > 0 ? (
                          open ? (
                            <ChevronDown size={14} className="text-zinc-400" />
                          ) : (
                            <ChevronRight size={14} className="text-zinc-400" />
                          )
                        ) : (
                          <span className="w-3.5" />
                        )}
                        {ins.name}
                      </td>
                      <td className="py-2.5 pr-3 text-right tabular-nums text-zinc-600">{fmt(ins.requests)}</td>
                      <td className="py-2.5 pr-3 text-right tabular-nums text-zinc-600">{fmt(ins.prompt_tokens)}</td>
                      <td className="py-2.5 pr-3 text-right tabular-nums text-zinc-600">{fmt(ins.completion_tokens)}</td>
                      <td className="py-2.5 pr-3 text-right tabular-nums font-medium text-zinc-900">{fmt(ins.total_tokens)}</td>
                      <td className="py-2.5">
                        {ins.exists ? (
                          <span className="inline-flex px-2 py-0.5 rounded-md bg-green-50 text-green-700 text-[11px] font-medium">
                            正常
                          </span>
                        ) : (
                          <span className="inline-flex px-2 py-0.5 rounded-md bg-zinc-100 text-zinc-500 text-[11px] font-medium">
                            已删除
                          </span>
                        )}
                      </td>
                    </tr>
                    {open && (
                      <tr key={`${ins.name}-detail`} className="bg-zinc-50/50">
                        <td colSpan={6} className="py-2 px-4">
                          <table className="w-full text-[12px]">
                            <thead>
                              <tr className="text-left text-zinc-400">
                                <th className="py-1.5 pr-3 font-medium">模型</th>
                                <th className="py-1.5 pr-3 font-medium text-right">请求数</th>
                                <th className="py-1.5 pr-3 font-medium text-right">输入</th>
                                <th className="py-1.5 pr-3 font-medium text-right">输出</th>
                                <th className="py-1.5 font-medium text-right">总计</th>
                              </tr>
                            </thead>
                            <tbody>
                              {ins.models.map((m) => (
                                <tr key={m.model} className="border-b border-zinc-100/60">
                                  <td className="py-1.5 pr-3 text-zinc-700">{m.model}</td>
                                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-500">{fmt(m.requests)}</td>
                                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-500">{fmt(m.prompt_tokens)}</td>
                                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-500">{fmt(m.completion_tokens)}</td>
                                  <td className="py-1.5 text-right tabular-nums font-medium text-zinc-700">{fmt(m.total_tokens)}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        )}
        {!isEmpty && (
          <div className="mt-3 text-[11px] text-zinc-400">
            每 5 秒自动刷新 · 已删除实例的统计仍保留在历史区
          </div>
        )}
      </div>
    </div>
  )
}
