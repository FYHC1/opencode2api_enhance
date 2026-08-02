import { useCallback, useEffect, useState } from 'react'
import clsx from 'clsx'
import { Plus, RefreshCw, Play, Square, Trash2, TestTube2, Copy, Loader2 } from 'lucide-react'
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
  const [addOpen, setAddOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async (silent = true) => {
    try {
      setInstances(await api.listInstances())
    } catch (e) {
      if (!silent) toast(String(e), false)
    }
  }, [toast])

  // 自动轮询（静默）
  useEffect(() => {
    load()
    const t = setInterval(() => void load(true), 3000)
    return () => clearInterval(t)
  }, [load])

  // 手动刷新（带 loading）
  const doRefresh = async () => {
    setRefreshing(true)
    await load(false)
    setRefreshing(false)
  }

  const toggle = (name: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const selectedAll = instances.length > 0 && instances.every((i) => selected.has(i.name))

  const toggleAll = () => {
    if (selectedAll) setSelected(new Set())
    else setSelected(new Set(instances.map((i) => i.name)))
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
      {/* 工具条 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-900">实例管理</h2>
          <span className="px-2 py-0.5 rounded-full bg-zinc-100 text-zinc-500 text-xs font-medium">
            {instances.length} 个
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void doRefresh()}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50"
          >
            <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
            {refreshing ? '刷新中…' : '刷新'}
          </button>
          <button
            onClick={() => setAddOpen(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-zinc-900 hover:bg-zinc-700"
          >
            <Plus size={14} /> 添加实例
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
{instances.map((i) => {
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
                      <span className={clsx('inline-block px-2 py-0.5 rounded-full text-xs font-medium', cls)}>{label}</span>
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
        </div>
      )}

      {instances.length === 0 && (
        <div className="flex flex-col items-center justify-center py-24 text-zinc-400">
          <p className="text-base mb-2">暂无实例</p>
          <p className="text-[13px]">在「节点扫描」页勾选节点批量添加，或点击「添加实例」</p>
        </div>
      )}

      <AddModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onAdded={(name) => {
          toast(`已添加实例 ${name}`)
          setAddOpen(false)
          void load()
        }}
      />
    </div>
  )
}

function maskKey(k: string) {
  if (!k) return '未设置'
  if (k.length <= 8) return k
  return `${k.slice(0, 3)}…${k.slice(-4)}`
}

function AddModal({
  open,
  onClose,
  onAdded,
}: {
  open: boolean
  onClose: () => void
  onAdded: (name: string) => void
}) {
  const [name, setName] = useState('')
  const [node, setNode] = useState('')
  const [port, setPort] = useState('')
  const [loading, setLoading] = useState(false)

  if (!open) return null

  const submit = async () => {
    const p = Number(port)
    if (!node.trim()) {
      alert('请填写节点名称')
      return
    }
    if (!p || p < 1024) {
      alert('端口需 >= 1024')
      return
    }
    setLoading(true)
    try {
      const inst = await api.addInstance(name.trim(), p, node.trim(), '')
      onAdded(inst.name)
    } catch (e) {
      alert(String(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/25" onClick={onClose}>
      <div
        className="w-[420px] bg-white rounded-2xl shadow-xl p-5 space-y-4"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-[15px] font-semibold text-zinc-900">添加实例</h3>
        <label className="block space-y-1">
          <span className="text-[12px] text-zinc-500">名称（留空自动命名）</span>
          <input className="w-full px-3 py-2 rounded-lg text-[13px]" value={name} onChange={(e) => setName(e.target.value)} placeholder="留空则自动命名" />
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] text-zinc-500">节点名称</span>
          <input className="w-full px-3 py-2 rounded-lg text-[13px]" value={node} onChange={(e) => setNode(e.target.value)} placeholder="如 CF移动优选1" />
        </label>
        <label className="block space-y-1">
          <span className="text-[12px] text-zinc-500">端口</span>
          <input className="w-full px-3 py-2 rounded-lg text-[13px]" value={port} onChange={(e) => setPort(e.target.value)} placeholder="如 18100" />
        </label>

        <div className="flex items-center justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-4 py-1.5 rounded-lg text-[13px] text-zinc-600 bg-zinc-100 hover:bg-zinc-200">
            取消
          </button>
          <button onClick={() => void submit()} disabled={loading} className="px-4 py-1.5 rounded-lg text-[13px] text-white bg-zinc-900 hover:bg-zinc-700 disabled:opacity-50">
            {loading ? '添加中…' : '确定'}
          </button>
        </div>
      </div>
    </div>
  )
}