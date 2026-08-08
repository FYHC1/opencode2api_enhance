import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import clsx from 'clsx'
import { Radar, RefreshCw, Square } from 'lucide-react'
import { api, type NodeView, type ProbeResult, type ScanProgress } from '../lib/api'
import ResultModal from '../components/ResultModal'

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
  // 已添加实例的节点：node → 是否入池（join_gateway），用于徽章区分「实例池/独享」
  const [instanceNodes, setInstanceNodes] = useState<Map<string, boolean>>(new Map())
  const [refreshing, setRefreshing] = useState(false)
  // 结果弹窗：扫描完成（running→done）时打开
  const [showResult, setShowResult] = useState(false)
  // 入池/独享动作进行中（弹窗按钮禁用）
  const [acting, setActing] = useState(false)
  // 追踪上一次扫描状态：仅在「本次扫描 running → done」时弹出结果弹窗
  const prevScanStatusRef = useRef<string | null>(null)

  const loadNodes = useCallback(async () => {
    try {
      const [ns, insts] = await Promise.all([api.listNodes(), api.listInstances()])
      setNodes(ns)
      setInstanceNodes(new Map(insts.map((i) => [i.node, i.join_gateway])))
    } catch (e) {
      toast(String(e), false)
    }
  }, [toast])

  // 加载节点 + 轮询扫描进度
