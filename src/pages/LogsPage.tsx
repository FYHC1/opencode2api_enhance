import { useCallback, useEffect, useMemo, useState } from 'react'
import clsx from 'clsx'
import {
  Activity,
  ChevronDown,
  ChevronRight,
  Download,
  Filter,
  Inbox,
  LayoutList,
  RefreshCw,
  Table2,
  Trash2,
} from 'lucide-react'
import {
  api,
  downloadText,
  type CallLogAggregate,
  type CallLogFilter,
  type CallLogRecord,
} from '../lib/api'

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

const inputCls =
  'border border-zinc-200 rounded-lg px-2.5 py-1.5 text-sm text-zinc-800 focus:outline-none focus:border-zinc-400 bg-white'

export default function LogsPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [logs, setLogs] = useState<CallLogRecord[]>([])
  const [agg, setAgg] = useState<CallLogAggregate[]>([])
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [onlyIssues, setOnlyIssues] = useState(false)
  // 按天筛选：'' = 全部日期
  const [dateFilter, setDateFilter] = useState('')
  // 视图切换：日志列表 / 汇总 / 时段分析 / 节点分析
  const [view, setView] = useState<'list' | 'agg' | 'hour' | 'node'>('list')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const [fNode, setFNode] = useState('')
  const [fKeyword, setFKeyword] = useState('')
  const [fStatus, setFStatus] = useState('')

  const buildFilter = useCallback(
    (): CallLogFilter => ({
      node: fNode.trim() || undefined,
      keyword: fKeyword.trim() || undefined,
      status: fStatus || undefined,
      limit: 500,
      offset: 0,
    }),
    [fNode, fKeyword, fStatus],
  )

  const load = useCallback(
    async (silent = true) => {
      try {
        if (view === 'agg') {
          setAgg(await api.callLogAggregate())
        } else {
          const recs = await api.callLogFiltered(buildFilter())
          setLogs(recs)
        }
        setError(null)
      } catch (e) {
        if (!silent) toast(String(e), false)
        else setError(String(e))
      }
    },
    [view, buildFilter, toast],
  )

  // 自动轮询（静默，5s）
  useEffect(() => {
    void load()
    const t = setInterval(() => void load(true), 5000)
    return () => clearInterval(t)
  }, [load])

  // 过滤条件变化立即重查（防抖 300ms）
  useEffect(() => {
    if (view === 'agg') return
    const t = setTimeout(() => void load(true), 300)
    return () => clearTimeout(t)
  }, [fNode, fKeyword, fStatus, view, load])

  const doRefresh = async () => {
    setRefreshing(true)
    await load(false)
    setRefreshing(false)
  }

  const doExportCsv = async () => {
    try {
      const text = await api.exportCallLogCsv()
      downloadText(`call-log-${Date.now()}.csv`, text)
      toast('日志 CSV 已导出', true)
    } catch (e) {
      toast(String(e), false)
    }
  }

  const doClearLog = async () => {
    if (!confirm('确定清空全部调用日志？该操作不可恢复。')) return
    try {
      await api.clearCallLog()
      setLogs([])
      toast('日志已清空')
    } catch (e) {
      toast(String(e), false)
    }
  }

  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  // 日志中出现的日期（YYYY-MM-DD，新→旧），供按天筛选
  const dates = useMemo(() => {
    const s = new Set<string>()
    for (const l of logs) {
      const d = (l.ts || '').slice(0, 10)
      if (d) s.add(d)
    }
    return [...s].sort().reverse()
  }, [logs])

  const visible = useMemo(() => {
    return logs.filter((l) => {
      if (onlyIssues && !hasIssue(l)) return false
      if (dateFilter && (l.ts || '').slice(0, 10) !== dateFilter) return false
      return true
    })
  }, [logs, onlyIssues, dateFilter])

  const okCount = logs.filter((l) => l.status === 'ok').length
  const failCount = logs.length - okCount
  const issueCount = logs.filter(hasIssue).length

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <div className="flex items-center justify-between mb-5">
        <h1 className="text-2xl font-semibold text-zinc-900">调用日志</h1>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void doExportCsv()}
            className="flex items-center gap-2 border border-zinc-200 text-zinc-700 rounded-lg px-4 py-2 text-sm hover:bg-zinc-50"
          >
            <Download size={14} />
            导出 CSV
          </button>
          <button
            onClick={() => void doClearLog()}
            disabled={logs.length === 0}
            className="flex items-center gap-2 bg-white border border-zinc-200 text-zinc-600 rounded-lg px-4 py-2 text-sm hover:bg-zinc-50 disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <Trash2 size={14} />
            清空
          </button>
          <button
            onClick={doRefresh}
            disabled={refreshing}
            className="flex items-center gap-2 bg-zinc-900 text-white rounded-lg px-4 py-2 text-sm hover:bg-zinc-700 disabled:opacity-50"
          >
            <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
            刷新
          </button>
        </div>
      </div>

      {/* 视图切换：日志列表 / 汇总 / 时段分析 / 节点分析 */}
      <div className="flex items-center gap-1 mb-4 bg-zinc-100/80 rounded-xl p-1 w-fit">
        {(
          [
            ['list', '日志列表', LayoutList],
            ['agg', '汇总', Table2],
            ['hour', '时段分析', Activity],
            ['node', '节点分析', Filter],
          ] as const
        ).map(([id, label, Icon]) => (
          <button
            key={id}
            type="button"
            onClick={() => setView(id)}
            className={clsx(
              'flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-[13px] font-medium transition-colors',
              view === id ? 'bg-white text-zinc-900 shadow-sm' : 'text-zinc-500 hover:text-zinc-700',
            )}
          >
            <Icon size={13} />
            {label}
          </button>
        ))}
      </div>

      {/* 过滤栏 */}
      <div className="bg-white rounded-2xl border p-4 mb-4 flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 text-sm text-zinc-500">
          <Filter size={14} />
          <span className="text-zinc-700">过滤</span>
        </div>
        <input
          value={fNode}
          onChange={(e) => setFNode(e.target.value)}
          placeholder="节点名包含"
          className={clsx(inputCls, 'w-36')}
        />
        <input
          value={fKeyword}
          onChange={(e) => setFKeyword(e.target.value)}
          placeholder="关键词（模型/路径/错误）"
          className={clsx(inputCls, 'w-52')}
        />
        <select
          value={fStatus}
          onChange={(e) => setFStatus(e.target.value)}
          className={clsx(inputCls, 'w-28')}
        >
          <option value="">全部状态</option>
          <option value="ok">成功</option>
          <option value="error">失败/异常</option>
        </select>
      </div>

      {error && <div className="text-red-600 text-sm mb-4">加载失败：{error}</div>}

      {/* 汇总 + 只看失败 + 按天筛选（列表视图） */}
      {view === 'list' && (
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
          {dates.length > 1 && (
            <select
              value={dateFilter}
              onChange={(e) => setDateFilter(e.target.value)}
              className="px-2.5 py-1.5 rounded-lg border border-zinc-200 bg-white text-[13px] text-zinc-600 outline-none"
              title="按日期筛选"
            >
              <option value="">全部日期（{dates.length} 天）</option>
              {dates.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
          )}
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
      )}

      {/* 汇总视图 */}
      {view === 'agg' && (
        agg.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-zinc-400">
            <Inbox size={40} strokeWidth={1.5} />
            <p className="mt-3 text-sm">暂无日志</p>
            <p className="text-xs mt-1">网关尚未记录调用（需以网关模式运行）</p>
          </div>
        ) : (
          <div className="bg-white rounded-2xl border overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-zinc-500 border-b bg-zinc-50/60">
                  <th className="px-4 py-2.5 font-medium">节点组合</th>
                  <th className="px-4 py-2.5 font-medium text-right w-28">总条数</th>
                  <th className="px-4 py-2.5 font-medium text-right w-28">异常/错误</th>
                  <th className="px-4 py-2.5 font-medium text-right w-48">最近时间</th>
                </tr>
              </thead>
              <tbody>
                {agg.map((a) => (
                  <tr key={a.instance} className="border-b border-zinc-100 last:border-0">
                    <td className="px-4 py-2.5 font-mono text-xs text-zinc-800 break-all">
                      {a.instance}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums text-zinc-800">{a.total}</td>
                    <td
                      className={clsx(
                        'px-4 py-2.5 text-right tabular-nums',
                        a.errors > 0 ? 'text-red-600 font-medium' : 'text-zinc-400',
                      )}
                    >
                      {a.errors}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs text-zinc-500 tabular-nums">
                      {fmtTime(a.last_ts)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}

      {/* 日志明细（列表视图） */}
      {view === 'list' && (
        visible.length === 0 ? (
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
                        {isExpanded ? <ChevronDown size={16} className="rotate-180" /> : <ChevronRight size={16} />}
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
                              {ev.node && ev.detail ? ' —' : ''}
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
        )
      )}

      {/* 时段分析 */}
      {view === 'hour' && <HourAnalysisView logs={logs} />}
      {/* 节点分析 */}
      {view === 'node' && <NodeAnalysisView logs={logs} />}

      <div className="flex items-center gap-2 text-zinc-400 text-xs mt-4">
        <Activity size={12} />
        每 5 秒自动刷新 · 保留上限可在设置页调整
      </div>
    </div>
  )
}

/** 超时/错误类事件（用于分析统计） */
const ISSUE_EVENT_TYPES = [
  'switch',
  'ttft_timeout',
  'silence_timeout',
  'stream_interrupt',
  'stream_error',
  'connect_error',
  'upstream_error',
  'all_failed',
]

type HourStat = {
  hour: number
  requests: number
  ok: number
  totalMs: number
  issueCount: number
}

type NodeStat = {
  node: string
  requests: number
  ok: number
  totalMs: number
  issueCount: number
}

const fmtPct = (n: number) => `${(n * 100).toFixed(0)}%`

/** 时段分析视图：按小时聚合请求数/平均耗时/失败率/异常次数（纯 CSS 条形图） */
function HourAnalysisView({ logs }: { logs: CallLogRecord[] }) {
  const hours = useMemo(() => {
    const arr: HourStat[] = Array.from({ length: 24 }, (_, i) => ({
      hour: i,
      requests: 0,
      ok: 0,
      totalMs: 0,
      issueCount: 0,
    }))
    for (const l of logs) {
      const d = new Date(l.ts)
      if (Number.isNaN(d.getTime())) continue
      const s = arr[d.getHours()]!
      s.requests++
      if (l.status === 'ok') s.ok++
      s.totalMs += l.duration_ms ?? 0
      s.issueCount += (l.events ?? []).filter((e) => ISSUE_EVENT_TYPES.includes(e.type)).length
    }
    return arr
  }, [logs])

  const withData = hours.filter((h) => h.requests > 0)
  const maxReq = Math.max(1, ...hours.map((h) => h.requests))

  return (
    <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-5">
      <div className="text-[14px] font-semibold text-zinc-900 mb-1">时段分析</div>
      <div className="text-[12px] text-zinc-400 mb-4">
        按小时统计请求分布与耗时，帮助定位一天中相对卡顿的时段（数据来自保留期内的调用日志）
      </div>
      {withData.length === 0 ? (
        <div className="py-12 text-center text-zinc-400 text-sm">暂无日志数据</div>
      ) : (
        <>
          {/* 24 小时条形图（柱高 ∝ 请求数） */}
          <div className="flex items-end gap-[3px] h-32 mb-4">
            {hours.map((h) => (
              <div key={h.hour} className="flex-1 flex flex-col justify-end items-center h-full group relative" title={`${String(h.hour).padStart(2, '0')} 时：${h.requests} 请求`}>
                {h.requests > 0 && (
                  <>
                    <div
                      className={clsx(
                        'w-full rounded-t-sm transition-all',
                        h.requests > 0 && h.ok / h.requests >= 0.9 ? 'bg-teal-500' : 'bg-amber-400',
                      )}
                      style={{ height: `${Math.max((h.requests / maxReq) * 100, 4)}%` }}
                    />
                    <div className="absolute bottom-full mb-1 hidden group-hover:block bg-zinc-900 text-white text-[10px] rounded px-1.5 py-0.5 whitespace-nowrap z-10">
                      {String(h.hour).padStart(2, '0')} 时 · {h.requests} 请求 · 均耗 {fmtDur(h.requests ? Math.round(h.totalMs / h.requests) : 0)}
                    </div>
                  </>
                )}
              </div>
            ))}
          </div>
          {/* 明细表 */}
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-[12px] text-zinc-500 border-b border-zinc-100">
                <th className="py-2 pr-3 font-medium">时段</th>
                <th className="py-2 pr-3 font-medium text-right">请求数</th>
                <th className="py-2 pr-3 font-medium text-right">平均耗时</th>
                <th className="py-2 pr-3 font-medium text-right">失败率</th>
                <th className="py-2 font-medium text-right">异常/切换</th>
              </tr>
            </thead>
            <tbody>
              {withData.map((h) => (
                <tr key={h.hour} className="border-b border-zinc-50">
                  <td className="py-1.5 pr-3 text-zinc-800">{String(h.hour).padStart(2, '0')}:00 - {String(h.hour).padStart(2, '0')}:59</td>
                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-600">{h.requests}</td>
                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-600">{fmtDur(Math.round(h.totalMs / h.requests))}</td>
                  <td className="py-1.5 pr-3 text-right tabular-nums text-zinc-600">{fmtPct(1 - h.ok / h.requests)}</td>
                  <td className="py-1.5 text-right tabular-nums text-zinc-600">{h.issueCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  )
}

/** 节点分析视图：按节点聚合请求数/成功率/平均耗时/异常次数（排序表 + 成功率条） */
function NodeAnalysisView({ logs }: { logs: CallLogRecord[] }) {
  const nodes = useMemo(() => {
    const m = new Map<string, NodeStat>()
    for (const l of logs) {
      // 最终节点：nodes 链最后一项；无则归「未知」
      const node = l.nodes?.slice(-1)[0] ?? '未知'
      let s = m.get(node)
      if (!s) {
        s = { node, requests: 0, ok: 0, totalMs: 0, issueCount: 0 }
        m.set(node, s)
      }
      s.requests++
      if (l.status === 'ok') s.ok++
      s.totalMs += l.duration_ms ?? 0
      s.issueCount += (l.events ?? []).filter((e) => ISSUE_EVENT_TYPES.includes(e.type)).length
    }
    return [...m.values()].sort((a, b) => b.requests - a.requests || b.issueCount - a.issueCount)
  }, [logs])

  return (
    <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-5">
      <div className="text-[14px] font-semibold text-zinc-900 mb-1">节点分析</div>
      <div className="text-[12px] text-zinc-400 mb-4">
        按最终出口节点聚合，评估各节点请求量、成功率与稳定性（数据来自保留期内的调用日志）
      </div>
      {nodes.length === 0 ? (
        <div className="py-12 text-center text-zinc-400 text-sm">暂无日志数据</div>
      ) : (
        <table className="w-full text-[13px]">
          <thead>
            <tr className="text-left text-[12px] text-zinc-500 border-b border-zinc-100">
              <th className="py-2 pr-3 font-medium">节点</th>
              <th className="py-2 pr-3 font-medium text-right">请求数</th>
              <th className="py-2 pr-3 font-medium text-right">成功率</th>
              <th className="py-2 pr-3 font-medium text-right">平均耗时</th>
              <th className="py-2 font-medium text-right">异常/切换</th>
            </tr>
          </thead>
          <tbody>
            {nodes.map((n) => {
              const rate = n.requests > 0 ? n.ok / n.requests : 0
              return (
                <tr key={n.node} className="border-b border-zinc-50">
                  <td className="py-2 pr-3">
                    <div className="flex items-center gap-2">
                      <span className="text-zinc-800 font-mono text-[12px] truncate max-w-[220px]">{n.node}</span>
                      <div className="flex-1 h-1.5 bg-zinc-100 rounded-full overflow-hidden min-w-[60px]">
                        <div
                          className={clsx('h-full rounded-full', rate >= 0.9 ? 'bg-teal-500' : rate >= 0.5 ? 'bg-amber-400' : 'bg-red-400')}
                          style={{ width: `${rate * 100}%` }}
                        />
                      </div>
                    </div>
                  </td>
                  <td className="py-2 pr-3 text-right tabular-nums text-zinc-600">{n.requests}</td>
                  <td className="py-2 pr-3 text-right tabular-nums text-zinc-600">{fmtPct(rate)}</td>
                  <td className="py-2 pr-3 text-right tabular-nums text-zinc-600">{fmtDur(Math.round(n.totalMs / n.requests))}</td>
                  <td className="py-2 text-right tabular-nums text-zinc-600">{n.issueCount}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}
