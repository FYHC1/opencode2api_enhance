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
  const [instanceNodes, setInstanceNodes] = useState<Set<string>>(new Set())
  const [refreshing, setRefreshing] = useState(false)
  const [adding, setAdding] = useState(false)

  const loadNodes = useCallback(async () => {
    try {
      const [ns, insts] = await Promise.all([api.listNodes(), api.listInstances()])
      setNodes(ns)
      setInstanceNodes(new Set(insts.map((i) => i.node)))
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

  const doRefresh = async () => {
    setRefreshing(true)
    await loadNodes()
    setRefreshing(false)
  }

  // 只扫描选中的节点
  const startScan = async () => {
    const names = [...selected]
    if (names.length === 0) {
      toast('请先勾选要扫描的节点', false)
      return
    }
    setScanning(true)
    try {
      const p = await api.scanStart({ nodes: names, timeout: 12 })
      setScan(p)
      toast(`开始扫描 ${names.length} 个节点…`)
    } catch (e) {
      setScanning(false)
      toast(String(e), false)
    }
  }

  const stopScan = async () => {
    try {
      await api.scanStop()
      toast('已停止扫描')
    } catch (e) {
      toast(String(e), false)
    } finally {
      setScanning(false)
      setScan(null) // 停止后关闭进度条
    }
  }

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

  const scanBtnDisabled = selected.size === 0 || scanning

  // 一键添加选中为实例：端口后端自动分配、密钥随机生成（sk- 开头），无需用户填写
  const doAddSelected = async () => {
    const items = [...selected].map((node) => ({ node }))
    if (items.length === 0) {
      toast('请先勾选要添加的节点', false)
      return
    }
    setAdding(true)
    try {
      const r = await api.batchAdd(items, 18100, true)
      toast(
        `成功添加 ${r.added_count} 个实例` + (r.error_count ? `，失败 ${r.error_count}` : ''),
        r.error_count === 0,
      )
      setSelected(new Set())
      await loadNodes()
    } catch (e) {
      toast(String(e), false)
    } finally {
      setAdding(false)
    }
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
            onClick={() => void doRefresh()}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-zinc-700 bg-white border border-zinc-200 hover:bg-zinc-50"
          >
            <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
            {refreshing ? '刷新中…' : '刷新'}
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
              disabled={scanBtnDisabled}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-zinc-900 hover:bg-zinc-700 disabled:opacity-40 disabled:cursor-not-allowed"
              title={selected.size === 0 ? '请先勾选节点' : ''}
            >
              <Radar size={14} /> 扫描选中节点（{selected.size}）
            </button>
          )}
          <button
            onClick={() => void doAddSelected()}
            disabled={selected.size === 0 || scanning || adding}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white bg-green-600 hover:bg-green-700 disabled:opacity-40"
          >
            <Plus size={14} className={adding ? 'animate-spin' : ''} />
            {adding ? '添加中…' : `添加选中为实例（${selected.size}）`}
          </button>
        </div>
      </div>

      {/* 进度条：仅扫描中显示，停止即隐藏 */}
      {scanning && scan && (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-4 space-y-2">
          <div className="flex items-center justify-between text-[12px] text-zinc-500">
            <span>
              {scan.current_node
                ? `扫描中：${scan.current}/${scan.total} · ${scan.current_node}`
                : `扫描中：${scan.current}/${scan.total}`}
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
                  <input type="checkbox" checked={all} onChange={() => toggleGroupSel(list)} className="accent-zinc-900" />
                  <button onClick={() => toggleGroup(g)} className="flex-1 text-left text-[13px] font-semibold text-zinc-700">
                    {g} <span className="text-zinc-400 font-normal">（{list.length}，已选 {checkedCount}）</span>
                  </button>
                  <span className="text-[11px] text-zinc-400">{isCollapsed ? '展开' : '收起'}</span>
                </div>
                {!isCollapsed && (
                  <div className="divide-y divide-zinc-50">
                    {list.map((n) => {
                      const r = resultsMap.get(n.name)
                      return (
                        <div key={n.name} className={clsx('flex items-center gap-2 px-4 py-2.5 pl-9', instanceNodes.has(n.name) && 'bg-zinc-50/70')}>
                          <input type="checkbox" checked={selected.has(n.name)} onChange={() => toggleNode(n.name)} disabled={instanceNodes.has(n.name)} className="accent-zinc-900 disabled:opacity-30" />
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="text-[13px] text-zinc-800 truncate">{n.name}</span>
                              {instanceNodes.has(n.name) && (
                                <span className="inline-block px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-green-50 text-green-600 border border-green-100">✓ 已添加实例</span>
                              )}
                              <span className="text-[11px] text-zinc-400">{n.node_type}</span>
                              <span className="text-[11px] text-zinc-300 font-mono">{n.server}:{n.port}</span>
                            </div>
                            <div className="text-[11px] text-zinc-400">{n.group}</div>
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