// 加载节点 + 轮询扫描进度（注意：这里是链式 setTimeout，卸载时清理）
  useEffect(() => {
    loadNodes()
    let alive = true
    const poll = async () => {
      if (!alive) return
      try {
        const p = await api.scanStatus()
        if (!alive) return
        const prev = prevScanStatusRef.current
        prevScanStatusRef.current = p.status
        setScan(p)
        setScanning(p.status === 'running' || p.status === 'stopping')
        // 扫描刚完成（running → done）：弹出结果弹窗
        if (p.status === 'done' && prev === 'running') setShowResult(true)
      } catch {
        /* ignore */
      }
      // 无论成功失败都继续轮询
      if (alive) setTimeout(poll, 800)
    }
    setTimeout(poll, 500)
    return () => {
      alive = false
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

  // 组内「可选」节点 = 未实例化（已实例化节点的复选框禁用，不参与组全选）
  const selectable = (list: NodeView[]) => list.filter((n) => !instanceNodes.has(n.name))

  // 组头勾选态：按可选节点计算（组内全是已实例化时视为未全选）
  const groupSelected = (list: NodeView[]) => {
    const sel = selectable(list)
    return sel.length > 0 && sel.every((n) => selected.has(n.name))
  }

  const toggleGroupSel = (list: NodeView[]) => {
    setSelected((prev) => {
      const next = new Set(prev)
      const sel = selectable(list)
      const all = sel.length > 0 && sel.every((n) => selected.has(n.name))
      for (const n of sel) {
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

  // 扫描结果中的可用节点（去重：剔除已添加为实例的节点）
  const okNodes = useMemo(() => {
    if (!scan || scan.status !== 'done') return []
    return (scan.results ?? [])
      .filter((r) => r.ok && !instanceNodes.has(r.node))
      .map((r) => r.node)
  }, [scan, instanceNodes])

  // 通用批量添加：tag === 'pool' 时额外标记入池（join_gateway）
  const doCommit = async (tag: 'pool' | 'solo') => {
    if (okNodes.length === 0) return
    setActing(true)
    try {
      const items = okNodes.map((node) => ({ node }))
      const r = await api.batchAdd(items, undefined, true)
      if (tag === 'pool' && r.added.length > 0) {
        // 进池：只打 join_gateway 标记（不自动启动，启停由实例池页控制）
        for (const a of r.added) {
          try {
            await api.setJoinGateway(a.name, true)
          } catch {
            /* 单条失败不阻断整体 */
          }
        }
      }
      toast(
        `成功添加 ${r.added_count} 个实例` +
          (tag === 'pool' ? '（已入池）' : '（独享）') +
          (r.error_count ? `，跳过/失败 ${r.error_count}` : ''),
        r.error_count === 0,
      )
      // 入池/独享完成后清空勾选态：已添加的节点复选框会变 disabled，
      // 不清空会导致选中态残留且无法手动取消（disabled 不响应点击）
      setSelected(new Set())
      await loadNodes()
      setShowResult(false)
    } catch (e) {
      toast(String(e), false)
    } finally {
      setActing(false)
    }
  }


  return (
    <>
      <div className="p-6 space-y-4">
      {/* 工具条 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
<h2 className="text-lg font-semibold text-zinc-900">节点池</h2>
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
              className={clsx(
                'flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] text-white transition-colors',
                scanBtnDisabled
                  ? 'bg-zinc-200 text-zinc-500 cursor-not-allowed'
                  : 'bg-green-600 hover:bg-green-700 shadow-sm',
              )}
              title={selected.size === 0 ? '请先勾选节点' : '扫描选中节点'}
            >
              <Radar size={14} /> 扫描选中节点（{selected.size}）
            </button>
          )}
        </div>
      </div>

{/* 扫描进度条：扫描中实时显示，完成后短暂保留结果 */}
      {scan && (scan.status === 'running' || scan.status === 'stopping' || scan.status === 'done') && (
        <div className="bg-white rounded-2xl border border-zinc-200 shadow-sm p-4 space-y-2">
          <div className="flex items-center justify-between text-[12px] text-zinc-500">
            <span>
              {scan.status === 'running' || scan.status === 'stopping'
                ? scan.current_node
                  ? `扫描中：${scan.current}/${scan.total} · ${scan.current_node}`
                  : `扫描中：${scan.current}/${scan.total}`
                : `扫描完成：${scan.results.filter((r) => r.ok).length}/${scan.total} 个可用`}
            </span>
            <span>{scan.total ? `${Math.round((scan.current / scan.total) * 100)}%` : ''}</span>
          </div>
          <div className="h-2 bg-zinc-100 rounded-full overflow-hidden">
            <div
              className="h-full bg-zinc-900 rounded-full transition-all"
              style={{ width: scan.total ? `${Math.min((scan.current / scan.total) * 100, 100)}%` : '0%' }}
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
                    disabled={selectable(list).length === 0}
                    className="accent-teal-600 disabled:opacity-30"
                    title={selectable(list).length === 0 ? '该组节点均已添加实例' : ''}
                  />
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
                        <div
                          key={n.name}
                          className={clsx(
                            'flex items-center gap-2 px-4 py-2.5 pl-9 transition-colors',
                            // 选中：左侧竖条（inset shadow 不占布局）+ 名称加粗，不做整行大色块（节点挨着时全选会连成一片）
                            selected.has(n.name) && 'shadow-[inset_3px_0_0_0_#0d9488]',
                            // 未选中：hover 浅灰；已实例化（禁选）静息灰底
                            !selected.has(n.name) && instanceNodes.has(n.name) && 'bg-zinc-50',
                            !selected.has(n.name) && !instanceNodes.has(n.name) && 'hover:bg-zinc-50',
                          )}
                        >
                          <input type="checkbox" checked={selected.has(n.name)} onChange={() => toggleNode(n.name)} disabled={instanceNodes.has(n.name)} className="accent-teal-600 disabled:opacity-30" />
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className={clsx('text-[13px] truncate', selected.has(n.name) ? 'font-semibold text-teal-800' : 'text-zinc-800')}>{n.name}</span>
                              {instanceNodes.has(n.name) && (
                                <span
                                  className={clsx(
                                    'inline-block px-1.5 py-0.5 rounded-full text-[10px] font-medium border',
                                    instanceNodes.get(n.name)
                                      ? 'bg-teal-50 text-teal-700 border-teal-100'
                                      : 'bg-blue-50 text-blue-700 border-blue-100',
                                  )}
                                >
                                  {instanceNodes.get(n.name) ? '✓ 已添加到实例池' : '✓ 已添加为独享'}
                                </span>
                              )}
                              <span className="text-[11px] text-zinc-400">{n.node_type}</span>
                              <span className="text-[11px] text-zinc-300 font-mono">{n.server}:{n.port}</span>
                            </div>
                            <div className="text-[11px] text-zinc-400">{n.group}</div>
                          </div>
                          {!n.has_cred && (
                            <span className="text-[11px] text-gray-400" title="该节点缺少连接凭据，扫描时会被跳过">
                              ✗无凭据
                            </span>
                          )}
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

      {showResult && (
        <ResultModal
          okCount={okNodes.length}
          total={scan?.total ?? 0}
          busy={acting}
          onClose={() => setShowResult(false)}
          onPool={() => void doCommit('pool')}
          onSolo={() => void doCommit('solo')}
        />
      )}
    </>
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
