import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { RefreshCw, Play, Square, Trash2, TestTube2, Copy, Loader2, Search, Server } from 'lucide-react'
import { api, type Instance, type TestResult } from '../lib/api'

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
  // 测试结果（行内徽章正反馈）：name → TestResult
  const [testResults, setTestResults] = useState<Record<string, TestResult>>({})
  const [search, setSearch] = useState('')
  const [searchFocus, setSearchFocus] = useState(false)
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all')
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
    // 本页只管理独享实例（池成员在实例池页管理），刷新只校正独享
    const names = instances.filter((i) => !i.join_gateway).map((i) => i.name)
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

  // 独享实例 = 未入池（页面边界：本页只显示独享，池成员在实例池页）
  const soloInstances = instances.filter((i) => !i.join_gateway)

  // 前端过滤：搜索（名称/节点/IP/端口）+ 状态筛选
  const filtered = soloInstances.filter((i) => {
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
    if (!confirm(`确定释放实例 ${name}？将关闭实例并释放节点。`)) return
    try {
      await api.removeInstance(name)
      toast(`已释放实例 ${name}`)
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
      setTestResults((prev) => ({ ...prev, [name]: r }))
      if (r.ok) toast(`「${name}」测试通过：${r.message}（${r.latency_ms}ms）`)
      // 失败 toast 与表格徽章一致：直接显示 r.message（已含完整文案，避免重复实例名）
      else toast(r.message || '测试失败', false)
    } catch (e) {
      toast(String(e), false)
    }
  }




  const batch = async (kind: 'start' | 'stop' | 'delete') => {
    const names = [...selected]
    if (names.length === 0) {
      toast('请先勾选实例')
      return
    }
if (kind === 'delete' && !confirm(`确定释放选中的 ${names.length} 个实例？将自动关闭并释放节点。`)) return
    setBatchBusy(true)
    try {
      const fn =
        kind === 'start' ? api.batchStart : kind === 'stop' ? api.batchStop : api.batchDelete
      const r = await fn(names)
      toast(
        `${kind === 'start' ? '启动' : kind === 'stop' ? '停止' : '释放'}成功 ${r.success_count} 个` +
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

  // 一键测试：对勾选的实例并行探测连通性，结果逐行回填徽章，汇总提示
  const [testBusy, setTestBusy] = useState(false)
  const doBatchTest = async () => {
    const names = [...selected]
    if (names.length === 0) {
      toast('请先勾选实例')
      return
    }
    setTestBusy(true)
    try {
      const results = await Promise.allSettled(names.map((n) => api.testInstance(n)))
      let ok = 0
      let fail = 0
      const updated: Record<string, TestResult> = {}
      names.forEach((n, i) => {
        const r = results[i]!
        if (r.status === 'fulfilled' && r.value.ok) {
          ok++
          updated[n] = r.value
        } else {
          fail++
          updated[n] = {
            name: n,
            port: 0,
            ok: false,
            status_code: null,
            model_count: null,
            message: r.status === 'fulfilled' ? r.value.message : String(r.reason),
            latency_ms: 0,
          }
        }
      })
      setTestResults((prev) => ({ ...prev, ...updated }))
      toast(`测试完成：成功 ${ok} 个${fail ? `，失败 ${fail}` : ''}`, fail === 0)
      await load()
    } finally {
      setTestBusy(false)
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
      {/* 工具条：标题 + 数量小字，右侧仅刷新 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-900">独享管理</h2>
          <span className="text-[12px] text-zinc-400">{soloInstances.length} 个</span>
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
        </div>
      </div>

      {soloInstances.length > 0 && (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm overflow-hidden">
          <div className="px-4 py-3 border-b border-zinc-100 flex items-center justify-between">
            <div className="flex items-center gap-2">
<Server size={15} className="text-teal-600" />
              <span className="text-[14px] font-semibold text-zinc-900">独享</span>
            </div>

            <div className="flex items-center gap-2">
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
                onClick={() => void doBatchTest()}
                disabled={selected.size === 0 || testBusy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-teal-700 bg-teal-50 border border-teal-100 hover:bg-teal-100 disabled:cursor-not-allowed disabled:opacity-40"
              >
                {testBusy ? <Loader2 size={14} className="animate-spin" /> : <TestTube2 size={14} />} 一键测试
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
                        onClick={() => void copyText(`http://127.0.0.1:${i.port}/v1\n${i.password || ''}`, 'API 地址与密钥')}
                        className="flex items-center gap-1 text-zinc-600 hover:underline"
                        title="点击复制 API 地址与密钥"
                      >
                        <code className="text-[12px] text-zinc-400">{maskKey(i.password)}</code>
                        <Copy size={11} />
                      </button>
                    </td>
<td className="py-2.5 pl-2">
                      <div className="flex flex-col items-start gap-1">
                        <div className="flex items-center gap-1.5">
                          <span className={clsx('inline-block px-2 py-0.5 rounded-full text-xs font-medium', cls)}>{label}</span>
                        </div>
                        {testBadge(testResults[i.name])}
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
<button onClick={() => void doRemove(i.name)} className="flex items-center gap-1 px-2.5 py-1 rounded-lg text-[12px] text-red-600 bg-red-50 hover:bg-red-100">
                          <Trash2 size={12} /> 释放
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

      {soloInstances.length === 0 && (
        <div className="flex flex-col items-center justify-center py-24 text-zinc-400">
          <p className="text-base mb-2">暂无独享实例</p>
<p className="text-[13px]">在「节点池」页勾选节点，以「独享」方式批量添加；池成员见「实例池」页</p>
        </div>
      )}

    </div>
  )
}

/** 测试结果徽章：✓ 通过+延迟+详情 / ✗ 失败+原因（无结果返回 null 不占位） */
function testBadge(r?: TestResult) {
  if (!r) return null
  if (r.ok) {
    return (
      <span
        className="inline-block max-w-[240px] px-2 py-0.5 rounded-full text-[11px] font-medium bg-green-50 text-green-700 truncate"
        title={r.message}
      >
        ✓ 通过 {r.latency_ms}ms{r.message ? ` · ${r.message}` : ''}
      </span>
    )
  }
  return (
    <span
      className="inline-block max-w-[240px] px-2 py-0.5 rounded-full text-[11px] font-medium bg-red-50 text-red-600 truncate"
      title={r.message || '测试失败'}
    >
      ✗ {r.message || '失败'}
    </span>
  )
}



function maskKey(k: string) {
  if (!k) return '未设置'
  if (k.length <= 8) return k
  return `${k.slice(0, 3)}…${k.slice(-4)}`
}