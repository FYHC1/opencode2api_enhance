import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { Copy, Loader2, Power, RefreshCw, ShieldCheck, Network, Search, Play, Square, TestTube2, Trash2 } from 'lucide-react'
import { api, type GatewayStatus, type Instance } from '../lib/api'

function statusBadge(st: Instance['status']): [string, string] {
  if (st === 'Running') return ['bg-green-50 text-green-700', '健康']
  if (st === 'Stopped') return ['bg-zinc-100 text-zinc-500', '已停止']
  if (st === 'Starting' || st === 'Stopping') return ['bg-amber-50 text-amber-700', st === 'Starting' ? '启动中' : '停止中']
  if (st && typeof st === 'object' && 'Error' in st) return ['bg-red-50 text-red-600', `错误:${(st as { Error: string }).Error}`]
  return ['bg-zinc-100 text-zinc-500', '未知']
}

export default function PoolPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [gw, setGw] = useState<GatewayStatus | null>(null)
  const [instances, setInstances] = useState<Instance[]>([])
  const [stopping, setStopping] = useState(false)
  const [routeBusy, setRouteBusy] = useState(false)
  const [kickBusy, setKickBusy] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [searchFocus, setSearchFocus] = useState(false)
  // 单行操作忙态；全部操作忙态（start / stop / test）
  const [rowBusy, setRowBusy] = useState<Record<string, 'start' | 'stop' | 'test'>>({})
  const [allBusy, setAllBusy] = useState<'start' | 'stop' | 'test' | null>(null)

  // 池成员 = 已入池（join_gateway=true）的实例；支持前端搜索（名称/节点/IP/端口）
  const members = instances
    .filter((i) => i.join_gateway)
    .filter((i) => {
      const q = search.trim().toLowerCase()
      return (
        !q ||
        i.name.toLowerCase().includes(q) ||
        i.node.toLowerCase().includes(q) ||
        (i.ip || '').toLowerCase().includes(q) ||
        String(i.port).includes(q)
      )
    })

  const load = useCallback(async () => {
    try {
      const [g, ins] = await Promise.all([api.gatewayStatus(), api.listInstances()])
      setGw(g)
      setInstances(ins)
    } catch (e) {
      /* 轮询静默失败，保留上次状态 */
    }
  }, [])

  // 首次加载 + 轻量轮询（网关状态 / 实例健康会变化）
  useEffect(() => {
    void load()
    const timer = setInterval(() => void load(), 3000)
    return () => clearInterval(timer)
  }, [load])

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast(`已复制${label}`)
    } catch {
      /* ignore */
    }
  }

  const doStopGateway = async () => {
    if (!confirm('确定关闭统一网关？实例的入池标记会保留，重新启动后自动恢复。')) return
    setStopping(true)
    try {
      await api.gatewayStop()
      toast('已关闭统一网关')
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setStopping(false)
    }
  }

  const doSetRouteMode = async (mode: 'smart' | 'failover' | 'round_robin') => {
    if (!gw || gw.route_mode === mode) return
    setRouteBusy(true)
    try {
      await api.gatewaySetRouteMode(mode)
      const label = mode === 'smart' ? 'smart（默认：故障转移+健康计数+超时切换）' : mode === 'failover' ? 'failover（失败才切换）' : 'round_robin（轮询分发）'
      toast(`已切换路由模式：${label}`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setRouteBusy(false)
    }
  }

  // 释放池成员：一条龙（自动关闭实例 → 删除记录 → 释放节点），无「恢复独享」中间态
  const doRelease = async (name: string) => {
    if (!confirm(`确定释放实例 ${name}？将关闭实例并释放节点。`)) return
    setKickBusy(name)
    try {
      await api.removeInstance(name)
      toast(`已释放实例 ${name}`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setKickBusy(null)
    }
  }

  // 单行操作：启动 / 停止 / 测试（与实例页行为一致）
  const doRowOp = async (name: string, op: 'start' | 'stop' | 'test') => {
    setRowBusy((prev) => ({ ...prev, [name]: op }))
    try {
      if (op === 'start') {
        await api.startInstance(name)
        toast(`已启动实例 ${name}`)
      } else if (op === 'stop') {
        await api.stopInstance(name)
        toast(`已停止实例 ${name}`)
      } else {
        const r = await api.testInstance(name)
        if (r.ok) toast(`「${name}」测试通过：${r.message}（${r.latency_ms}ms）`)
        else toast(`「${name}」测试失败：${r.message}`, false)
      }
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setRowBusy((prev) => {
        const next = { ...prev }
        delete next[name]
        return next
      })
    }
  }

  // 全部操作：一键启动 / 一键停止 / 一键测试全部池成员
  const doAll = async (kind: 'start' | 'stop' | 'test') => {
    const names = members.map((i) => i.name)
    if (names.length === 0) {
      toast('池中暂无成员')
      return
    }
    setAllBusy(kind)
    try {
      let ok = 0
      let fail = 0
      if (kind === 'start' || kind === 'stop') {
        // 复用 Rust 并行命令（batch_start 4 worker / batch_stop 8 worker），避免前端串行
        const r = kind === 'start' ? await api.batchStart(names) : await api.batchStop(names)
        ok = r.success_count
        fail = r.error_count
      } else {
        // 测试无批量命令，逐个探测
        for (const n of names) {
          try {
            const r = await api.testInstance(n)
            if (!r.ok) throw new Error(r.message)
            ok++
          } catch {
            fail++
          }
        }
      }
      const label = kind === 'start' ? '启动' : kind === 'stop' ? '停止' : '测试'
      toast(`池成员${label}完成：成功 ${ok} 个${fail ? `，失败 ${fail}` : ''}`, fail === 0)
      await load()
    } finally {
      setAllBusy(null)
    }
  }

  const running = gw?.running ?? false
  const freeModels = gw?.free_models ?? []
  const freeModelsError = gw?.free_models_error ?? null

  return (
    <div className="p-6 space-y-4">
      {/* 工具条 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-900">实例池</h2>
          <span
            className={clsx(
              'flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium',
              running ? 'bg-green-50 text-green-700' : 'bg-zinc-100 text-zinc-500',
            )}
          >
            <span className={clsx('w-1.5 h-1.5 rounded-full', running ? 'bg-green-500' : 'bg-zinc-400')} />
            {running ? '网关运行中' : '网关未启动'}
          </span>
          <span className="px-2 py-0.5 rounded-full bg-zinc-100 text-zinc-500 text-xs font-medium">
            {members.length} 个池成员
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void load()}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50"
          >
            <RefreshCw size={14} /> 刷新
          </button>
          <button
            onClick={() => void doStopGateway()}
            disabled={!running || stopping}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-red-600 bg-red-50 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {stopping ? <Loader2 size={14} className="animate-spin" /> : <Power size={14} />}
            {stopping ? '关闭中…' : '一键关闭网关'}
          </button>
        </div>
      </div>

      {/* 网关状态卡 */}
      {/* 网关状态卡 */}
      <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-5">
        <div className="flex items-center gap-2 mb-4">
          <ShieldCheck size={16} className="text-teal-600" />
          <h3 className="text-[15px] font-semibold text-zinc-900">统一网关</h3>
          <span className="flex-1" />
          <span className="text-[12px] text-zinc-400">{gw?.message || '加载中…'}</span>
        </div>

        <div className="grid grid-cols-2 gap-4">
          {/* 地址 */}
          <div className="rounded-xl border border-zinc-100 bg-zinc-50/60 p-4">
            <div className="text-[12px] text-zinc-500 mb-1.5">统一 API 地址</div>
            <button
              onClick={() => void copyText(gw?.address ?? '', '统一 API 地址')}
              className="flex items-center gap-1 text-teal-700 hover:underline"
              title="点击复制"
            >
              <code className="text-[13px]">{gw?.address ?? 'http://127.0.0.1:18082/v1'}</code>
              <Copy size={12} />
            </button>
            <div className="mt-1 text-[11px] text-zinc-400">
              池内 {gw?.running_instances ?? 0} / 共 {gw?.total_instances ?? 0} 个运行实例
            </div>
          </div>

          {/* 密钥 */}
          <div className="rounded-xl border border-zinc-100 bg-zinc-50/60 p-4">
            <div className="text-[12px] text-zinc-500 mb-1.5">统一密钥</div>
            <button
              onClick={() => void copyText(gw?.api_key ?? 'sk-unified-local', '统一密钥')}
              className="flex items-center gap-1 text-zinc-600 hover:underline"
              title="点击复制"
            >
              <code className="text-[13px]">{gw?.api_key ?? 'sk-unified-local'}</code>
              <Copy size={12} />
            </button>
            <div className="mt-1 text-[11px] text-zinc-400">配置客户端时使用此地址 + 密钥</div>
          </div>
        </div>

        {/* 免费模型 */}
        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2">
          <span className="text-[12px] text-zinc-500">网关可用免费模型：</span>
          {gw?.free_models_loading ? (
            <span className="flex items-center gap-1 text-[12px] text-zinc-400">
              <Loader2 size={12} className="animate-spin" /> 探测中…
            </span>
          ) : freeModels.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {freeModels.map((m) => (
                <span key={m} className="px-2 py-0.5 rounded-md bg-teal-50 text-teal-700 text-[11px] font-medium">
                  {m}
                </span>
              ))}
            </div>
          ) : freeModelsError ? (
            <span className="text-[12px] text-red-500">探测失败：{freeModelsError}</span>
          ) : (
            <span className="text-[12px] text-zinc-400">—</span>
          )}
        </div>
      </div>

      {/* 路由模式 + 操作提示 */}
      <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-5 flex items-center justify-between">
        <div>
          <h3 className="text-[14px] font-semibold text-zinc-900 mb-0.5">路由模式</h3>
          <p className="text-[12px] text-zinc-400">
            smart（默认）：故障转移+健康计数+超时切换；failover：失败才切换；round_robin：轮询分发。
          </p>
        </div>
        <div className="flex items-center gap-2">
          {(['smart', 'failover', 'round_robin'] as const).map((m) => (
            <button
              key={m}
              onClick={() => void doSetRouteMode(m)}
              disabled={!running || routeBusy}
              className={clsx(
                'px-4 py-1.5 rounded-lg text-[13px] border transition-colors disabled:cursor-not-allowed disabled:opacity-50',
                gw?.route_mode === m
                  ? 'bg-zinc-900 text-white border-zinc-900'
                  : 'text-zinc-600 bg-white border-zinc-200 hover:bg-zinc-50',
              )}
            >
              {m === 'smart' ? 'smart（默认）' : m}
            </button>
          ))}
        </div>
      </div>

      {/* 池成员列表 */}
      <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm overflow-hidden">
        <div className="px-4 py-3 border-b border-zinc-100 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Network size={15} className="text-teal-600" />
            <span className="text-[14px] font-semibold text-zinc-900">池成员</span>
            <span className="text-[12px] text-zinc-400">已入池的实例会聚合到统一网关地址，未入池实例保持独享</span>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => void doAll('start')}
              disabled={members.length === 0 || !!allBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-green-600 hover:bg-green-700 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {allBusy === 'start' ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />} 全部启动
            </button>
            <button
              onClick={() => void doAll('stop')}
              disabled={members.length === 0 || !!allBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {allBusy === 'stop' ? <Loader2 size={14} className="animate-spin" /> : <Square size={14} />} 全部停止
            </button>
            <button
              onClick={() => void doAll('test')}
              disabled={members.length === 0 || !!allBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-teal-700 bg-teal-50 border border-teal-100 hover:bg-teal-100 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {allBusy === 'test' ? <Loader2 size={14} className="animate-spin" /> : <TestTube2 size={14} />} 一键测试
            </button>
            <div
              className={clsx(
                'relative flex items-center rounded-lg border border-zinc-200 bg-white transition-all duration-200 overflow-hidden',
                searchFocus || search ? 'w-52' : 'w-9',
              )}
            >
              <Search size={14} className="absolute left-2.5 text-zinc-400 pointer-events-none" />
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                onFocus={() => setSearchFocus(true)}
                onBlur={() => setSearchFocus(false)}
                placeholder="搜索池成员"
                className={clsx(
                  'w-full bg-transparent py-1.5 pl-8 pr-2 text-[12px] outline-none placeholder:text-zinc-300 transition-opacity',
                  searchFocus || search ? 'opacity-100' : 'opacity-0',
                )}
              />
            </div>
          </div>
        </div>
{members.length > 0 ? (
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-zinc-400 border-b border-zinc-100">
                <th className="py-3 pl-4">名称 / 节点 IP</th>
                <th className="py-3 pl-2">端口</th>
                <th className="py-3 pl-2">健康状态</th>
                <th className="py-3 pl-2 pr-4 text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {members.map((i) => {
                const [cls, label] = statusBadge(i.status)
                return (
                  <tr key={i.name} className="border-b border-zinc-50 hover:bg-zinc-50/50">
                    <td className="py-2.5 pl-4">
                      <div className="font-medium text-zinc-800">{i.node}</div>
                      <div className="text-[11px] text-zinc-400">
                        {i.ip ? (
                          <button
                            onClick={() => void copyText(i.ip, '节点 IP')}
                            className="flex items-center gap-1 text-zinc-400 hover:text-zinc-600 hover:underline"
                            title="点击复制"
                          >
                            <code className="text-[12px]">{i.ip}</code>
                            <Copy size={10} />
                          </button>
                        ) : (
                          '—'
                        )}
                      </div>
                    </td>
                    <td className="py-2.5 pl-2 text-zinc-500">{i.port}</td>
                    <td className="py-2.5 pl-2">
                      <span className={clsx('inline-block px-2 py-0.5 rounded-full text-xs font-medium', cls)}>{label}</span>
                    </td>
<td className="py-2.5 pl-2 pr-4">
                      <div className="flex items-center justify-end gap-1.5">
                        {i.status === 'Running' ? (
                          <button
                            onClick={() => void doRowOp(i.name, 'stop')}
                            disabled={!!rowBusy[i.name]}
                            className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-zinc-700 bg-zinc-100 hover:bg-zinc-200 disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {rowBusy[i.name] === 'stop' ? <Loader2 size={12} className="animate-spin" /> : null}
                            {rowBusy[i.name] === 'stop' ? '停止中…' : '停止'}
                          </button>
                        ) : (
                          <button
                            onClick={() => void doRowOp(i.name, 'start')}
                            disabled={!!rowBusy[i.name]}
                            className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-white bg-green-600 hover:bg-green-700 disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {rowBusy[i.name] === 'start' ? <Loader2 size={12} className="animate-spin" /> : null}
                            {rowBusy[i.name] === 'start' ? '启动中…' : '启动'}
                          </button>
                        )}
                        <button
                          onClick={() => void doRowOp(i.name, 'test')}
                          disabled={!!rowBusy[i.name]}
                          className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-teal-700 bg-teal-50 hover:bg-teal-100 disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          <TestTube2 size={12} /> 测试
                        </button>
<button
                          onClick={() => void doRelease(i.name)}
                          disabled={kickBusy === i.name || !!rowBusy[i.name]}
                          className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-red-600 bg-red-50 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {kickBusy === i.name ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                          {kickBusy === i.name ? '释放中…' : '释放'}
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        ) : (
          <div className="flex flex-col items-center justify-center py-16 text-zinc-400">
<p className="text-[13px] mb-1">暂无池成员</p>
<p className="text-[12px]">在「节点池」页勾选节点，以「进池」方式批量添加（聚合到统一网关）</p>
          </div>
        )}
      </div>
    </div>
  )
}