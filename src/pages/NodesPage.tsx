import { useCallback, useEffect, useMemo, useState } from 'react'
import clsx from 'clsx'
import { Radar, RefreshCw, Square, Plus } from 'lucide-react'
import { api, type NodeView, type ProbeResult, type ScanProgress } from '../lib/api'

export default function NodesPage({
  toast,
}: {
  toast: (msg: string, ok?: boolean) => void
}) {
  const [nodes, setNodes] = useState<NodeView[]>([])
  const [scan, setScan] = useState<ScanProgress | null>(null)
  const [scanning, setScanning] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [addOpen, setAddOpen] = useState(false)

  const loadNodes = useCallback(async () => {
    try {
      setNodes(await api.listNodes())
    } catch (e) {
      toast(String(e), false)
    }
  }, [toast])

  // 加载节点 + 轮询扫描进度
  useEffect(() => {
    loadNodes()
    let t: number | undefined
    const poll = async () => {
      try {
        const p = await api.scanStatus()
        setScan(p)
        setScanning(p.status === 'running' || p.status === 'stopping')
      } catch {
        /* ignore */
      } finally {
        if (!t) t = window.setTimeout(poll, 800)
      }
    }
    t = window.setTimeout(poll, 500)
    return () => {
      if (t) clearTimeout(t)
    }
  }, [loadNodes])

  const resultsMap = useMemo(() => {
    const m = new Map<string, ProbeResult>()
    if (scan?.results) for (const r of scan.results) m.set(r.node, r)
    return m
  }, [scan])

  const groups = useMemo(() => {
    const g = new Map<string, NodeView[]>()
    for (const n of nodes) {
      const k = n.group || '其他'
      if (!g.has(k)) g.set(k, [])
      g.get(k)!.push(n)
    }
    return Array.from(g.entries())
  }, [nodes])

  const startScan = async () => {
    setScanning(true)
    try {
      const p = await api.scanStart({ timeout: 12 })
      setScan(p)
      toast('开始节点扫描…')
    } catch (e) {
      setScanning(false)
      toast(String(e), false)
    }
  }

  const stopScan = async () => {
    try {
      await api.scanStop()
      toast('已请求停止扫描')
    } catch (e) {
      toast(String(e), false)
    }
  }

  const okNodes = nodes.filter((n) => resultsMap.get(n.name)?.ok)

  const toggleGroup = (g: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(g)) next.delete(g)
      else next.add(g)
      return next
    })
  }

  const groupSelected = (list: NodeView[]) => list.length > 0 && list.every((n) => selected.has(n.name))

  const toggleGroupSel = (list: NodeView[]) => {
    setSelected((prev) => {
      const next = new Set(prev)
      const all = groupSelected(list)
      for (const n of list) {
        if (all) next.delete(n.name)
        else next.add(n.name)
      }
      return next
    })
  }

  const toggleNode = (name: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const selectOk = () => {
    setSelected(new Set(okNodes.map((n) => n.name)))
    toast(`已勾选 ${okNodes.length} 个可用节点`)
  }

  return (
    <div className="p-6 space-y-4">
      {/* 工具条 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-900">节点扫描</h2>
          <span className="px-2 py-0.5 rounded-full bg-zinc-100 text-zinc-500 text-xs font-medium">
            {nodes.length} 个
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => void loadNodes()}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50"
          >
            <RefreshCw size={14} /> 刷新
          </button>
          {scanning ? (
            <button
              onClick={() => void stopScan()}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-red-600 hover:bg-red-700"
            >
              <Square size={14} /> 停止扫描
            </button>
          ) : (
            <button
              onClick={() => void startScan()}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-zinc-900 hover:bg-zinc-700"
            >
              <Radar size={14} /> 一键扫描全部
            </button>
          )}
          <button
            onClick={selectOk}
            className="px-3 py-1.5 rounded-lg text-[13px] text-teal-700 bg-teal-50 hover:bg-teal-100"
          >
            全选可用
          </button>
          <button
            onClick={() => setAddOpen(true)}
            disabled={selected.size === 0 || scanning}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-green-600 hover:bg-green-700 disabled:opacity-40"
          >
            <Plus size={14} /> 添加选中为实例（{selected.size}）
          </button>
        </div>
      </div>

      {/* 进度条 */}
      {scan && (scan.total > 0 || scanning) && (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-4 space-y-2">
          <div className="flex items-center justify-between text-[12px] text-zinc-500">
            <span>
              {scanning
                ? `扫描中：${scan.current}/${scan.total}${scan.current_node ? ` · ${scan.current_node}` : ''}`
                : scan.status === 'done'
                  ? '扫描完成'
                  : scan.status === 'error'
                    ? `扫描出错：${scan.error}`
                    : '扫描已停止'}
            </span>
            <span>{scan.total ? `${Math.round((scan.current / scan.total) * 100)}%` : ''}</span>
          </div>
          <div className="h-2 bg-zinc-100 rounded-full overflow-hidden">
            <div
              className="h-full bg-zinc-900 rounded-full transition-all"
              style={{ width: scan.total ? `${(scan.current / scan.total) * 100}%` : '0%' }}
            />
          </div>
        </div>
      )}

      {/* 节点列表 */}
      {nodes.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 text-zinc-400">
          <p className="text-base mb-2">未发现节点</p>
          <p className="text-[13px]">请先在「设置」页配置 Clash 外部控制地址</p>
        </div>
      ) : (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm divide-y divide-zinc-100">
          {groups.map(([g, list]) => {
            const isCollapsed = collapsed.has(g)
            const all = groupSelected(list)
            const checkedCount = list.filter((n) => selected.has(n.name)).length
            return (
              <div key={g}>
                <div className="flex items-center gap-3 px-4 py-2.5 bg-zinc-50/50">
                  <input
                    type="checkbox"
                    checked={all}
                    onChange={() => toggleGroupSel(list)}
                    className="accent-zinc-900"
                  />
                  <button
                    onClick={() => toggleGroup(g)}
                    className="flex-1 text-left text-[13px] font-semibold text-zinc-700"
                  >
                    {g} <span className="text-zinc-400 font-normal">({list.length}，已选 {checkedCount})</span>
                  </button>
                  <span className="text-[11px] text-zinc-400">{isCollapsed ? '展开' : '收起'}</span>
                </div>
                {!isCollapsed && (
                  <div className="divide-y divide-zinc-50">
                    {list.map((n) => {
                      const r = resultsMap.get(n.name)
                      return (
                        <div key={n.name} className="flex items-center gap-2 px-4 py-2.5 pl-9">
                          <input
                            type="checkbox"
                            checked={selected.has(n.name)}
                            onChange={() => toggleNode(n.name)}
                            className="accent-zinc-900"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="text-[13px] text-zinc-800 truncate">{n.name}</span>
                              <span className="text-[11px] text-zinc-400">{n.node_type}</span>
                            </div>
                            <div className="text-[11px] text-zinc-400">
                              {n.server}:{n.port} · {n.group}
                            </div>
                          </div>
                          <span className={clsx('text-xs', n.has_cred ? 'text-green-600' : 'text-gray-300')}>
                            {n.has_cred ? '✓凭据' : '✗无凭据'}
                          </span>
                          {r && !r.ok && (
                            <span className="text-[11px] text-zinc-400 max-w-[160px] truncate" title={r.message}>
                              {r.message}
                            </span>
                          )}
                          {badgeNode(r)}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* 添加实例 Modal */}
      <AddModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        nodes={nodes.filter((n) => selected.has(n.name))}
        onAdded={() => {
          toast('批量添加完成')
          setAddOpen(false)
          setSelected(new Set())
          void loadNodes()
        }}
      />
    </div>
  )
}

function badgeNode(r?: ProbeResult) {
  if (!r) return null
  const map: Record<string, [string, string]> = {
    ok: ['bg-green-50 text-green-700', 'ok'],
    upstream: ['bg-amber-50 text-amber-700', 'upstream'],
    config: ['bg-red-50 text-red-600', 'config'],
    socks: ['bg-red-50 text-red-600', 'socks'],
    tls: ['bg-red-50 text-red-600', 'tls'],
    timeout: ['bg-red-50 text-red-600', 'timeout'],
    other: ['bg-zinc-100 text-zinc-500', 'other'],
  }
  const [cl, label] = map[r.category] || ['bg-zinc-100 text-zinc-500', r.category]
  return (
    <span className={clsx('inline-block shrink-0 px-2 py-0.5 rounded-full text-[11px] font-medium', cl)}>
      {label}
      {r.latency_ms > 0 ? ` ${r.latency_ms}ms` : ''}
    </span>
  )
}

function AddModal({
  open,
  onClose,
  nodes,
  onAdded,
}: {
  open: boolean
  onClose: () => void
  nodes: NodeView[]
  onAdded: () => void
}) {
  const [ports, setPorts] = useState<Record<string, string>>({})
  const [portState, setPortState] = useState<Record<string, { ok: boolean; reason: string }>>({})
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (open) {
      const init: Record<string, string> = {}
      nodes.forEach((n, i) => {
        init[n.name] = String(18100 + i)
      })
      setPorts(init)
      setPortState({})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  if (!open) return null

  const onPortChange = async (name: string, value: string) => {
    setPorts((prev) => ({ ...prev, [name]: value }))
    const n = Number(value)
    if (!n || n < 1024) {
      setPortState((prev) => ({ ...prev, [name]: { ok: false, reason: '端口需 >= 1024' } }))
      return
    }
    try {
      const r = await api.portCheck(n)
      setPortState((prev) => ({ ...prev, [name]: { ok: r.available, reason: r.reason } }))
    } catch (e) {
      setPortState((prev) => ({ ...prev, [name]: { ok: false, reason: String(e) } }))
    }
  }

  const submit = async () => {
    const items = nodes.map((n) => ({ node: n.name, port: Number(ports[n.name] || 18100) }))
    if (items.some((it) => !it.port || it.port < 1024)) {
      alert('存在无效端口（需 >= 1024）')
      return
    }
    if (items.some((it) => portState[it.node] && !portState[it.node]?.ok)) {
      alert('存在被占用的端口，请修改')
      return
    }
    setLoading(true)
    try {
      const r = await api.batchAdd(items, 18100, true)
      onAdded()
      alert(`成功添加 ${r.added_count} 个` + (r.error_count ? `，失败 ${r.error_count}` : ''))
    } catch (e) {
      alert(String(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/25" onClick={onClose}>
      <div
        className="w-[480px] bg-white rounded-2xl shadow-xl p-5 space-y-3"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-[15px] font-semibold text-zinc-900">添加选中为实例</h3>
        <div className="max-h-72 overflow-y-auto space-y-2">
          {nodes.map((n) => {
            const st = portState[n.name]
            return (
              <div key={n.name} className="flex items-center gap-2">
                <span className="flex-1 text-[13px] text-zinc-700 truncate" title={n.name}>
                  {n.name}
                </span>
                <span className="text-[11px] text-zinc-400">端口</span>
                <input
                  className="w-28 px-2 py-1 rounded-lg text-[13px]"
                  value={ports[n.name] || ''}
                  onChange={(e) => void onPortChange(n.name, e.target.value)}
                />
                {st && (
                  <span className={clsx('text-[11px] w-24', st.ok ? 'text-green-600' : 'text-red-500')}>
                    {st.ok ? '✓ 可用' : `✗ ${st.reason}`}
                  </span>
                )}
              </div>
            )
          })}
        </div>
        <div className="flex items-center justify-end gap-2 pt-2">
          <button
            onClick={onClose}
            className="px-4 py-1.5 rounded-lg text-[13px] text-zinc-600 bg-zinc-100 hover:bg-zinc-200"
          >
            取消
          </button>
          <button
            onClick={() => void submit()}
            disabled={loading}
            className="px-4 py-1.5 rounded-lg text-[13px] text-white bg-zinc-900 hover:bg-zinc-700 disabled:opacity-50"
          >
            {loading ? '添加中…' : '确定'}
          </button>
        </div>
      </div>
    </div>
  )
}