import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { Copy, Loader2, Power, RefreshCw, ShieldCheck, Network, Search } from 'lucide-react'
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

  const doSetRouteMode = async (mode: 'failover' | 'round_robin') => {
    if (!gw || gw.route_mode === mode) return
    setRouteBusy(true)
    try {
      await api.gatewaySetRouteMode(mode)
      toast(`已切换路由模式：${mode === 'failover' ? 'failover（失败才切换）' : 'round_robin（轮询分发）'}`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setRouteBusy(false)
    }
  }

  const doKick = async (name: string) => {
    setKickBusy(name)
    try {
      await api.setJoinGateway(name, false)
      toast(`已将实例 ${name} 移出实例池（恢复独享）`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setKickBusy(null)
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
              <code className="text-[13px]">{gw?.address ?? 'http://127.0.0.1:18080/v1'}</code>
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
            failover：当前实例失败才切下一个健康实例（推荐）；round_robin：轮询分发到池内实例。
          </p>
        </div>
        <div className="flex items-center gap-2">
          {['failover', 'round_robin'].map((m) => (
            <button
              key={m}
              onClick={() => void doSetRouteMode(m as 'failover' | 'round_robin')}
              disabled={!running || routeBusy}
              className={clsx(
                'px-4 py-1.5 rounded-lg text-[13px] border transition-colors disabled:cursor-not-allowed disabled:opacity-50',
                gw?.route_mode === m
                  ? 'bg-zinc-900 text-white border-zinc-900'
                  : 'text-zinc-600 bg-white border-zinc-200 hover:bg-zinc-50',
              )}
            >
              {m === 'failover' ? 'failover' : 'round_robin'}
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
                      <div className="flex items-center justify-end">
                        <button
                          onClick={() => void doKick(i.name)}
                          disabled={kickBusy === i.name}
                          className="flex items-center gap-1 px-2.5 py-1 rounded-lg text-[12px] text-red-600 bg-red-50 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {kickBusy === i.name ? <Loader2 size={12} className="animate-spin" /> : null}
                          {kickBusy === i.name ? '移出中…' : '移出池'}
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
            <p className="text-[12px]">在「实例」页将实例「移入池」，或「节点扫描」页批量添加时选择「添加进实例池」</p>
          </div>
        )}
      </div>
    </div>
  )
}