import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { RefreshCw, Play, Square, Trash2, TestTube2, Copy, Loader2, Network, Search, Server } from 'lucide-react'
import { api, type Instance } from '../lib/api'

function statusBadge(st: Instance['status']): [string, string] {
  if (st === 'Running') return ['bg-green-50 text-green-700', '运行中']
  if (st === 'Stopped') return ['bg-zinc-100 text-zinc-500', '已停止']
  if (st === 'Starting' || st === 'Stopping') return ['bg-amber-50 text-amber-700', st === 'Starting' ? '启动中' : '停止中']
  if (st && typeof st === 'object' && 'Error' in st) return ['bg-red-50 text-red-600', `错误:${(st as { Error: string }).Error}`]
  return ['bg-zinc-100 text-zinc-500', '未知']
}

export default function InstancesPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [instances, setInstances] = useState<Instance[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [search, setSearch] = useState('')
  const [searchFocus, setSearchFocus] = useState(false)
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped' | 'pool' | 'solo'>('all')
  const [refreshing, setRefreshing] = useState(false)
  // 手动刷新进度：{ done: 已检查数量, total: 实例总数 }，null = 不在刷新
  const [refreshProgress, setRefreshProgress] = useState<{ done: number; total: number } | null>(null)

  const load = useCallback(async (silent = true) => {
    try {
      setInstances(await api.listInstances())
    } catch (e) {
      if (!silent) toast(String(e), false)
    }
  }, [toast])

  // 首次加载查询一次实例状态，之后不自动轮询，由用户点击刷新按钮手动刷新
  useEffect(() => {
    void load()
  }, [load])

  // 手动刷新：按名称分批（每批并发 CHECK_BATCH）调用后端校正状态，
  // 每批返回后更新列表并累计进度，全部完成后按钮文字恢复「刷新」
  const CHECK_BATCH = 5
  const doRefresh = async () => {
    const names = instances.map((i) => i.name)
    const total = names.length
    if (total === 0) {
      await load(false)
      return
    }
    setRefreshing(true)
    setRefreshProgress({ done: 0, total })
    let done = 0
    try {
      for (let i = 0; i < names.length; i += CHECK_BATCH) {
        const batch = names.slice(i, i + CHECK_BATCH)
        const updated = await api.refreshStates(batch)
        if (updated.length > 0) {
          // 函数式合并，避免并发批次间基于旧 state 互相覆盖
          setInstances((prev) => {
            const map = new Map(prev.map((it) => [it.name, it]))
            for (const u of updated) map.set(u.name, u)
            return [...map.values()]
          })
        }
        done += updated.length
        setRefreshProgress({ done, total })
      }
    } catch (e) {
      toast(String(e), false)
    } finally {
      setRefreshing(false)
      setRefreshProgress(null)
    }
  }

  const toggle = (name: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  // 前端过滤：搜索（名称/节点/IP/端口）+ 状态/池筛选
  const filtered = instances.filter((i) => {
    const q = search.trim().toLowerCase()
    const hit =
      !q ||
      i.name.toLowerCase().includes(q) ||
      i.node.toLowerCase().includes(q) ||
      (i.ip || '').toLowerCase().includes(q) ||
      String(i.port).includes(q)
    if (!hit) return false
    if (filter === 'running' && i.status !== 'Running') return false
    if (filter === 'stopped' && i.status !== 'Stopped') return false
    if (filter === 'pool' && !i.join_gateway) return false
    if (filter === 'solo' && i.join_gateway) return false
    return true
  })

  const selectedAll = filtered.length > 0 && filtered.every((i) => selected.has(i.name))

  const toggleAll = () => {
    if (selectedAll) setSelected(new Set())
    else setSelected(new Set(filtered.map((i) => i.name)))
  }

// 忙态：optimistic —— 变化触发重渲染；key=实例名，值为该实例正在进行的操作
  const [pending, setPending] = useState<Record<string, 'start' | 'stop'>>({})
  const [batchBusy, setBatchBusy] = useState(false)

  // 标记/清除某实例的进行中操作
  const setOp = (name: string, op: 'start' | 'stop' | null) => {
    setPending((prev) => {
      const next = { ...prev }
      if (op) next[name] = op
      else delete next[name]
      return next
    })
  }

  const doStart = async (name: string) => {
    setOp(name, 'start')
    try {
      await api.startInstance(name)
      toast(`已启动实例 ${name}`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setOp(name, null)
    }
  }

  const doStop = async (name: string) => {
    setOp(name, 'stop')
    try {
      await api.stopInstance(name)
      toast(`已停止实例 ${name}`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setOp(name, null)
    }
  }

  const doRemove = async (name: string) => {
    if (!confirm(`确定删除实例 ${name}？`)) return
    try {
      await api.removeInstance(name)
      toast(`已删除实例 ${name}`)
      setSelected((prev) => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
      await load()
    } catch (e) {
      toast(String(e), false)
    }
  }

  const doTest = async (name: string) => {
    try {
      const r = await api.testInstance(name)
      if (r.ok) toast(`「${name}」测试通过：${r.message}（${r.latency_ms}ms）`)
      else toast(`「${name}」测试失败：${r.message}`, false)
    } catch (e) {
      toast(String(e), false)
    }
  }

const [joinBusy, setJoinBusy] = useState<Record<string, boolean>>({})
  const doJoin = async (name: string, join: boolean) => {
    setJoinBusy((prev) => ({ ...prev, [name]: true }))
    try {
      await api.setJoinGateway(name, join)
      toast(join ? `已将实例 ${name} 移入实例池` : `已将实例 ${name} 移出实例池（恢复独享）`)
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setJoinBusy((prev) => {
        const next = { ...prev }
        delete next[name]
        return next
      })
    }
  }

  // 批量出池：逐实例调用 setJoinGateway（Rust 无批量命令），无需确认弹窗
  const [joinBatchBusy, setJoinBatchBusy] = useState(false)
  const doBatchLeave = async () => {
    const names = [...selected].filter((n) => instances.find((i) => i.name === n)?.join_gateway)
    if (names.length === 0) {
      toast('未选中需要移出的实例')
      return
    }
    setJoinBatchBusy(true)
    try {
      let ok = 0
      let fail = 0
      for (const n of names) {
        try {
          await api.setJoinGateway(n, false)
          ok++
        } catch {
          fail++
        }
      }
      toast(`移出实例池成功 ${ok} 个${fail ? `，失败 ${fail}` : ''}`, fail === 0)
      await load()
    } finally {
      setJoinBatchBusy(false)
    }
  }

  // 批量入池：弹窗确认（提示默认启动）→ 逐个启动未运行实例并入池，实时进度
  const [joinConfirm, setJoinConfirm] = useState<string[] | null>(null)
  const [joinProgress, setJoinProgress] = useState<{ done: number; total: number } | null>(null)
  const [joinRunning, setJoinRunning] = useState(false)

  const openJoinConfirm = () => {
    const names = [...selected].filter((n) => !instances.find((i) => i.name === n)?.join_gateway)
    if (names.length === 0) {
      toast('未选中需要移入的实例')
      return
    }
    setJoinConfirm(names)
  }

  const runBatchJoin = async () => {
    if (!joinConfirm) return
    const names = joinConfirm
    const total = names.length
    setJoinRunning(true)
    setJoinProgress({ done: 0, total })
    let ok = 0
    let fail = 0
    try {
      for (const n of names) {
        try {
          const inst = instances.find((i) => i.name === n)
          // 加入池的节点默认启动：未运行的先启动，再打 join_gateway 标记
          if (inst && inst.status !== 'Running') {
            await api.startInstance(n)
          }
          await api.setJoinGateway(n, true)
          ok++
        } catch {
          fail++
        }
        setJoinProgress({ done: ok + fail, total })
      }
      toast(`已启动并入池 ${ok} 个${fail ? `，失败 ${fail}` : ''}`, fail === 0)
      await load()
    } finally {
      setJoinRunning(false)
      setJoinProgress(null)
      setJoinConfirm(null)
    }
  }

  const batch = async (kind: 'start' | 'stop' | 'delete') => {
    const names = [...selected]
    if (names.length === 0) {
      toast('请先勾选实例')
      return
    }
if (kind === 'delete' && !confirm(`确定删除选中的 ${names.length} 个实例？`)) return
    setBatchBusy(true)
    try {
      const fn =
        kind === 'start' ? api.batchStart : kind === 'stop' ? api.batchStop : api.batchDelete
      const r = await fn(names)
      toast(
        `${kind === 'start' ? '启动' : kind === 'stop' ? '停止' : '删除'}成功 ${r.success_count} 个` +
          (r.error_count ? `，失败 ${r.error_count}` : ''),
        r.error_count === 0,
      )
      if (kind === 'delete') setSelected(new Set())
      await load()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setBatchBusy(false)
    }
  }

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast(`已复制${label}`)
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="p-6 space-y-4">
      {/* 工具条：标题 + 操作按钮（原先样式一排） */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-900">实例管理</h2>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void doRefresh()}
            disabled={refreshing}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-70"
          >
            <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
            {refreshProgress ? `刷新 ${refreshProgress.done} / ${refreshProgress.total}` : '刷新'}
          </button>
          <button
            onClick={() => void batch('start')}
            disabled={selected.size === 0 || batchBusy}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-green-600 hover:bg-green-700 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {batchBusy ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />} 批量启动
          </button>
          <button
            onClick={() => void batch('stop')}
            disabled={selected.size === 0 || batchBusy}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Square size={14} /> 批量停止
          </button>
          <button
            onClick={() => openJoinConfirm()}
            disabled={selected.size === 0 || joinBatchBusy || batchBusy || joinRunning}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-teal-700 bg-teal-50 border border-teal-100 hover:bg-teal-100 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {joinRunning ? <Loader2 size={14} className="animate-spin" /> : <Network size={14} />} 批量入池
          </button>
          <button
            onClick={() => void doBatchLeave()}
            disabled={selected.size === 0 || joinBatchBusy || batchBusy || joinRunning}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-amber-700 bg-amber-50 border border-amber-100 hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {joinBatchBusy ? <Loader2 size={14} className="animate-spin" /> : <Network size={14} />} 批量出池
          </button>
          <button
            onClick={() => void batch('delete')}
            disabled={selected.size === 0}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-red-600 bg-red-50 hover:bg-red-100 disabled:opacity-40"
          >
            <Trash2 size={14} /> 批量删除
          </button>
        </div>
      </div>

      {instances.length > 0 && (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm overflow-hidden">
          <div className="px-4 py-3 border-b border-zinc-100 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Server size={15} className="text-teal-600" />
              <span className="text-[14px] font-semibold text-zinc-900">独享</span>
              <span className="text-[12px] text-zinc-400">共 {instances.length} 个</span>
            </div>
            <div className="flex items-center gap-2">
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
                  placeholder="搜索名称 / 节点 / IP"
                  className={clsx(
                    'w-full bg-transparent py-1.5 pl-8 pr-2 text-[12px] outline-none placeholder:text-zinc-300 transition-opacity',
                    searchFocus || search ? 'opacity-100' : 'opacity-0',
                  )}
                />
              </div>
              <select
                value={filter}
                onChange={(e) => setFilter(e.target.value as typeof filter)}
                className="px-2.5 py-1.5 rounded-lg border border-zinc-200 bg-white text-[12px] text-zinc-600 outline-none"
              >
                <option value="all">全部实例</option>
                <option value="running">运行中</option>
                <option value="stopped">已停止</option>
                <option value="pool">池成员</option>
                <option value="solo">独享</option>
              </select>
            </div>
          </div>
          {filtered.length > 0 ? (
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-zinc-400 border-b border-zinc-100">
                <th className="py-3 pl-4 w-8">
                  <input type="checkbox" checked={selectedAll} onChange={toggleAll} className="accent-zinc-900" />
                </th>
                <th className="py-3 pl-2">名称 / 节点 IP</th>
                <th className="py-3 pl-2">端口</th>
                <th className="py-3 pl-2">API 地址</th>
                <th className="py-3 pl-2">密钥</th>
                <th className="py-3 pl-2">状态</th>
                <th className="py-3 pl-2 pr-4 text-right">操作</th>
              </tr>
            </thead>
            <tbody>
{filtered.map((i) => {
                const isPending = pending[i.name]
                // 乐观状态：操作中直接显示启动中/停止中，覆盖真实状态徽章
                const displayStatus: Instance['status'] = isPending === 'stop' ? 'Stopping' : isPending === 'start' ? 'Starting' : i.status
                const [cls, label] = statusBadge(displayStatus)
                return (
                  <tr key={i.name} className="border-b border-zinc-50 hover:bg-zinc-50/50">
                    <td className="py-2.5 pl-4">
                      <input type="checkbox" checked={selected.has(i.name)} onChange={() => toggle(i.name)} className="accent-zinc-900" />
                    </td>
                    <td className="py-2.5 pl-2">
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
                      <button
                        onClick={() => void copyText(`http://127.0.0.1:${i.port}/v1`, 'API 地址')}
                        className="flex items-center gap-1 text-teal-700 hover:underline"
                        title="点击复制"
                      >
                        <code className="text-[12px]">127.0.0.1:{i.port}/v1</code>
                        <Copy size={11} />
                      </button>
                    </td>
                    <td className="py-2.5 pl-2">
                      <button
                        onClick={() => void copyText(i.password || '', '密钥')}
                        className="flex items-center gap-1 text-zinc-600 hover:underline"
                        title="点击复制"
                      >
                        <code className="text-[12px] text-zinc-400">{maskKey(i.password)}</code>
                        <Copy size={11} />
                      </button>
                    </td>
<td className="py-2.5 pl-2">
                      <div className="flex items-center gap-1.5">
                        <span className={clsx('inline-block px-2 py-0.5 rounded-full text-xs font-medium', cls)}>{label}</span>
                        {i.join_gateway && (
                          <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-teal-50 text-teal-700 border border-teal-100" title="已加入实例池">
                            <Network size={10} /> 池
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="py-2.5 pl-2 pr-4">
                      <div className="flex items-center justify-end gap-1.5">
{i.status === 'Running' ? (
                          <button
                            onClick={() => void doStop(i.name)}
                            disabled={!!pending[i.name]}
                            className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-zinc-700 bg-zinc-100 hover:bg-zinc-200 disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {pending[i.name] === 'stop' ? <Loader2 size={12} className="animate-spin" /> : null}
                            {pending[i.name] === 'stop' ? '停止中…' : '停止'}
                          </button>
                        ) : (
                          <button
                            onClick={() => void doStart(i.name)}
                            disabled={!!pending[i.name]}
                            className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[12px] text-white bg-green-600 hover:bg-green-700 disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {pending[i.name] === 'start' ? <Loader2 size={12} className="animate-spin" /> : null}
                            {pending[i.name] === 'start' ? '启动中…' : '启动'}
                          </button>
                        )}
                        <button onClick={() => void doTest(i.name)} className="flex items-center gap-1 px-2.5 py-1 rounded-lg text-[12px] text-teal-700 bg-teal-50 hover:bg-teal-100">
                          <TestTube2 size={12} /> 测试
                        </button>
                        <button
                          onClick={() => void doJoin(i.name, !i.join_gateway)}
                          disabled={!!joinBusy[i.name]}
                          className={clsx(
                            'flex items-center gap-1 px-2.5 py-1 rounded-lg text-[12px] disabled:cursor-not-allowed disabled:opacity-60',
                            i.join_gateway
                              ? 'text-amber-700 bg-amber-50 hover:bg-amber-100'
                              : 'text-teal-700 bg-teal-50 hover:bg-teal-100',
                          )}
                          title={i.join_gateway ? '移出实例池（恢复独享）' : '加入实例池（聚合到统一网关）'}
                        >
                          {joinBusy[i.name] ? <Loader2 size={12} className="animate-spin" /> : <Network size={12} />}
                          {joinBusy[i.name] ? '处理中…' : i.join_gateway ? '移出池' : '移入池'}
                        </button>
                        <button onClick={() => void doRemove(i.name)} className="px-2.5 py-1 rounded-lg text-[12px] text-red-600 bg-red-50 hover:bg-red-100">
                          删除
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
            <p className="text-[13px]">没有匹配「{search || filter}」的实例，试试调整搜索或筛选条件</p>
          </div>
          )}
        </div>
      )}

      {instances.length === 0 && (
        <div className="flex flex-col items-center justify-center py-24 text-zinc-400">
          <p className="text-base mb-2">暂无实例</p>
          <p className="text-[13px]">在「节点扫描」页勾选节点批量添加</p>
        </div>
      )}

      {/* 批量入池确认弹窗：提示默认启动，确认后显示启动进度 */}
      {joinConfirm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/25" onClick={() => { if (!joinRunning) setJoinConfirm(null) }}>
          <div
            className="w-[420px] bg-white rounded-2xl shadow-xl p-5 space-y-4"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-[15px] font-semibold text-zinc-900">批量入池</h3>
            {joinProgress ? (
              <div className="space-y-3">
                <p className="text-[13px] text-zinc-600">
                  正在启动并移入实例池：{joinProgress.done} / {joinProgress.total}
                </p>
                <div className="h-2 rounded-full bg-zinc-100 overflow-hidden">
                  <div
                    className="h-full rounded-full bg-teal-500 transition-all duration-200"
                    style={{ width: `${joinProgress.total > 0 ? (joinProgress.done / joinProgress.total) * 100 : 0}%` }}
                  />
                </div>
              </div>
            ) : (
              <p className="text-[13px] text-zinc-600">
                选中的 <span className="font-medium text-zinc-900">{joinConfirm.length}</span> 个实例加入池后会
                <span className="font-medium text-zinc-900">默认启动</span>，是否继续？
              </p>
            )}
            <div className="flex items-center justify-end gap-2 pt-2">
              <button
                onClick={() => setJoinConfirm(null)}
                disabled={joinRunning}
                className="px-4 py-1.5 rounded-lg text-[13px] text-zinc-600 bg-zinc-100 hover:bg-zinc-200 disabled:opacity-40"
              >
                取消
              </button>
              <button
                onClick={() => void runBatchJoin()}
                disabled={joinRunning}
                className="flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-[13px] text-white bg-teal-600 hover:bg-teal-700 disabled:opacity-60"
              >
                {joinRunning ? (
                  <>
                    <Loader2 size={14} className="animate-spin" />
                    已启动 {joinProgress?.done ?? 0}/{joinProgress?.total ?? joinConfirm.length}
                  </>
                ) : (
                  '继续'
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function maskKey(k: string) {
  if (!k) return '未设置'
  if (k.length <= 8) return k
  return `${k.slice(0, 3)}…${k.slice(-4)}`
}