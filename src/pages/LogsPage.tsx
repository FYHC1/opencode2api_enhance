import { useCallback, useEffect, useMemo, useState } from 'react'
import clsx from 'clsx'
import {
  Activity,
  ChevronDown,
  ChevronRight,
  Filter,
  Inbox,
  RefreshCw,
} from 'lucide-react'
import { api, type CallLogRecord } from '../lib/api'

const fmtTime = (ts: string) => {
  try {
    const d = new Date(ts)
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch {
    return ts
  }
}

const fmtDur = (ms?: number) => {
  if (!ms || ms <= 0) return '-'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

/** 是否有需要展开详细展示的事件（异常/切换） */
const hasIssue = (rec: CallLogRecord) => {
  if (rec.status !== 'ok') return true
  return (rec.events ?? []).some((e) =>
    ['switch', 'ttft_timeout', 'silence_timeout', 'stream_interrupt', 'stream_error', 'connect_error', 'upstream_error', 'all_failed'].includes(e.type),
  )
}

const issueLabel = (rec: CallLogRecord): string => {
  const ev = rec.events ?? []
  if (ev.some((e) => e.type === 'all_failed')) return '全部节点失败'
  if (ev.some((e) => e.type === 'switch')) return '已切换节点'
  if (ev.some((e) => e.type === 'ttft_timeout')) return '首字超时'
  if (ev.some((e) => e.type === 'silence_timeout')) return '静默超时'
  if (ev.some((e) => e.type === 'stream_interrupt')) return '流中断'
  if (ev.some((e) => e.type === 'stream_error')) return '流错误'
  if (ev.some((e) => e.type === 'connect_error')) return '连接失败'
  if (ev.some((e) => e.type === 'upstream_error')) return '上游错误'
  return '异常'
}

export default function LogsPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [logs, setLogs] = useState<CallLogRecord[]>([])
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [onlyIssues, setOnlyIssues] = useState(false)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const load = useCallback(
    async (silent = true) => {
      try {
        const recs = await api.getCallLog(5000)
        // 最新在前
        setLogs([...recs].reverse())
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

  const doRefresh = async () => {
    setRefreshing(true)
    await load(false)
    setRefreshing(false)
  }

  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const visible = useMemo(() => {
    if (!onlyIssues) return logs
    return logs.filter(hasIssue)
  }, [logs, onlyIssues])

  const okCount = logs.filter((l) => l.status === 'ok').length
  const failCount = logs.length - okCount
  const issueCount = logs.filter(hasIssue).length

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <div className="flex items-center justify-between mb-5">
        <h1 className="text-2xl font-semibold text-zinc-900">调用日志</h1>
        <button
          onClick={doRefresh}
          disabled={refreshing}
          className="flex items-center gap-2 bg-zinc-900 text-white rounded-lg px-4 py-2 text-sm hover:bg-zinc-700 disabled:opacity-50"
        >
          <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
          刷新
        </button>
      </div>

      {/* 汇总 + 过滤 */}
      <div className="bg-white rounded-2xl border p-4 mb-4 flex flex-wrap items-center gap-4">
        <div className="flex gap-5 text-sm">
          <span className="text-zinc-600">
            共 <b className="text-zinc-900">{logs.length}</b> 条
          </span>
          <span className="text-green-600">
            【成功】<b>{okCount}</b>
          </span>
          <span className="text-red-600">
            【失败】<b>{failCount}</b>
          </span>
          <span className="text-amber-600">
            异常/切换 <b>{issueCount}</b>
          </span>
        </div>
        <label className="flex items-center gap-2 text-sm text-zinc-600 cursor-pointer ml-auto">
          <input
            type="checkbox"
            checked={onlyIssues}
            onChange={(e) => setOnlyIssues(e.target.checked)}
            className="accent-zinc-900"
          />
          <Filter size={14} />
          只看失败/切换
        </label>
      </div>

      {error && <div className="text-red-600 text-sm mb-4">加载失败：{error}</div>}

      {/* 日志列表 */}
      {visible.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-zinc-400">
          <Inbox size={40} strokeWidth={1.5} />
          <p className="mt-3 text-sm">暂无日志</p>
          <p className="text-xs mt-1">
            {logs.length === 0 ? '网关尚未记录调用（需以网关模式运行）' : '没有匹配的日志'}
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {visible.map((rec) => {
            const issue = hasIssue(rec)
            const isExpanded = expanded.has(rec.req_id)
            const nodes = rec.nodes ?? []
            return (
              <div
                key={rec.req_id}
                className={clsx(
                  'rounded-2xl border bg-white overflow-hidden',
                  issue ? 'border-amber-300/70' : 'border-zinc-200',
                )}
              >
                {/* 成功：一行简短；异常：可展开 */}
                <button
                  type="button"
                  onClick={() => issue && toggleExpand(rec.req_id)}
                  className={clsx(
                    'w-full flex items-center gap-3 px-4 py-2.5 text-left',
                    issue && 'cursor-pointer hover:bg-zinc-50',
                  )}
                >
                  <span
                    className={clsx(
                      'shrink-0 w-[68px] text-center text-xs font-medium rounded-md py-0.5',
                      rec.status === 'ok' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700',
                    )}
                  >
                    {rec.status === 'ok' ? '【成功】' : '【失败】'}
                  </span>
                  {issue && (
                    <span className="shrink-0 text-[11px] text-amber-700 bg-amber-100 rounded-md px-2 py-0.5">
                      {issueLabel(rec)}
                    </span>
                  )}
                  <span className="text-zinc-500 text-xs tabular-nums shrink-0">{fmtTime(rec.ts)}</span>
                  <span className="text-zinc-800 text-sm font-medium truncate flex-1">
                    {rec.model || '-'}
                  </span>
                  {nodes.length > 0 && (
                    <span className="text-zinc-500 text-xs truncate hidden sm:inline">
                      {nodes.join(' → ')}
                    </span>
                  )}
                  <span className="text-zinc-400 text-xs tabular-nums shrink-0">
                    {fmtDur(rec.duration_ms)}
                  </span>
                  {issue && (
                    <span className="shrink-0 text-zinc-400">
                      {isExpanded ? <ChevronUpIcon /> : <ChevronRight size={16} />}
                    </span>
                  )}
                </button>

                {/* 异常/切换：整块详细时间线 */}
                {issue && isExpanded && (
                  <div className="border-t border-zinc-100 px-4 py-3 bg-zinc-50/60">
                    <div className="text-xs text-zinc-500 mb-2 font-mono break-all">
                      req_id: {rec.req_id} · {rec.path || '/v1/chat/completions'} · stream: {rec.stream ? '是' : '否'} · 路由: {rec.route_mode || '-'}
                      {rec.err_msg && <span className="text-red-600"> · 错误: {rec.err_msg}</span>}
                    </div>
                    <div className="text-xs text-zinc-500 mb-2">
                      token: 输入 {rec.prompt_tokens ?? 0} / 输出 {rec.completion_tokens ?? 0} · 耗时 {fmtDur(rec.duration_ms)}
                    </div>
                    <div className="space-y-1.5">
                      {(rec.events ?? []).map((ev, i) => (
                        <div key={i} className="flex items-start gap-2 text-xs">
                          <span className="shrink-0 w-24 text-zinc-400 tabular-nums">{fmtTime(ev.at ?? '')}</span>
                          <span
                            className={clsx(
                              'shrink-0 w-28 rounded px-1.5 py-0.5 text-center font-medium',
                              ev.type === 'switch' ? 'bg-amber-100 text-amber-800'
                                : ev.type === 'connect_ok' || ev.type === 'complete' ? 'bg-green-100 text-green-700'
                                : ev.type === 'all_failed' ? 'bg-red-100 text-red-700'
                                : 'bg-zinc-200 text-zinc-700',
                            )}
                          >
                            {ev.type}
                          </span>
                          <span className="text-zinc-700 break-all">
                            {ev.node && <b className="text-zinc-900">{ev.node}</b>}
                            {ev.node && ev.detail ? ' — ' : ''}
                            {ev.detail}
                          </span>
                        </div>
                      ))}
                      {rec.events?.length === 0 && (
                        <span className="text-zinc-400">无事件明细</span>
                      )}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* 空态提示 */}
      {logs.length > 0 && visible.length === 0 && null}
      <div className="flex items-center gap-2 text-zinc-400 text-xs mt-4">
        <Activity size={12} />
        每 5 秒自动刷新 · 保留上限可在设置页调整
      </div>
    </div>
  )
}

function ChevronUpIcon() {
  return <ChevronDown size={16} className="rotate-180" />
}
