import { useEffect, useState } from 'react'
import clsx from 'clsx'
import { Loader2, Search, Trash2, LogOut } from 'lucide-react'
import { api, type OrphanProcess } from '../lib/api'
import type { ConfigView, BinariesInfo } from '../lib/api'

export default function SettingsPage({
  toast,
  onRequestExit,
}: {
  toast: (msg: string, ok?: boolean) => void
  onRequestExit?: () => void
}) {
  const [config, setConfig] = useState<ConfigView | null>(null)
  const [autostart, setAutostart] = useState<boolean>(false)
  const [binariesInfo, setBinariesInfo] = useState<BinariesInfo | null>(null)

  // Clash 外部控制表单
  const [clashUrl, setClashUrl] = useState('')
  const [clashToken, setClashToken] = useState('')

  // 网关超时切换区间表单
  const [timeoutForm, setTimeoutForm] = useState({
    timeout_ttft_min_ms: 10000,
    timeout_ttft_max_ms: 10000,
    timeout_silence_min_ms: 5000,
    timeout_silence_max_ms: 5000,
    failover_probe_min: 2,
    failover_probe_max: 3,
    call_log_max: 5000,
  })
  // 节点前缀展示开关（默认关闭）
  const [showNodePrefix, setShowNodePrefix] = useState(false)

  // 订阅自动拉取（main 功能 M1）
  const [subscribeUrl, setSubscribeUrl] = useState('')
  const [subscribeInterval, setSubscribeInterval] = useState(0)
  // 一键拉取目标：独享 / 进池（main 功能 M1）
  const [subscribeTarget, setSubscribeTarget] = useState<'solo' | 'pool'>('solo')
  const [subscribeBusy, setSubscribeBusy] = useState(false)
  // 统一网关密钥（main 功能 M6）
  const [gatewayKey, setGatewayKey] = useState('')
  // 健康巡检（main 功能 M2）
  const [healthInterval, setHealthInterval] = useState(0)
  const [healthThreshold, setHealthThreshold] = useState(0)
  // 实例池性能模式（P1/P2）：探活 + 质量加权路由 + 熔断 + 竞速
  const [poolForm, setPoolForm] = useState({
    pool_probe_interval_sec: 45,
    pool_quality_window_min: 10,
    pool_breaker_threshold: 3,
    pool_halfopen_interval_sec: 60,
    pool_race_copies: 2,
    scan_concurrency: 8,
    batch_concurrency: 4,
    test_concurrency: 4,
    pool_probe_concurrency: 4,
  })
  const [poolProbeEnabled, setPoolProbeEnabled] = useState(true)
  const [perfMode, setPerfMode] = useState(true)
  // 残留进程清理（孤儿实例 / 探针残留）
  const [orphans, setOrphans] = useState<OrphanProcess[]>([])
  const [orphanBusy, setOrphanBusy] = useState(false)
  const [killBusy, setKillBusy] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())


  useEffect(() => {
    const loadData = async () => {
      try {
        const [cfg, as, bin] = await Promise.all([
          api.configGet(),
          api.autostartGet(),
          api.getBinariesInfo(),
        ])
        setConfig(cfg)
        setAutostart(as)
        setBinariesInfo(bin)
        setClashUrl(cfg.clash_external_url)
        setTimeoutForm({
          timeout_ttft_min_ms: cfg.timeout_ttft_min_ms,
          timeout_ttft_max_ms: cfg.timeout_ttft_max_ms,
          timeout_silence_min_ms: cfg.timeout_silence_min_ms,
          timeout_silence_max_ms: cfg.timeout_silence_max_ms,
          failover_probe_min: cfg.failover_probe_min,
          failover_probe_max: cfg.failover_probe_max,
          call_log_max: cfg.call_log_max,
        })
        setShowNodePrefix(cfg.show_node_prefix)
        setSubscribeUrl(cfg.subscribe_url)
        setSubscribeInterval(cfg.subscribe_interval_min)
        setHealthInterval(cfg.health_check_interval_sec)
        setHealthThreshold(cfg.health_restart_threshold)
        setPoolForm({
          pool_probe_interval_sec: cfg.pool_probe_interval_sec,
          pool_quality_window_min: cfg.pool_quality_window_min,
          pool_breaker_threshold: cfg.pool_breaker_threshold,
          pool_halfopen_interval_sec: cfg.pool_halfopen_interval_sec,
          pool_race_copies: cfg.pool_race_copies,
          scan_concurrency: cfg.scan_concurrency,
          batch_concurrency: cfg.batch_concurrency,
          test_concurrency: cfg.test_concurrency,
          pool_probe_concurrency: cfg.pool_probe_concurrency,
        })
        setPoolProbeEnabled(cfg.pool_probe_enabled)
        setPerfMode(cfg.pool_performance_mode)
      } catch (e) {
        console.error('加载设置失败', e)
        toast('加载设置失败', false)
      }
    }
    loadData()
  }, [toast])

  const handleSaveClash = async () => {
    try {
      await api.configSet('clash_external_url', clashUrl)
      if (clashToken.trim()) {
        await api.configSet('clash_auth_token', clashToken)
      }
      toast('已保存', true)
      // 重新加载配置以更新 has_clash_token 状态
      const cfg = await api.configGet()
      setConfig(cfg)
      setClashToken('')
    } catch (e) {
      console.error('保存失败', e)
      toast('保存失败', false)
    }
  }

  const handleAutostartChange = async (enabled: boolean) => {
    try {
      await api.autostartSet(enabled)
      setAutostart(enabled)
      toast(enabled ? '已启用开机自启' : '已禁用开机自启', true)
    } catch (e) {
      console.error('设置开机自启失败', e)
      toast('设置失败', false)
    }
  }

  // 校验区间：min <= max，且为正数
  const validateRange = (min: number, max: number): boolean => {
    return min > 0 && max >= min
  }

  const handleSaveTimeout = async () => {
    const f = timeoutForm
    if (!validateRange(f.timeout_ttft_min_ms, f.timeout_ttft_max_ms) ||
        !validateRange(f.timeout_silence_min_ms, f.timeout_silence_max_ms) ||
        !validateRange(f.failover_probe_min, f.failover_probe_max)) {
      toast('区间不合法：最小值需 >0 且 最小值 ≤ 最大值', false)
      return
    }
    if (f.call_log_max < 100) {
      toast('日志保留上限至少 100 条', false)
      return
    }
    try {
      await api.configSet('timeout_ttft_min_ms', String(f.timeout_ttft_min_ms))
      await api.configSet('timeout_ttft_max_ms', String(f.timeout_ttft_max_ms))
      await api.configSet('timeout_silence_min_ms', String(f.timeout_silence_min_ms))
      await api.configSet('timeout_silence_max_ms', String(f.timeout_silence_max_ms))
      await api.configSet('failover_probe_min', String(f.failover_probe_min))
      await api.configSet('failover_probe_max', String(f.failover_probe_max))
      await api.configSet('call_log_max', String(f.call_log_max))
      toast('超时配置已保存（重启网关后生效）', true)
    } catch (e) {
      console.error('保存超时配置失败', e)
      toast('保存失败', false)
    }
  }

  const handleShowNodePrefixChange = async (enabled: boolean) => {
    try {
      await api.configSet('show_node_prefix', String(enabled))
      setShowNodePrefix(enabled)
      toast(enabled ? '已开启节点前缀展示' : '已关闭节点前缀展示', true)
    } catch (e) {
      console.error('设置节点前缀失败', e)
      toast('设置失败', false)
    }
  }

  // 订阅自动拉取配置（main 功能 M1）
  const handleSaveSubscribe = async () => {
    try {
      await api.configSet('subscribe_url', subscribeUrl.trim())
      await api.configSet('subscribe_interval_min', String(subscribeInterval))
      toast('订阅配置已保存（后台按间隔自动拉取并入实例）', true)
    } catch (e) {
      console.error('保存订阅配置失败', e)
      toast('保存失败', false)
    }
  }

  // 一键拉取并导入（main 功能 M1）：立即拉取订阅 → 批量建实例（独享 / 进池）
  const handleSubscribeImport = async () => {
    if (!subscribeUrl.trim()) {
      toast('请先填写订阅 URL', false)
      return
    }
    setSubscribeBusy(true)
    try {
      const n = await api.subscribeImport(subscribeUrl.trim(), subscribeTarget === 'pool')
      toast(`订阅拉取成功：批量导入 ${n} 个实例（${subscribeTarget === 'pool' ? '已入池' : '独享'}）`, true)
    } catch (e) {
      console.error('订阅导入失败', e)
      toast(String(e), false)
    } finally {
      setSubscribeBusy(false)
    }
  }

  // 统一网关密钥（main 功能 M6）
  const handleSaveGatewayKey = async () => {
    const v = gatewayKey.trim()
    if (v && v.length < 8) {
      toast('网关密钥至少 8 个字符', false)
      return
    }
    try {
      await api.configSet('gateway_key', v)
      setGatewayKey('')
      const cfg = await api.configGet()
      setConfig(cfg)
      toast(v ? '网关密钥已更新（重启网关后生效）' : '网关密钥已重置为默认', true)
    } catch (e) {
      console.error('保存网关密钥失败', e)
      toast('保存失败', false)
    }
  }

  // 健康巡检（main 功能 M2）
  const handleSaveHealth = async () => {
    try {
      await api.configSet('health_check_interval_sec', String(healthInterval))
      await api.configSet('health_restart_threshold', String(healthThreshold))
      toast('健康巡检配置已保存', true)
    } catch (e) {
      console.error('保存健康巡检失败', e)
      toast('保存失败', false)
    }
  }

  // 实例池性能模式（P1/P2）：探活间隔/窗口 + 熔断阈值/半开 + 开关
  const handleSavePool = async () => {
    const f = poolForm
    if (f.pool_probe_interval_sec < 0 || f.pool_quality_window_min < 1 ||
        f.pool_breaker_threshold < 1 || f.pool_halfopen_interval_sec < 1) {
      toast('性能模式参数不合法：间隔需 ≥0，窗口/熔断/半开需 ≥1', false)
      return
    }
    const concurrency: [string, number, number, number][] = [
      ['竞速并行（1~4）', f.pool_race_copies, 1, 4],
      ['节点扫描并发（1~16）', f.scan_concurrency, 1, 16],
      ['批量启停/释放并发（1~16）', f.batch_concurrency, 1, 16],
      ['一键测试并发（1~16）', f.test_concurrency, 1, 16],
      ['链路探活并发（1~16）', f.pool_probe_concurrency, 1, 16],
    ]
    for (const [label, v, lo, hi] of concurrency) {
      if (v < lo || v > hi) {
        toast(`并发不合法：${label}`, false)
        return
      }
    }
    try {
      await api.configSet('pool_probe_interval_sec', String(f.pool_probe_interval_sec))
      await api.configSet('pool_quality_window_min', String(f.pool_quality_window_min))
      await api.configSet('pool_breaker_threshold', String(f.pool_breaker_threshold))
      await api.configSet('pool_halfopen_interval_sec', String(f.pool_halfopen_interval_sec))
      await api.configSet('pool_probe_enabled', String(poolProbeEnabled))
      await api.configSet('pool_performance_mode', String(perfMode))
      await api.configSet('pool_race_copies', String(f.pool_race_copies))
      await api.configSet('scan_concurrency', String(f.scan_concurrency))
      await api.configSet('batch_concurrency', String(f.batch_concurrency))
      await api.configSet('test_concurrency', String(f.test_concurrency))
      await api.configSet('pool_probe_concurrency', String(f.pool_probe_concurrency))
      toast('性能模式配置已保存（热生效）', true)
    } catch (e) {
      console.error('保存性能模式配置失败', e)
      toast('保存失败', false)
    }
  }

  // 残留进程：探测 / 全选 / 一键清除
  const doScanOrphans = async () => {
    setOrphanBusy(true)
    try {
      const s = await api.orphanScan()
      setOrphans(s.items)
      setSelected(new Set(s.items.map((i) => i.pid)))
      toast(`探测到 ${s.total} 个残留进程（探针 ${s.probe} · 孤儿 ${s.orphan}）`, s.total === 0)
    } catch (e) {
      console.error('探测残留进程失败', e)
      toast('探测失败', false)
    } finally {
      setOrphanBusy(false)
    }
  }

  const toggleAll = () => {
    setSelected((prev) => (prev.size === orphans.length ? new Set() : new Set(orphans.map((i) => i.pid))))
  }

  const toggleOne = (pid: number) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(pid)) next.delete(pid)
      else next.add(pid)
      return next
    })
  }

  const doKillOrphans = async () => {
    const pids = [...selected]
    if (pids.length === 0) {
      toast('未选中任何进程', false)
      return
    }
    if (!window.confirm(`确定清除选中的 ${pids.length} 个残留进程？\n运行中的实例与网关不受影响。`)) return
    setKillBusy(true)
    try {
      const r = await api.orphanKill(pids)
      const errCount = Object.keys(r.errors).length
      toast(`已清除 ${r.killed.length} 个残留进程${errCount ? `，失败 ${errCount}` : ''}`, errCount === 0)
      await doScanOrphans() // 清除后自动重新探测
    } catch (e) {
      console.error('清除残留进程失败', e)
      toast('清除失败', false)
    } finally {
      setKillBusy(false)
    }
  }

  const handleDataClean = async (level: 1 | 2 | 3) => {
    const labels: Record<number, string> = {
      1: '仅清理运行时数据（日志、统计、临时配置，保留实例记录）',
      2: '清理运行时数据 + 清空实例记录（回到空实例池）',
      3: '全部重置（运行数据 + 实例 + 配置，回到出厂默认）',
    }
    if (!window.confirm(`确定要执行「${labels[level]}」？\n\n这会先停止所有运行中的实例与网关。此操作不可撤销。`)) return
    if (level === 3 && !window.confirm('这是完全重置，将删除所有配置并备份到 config.json.bak。\n请再次确认继续？')) return
    try {
      await api.dataClean(level)
      try {
        const [cfg, as] = await Promise.all([api.configGet(), api.autostartGet()])
        setConfig(cfg)
        setAutostart(as)
      } catch { /* 忽略刷新失败 */ }
      toast('清理完成', true)
    } catch (e) {
      console.error('清理失败', e)
      toast('清理失败', false)
    }
  }

  if (!config || !binariesInfo) {
    return <div className="p-8 text-zinc-500">加载中...</div>
  }

  return (
    <div className="p-6 space-y-6 max-w-2xl mx-auto">
      <h1 className="text-2xl font-semibold text-zinc-900">设置</h1>

      {/* Clash 外部控制 */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">Clash 外部控制</h2>
        
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">URL</label>
          <input
            type="text"
            placeholder="http://127.0.0.1:9097"
            value={clashUrl}
            onChange={(e) => setClashUrl(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">Clash 控制面板的访问地址</p>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">访问密钥</label>
          <input
            type="password"
            placeholder={config.has_clash_token ? '留空则不修改' : ''}
            value={clashToken}
            onChange={(e) => setClashToken(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg"
          />
          {config.has_clash_token && (
            <p className="text-zinc-500 text-xs">已配置</p>
          )}
          <p className="text-zinc-500 text-xs">留空则不修改</p>
        </div>

        <button
          onClick={handleSaveClash}
          className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700"
        >
          保存
        </button>
      </div>

      {/* 网关超时切换（区间随机，防上游识别） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">网关超时切换</h2>
        <p className="text-zinc-500 text-xs">
          每次请求在区间内随机取超时值，避免固定超时被上游识别为定时扫描；最小值防止过密重试
        </p>

        {/* 首字超时 (TTFT) */}
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">首字超时 (TTFT)</label>
          <div className="flex items-center gap-3">
            <input
              type="number"
              min={1}
              value={timeoutForm.timeout_ttft_min_ms}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, timeout_ttft_min_ms: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-400">~</span>
            <input
              type="number"
              min={1}
              value={timeoutForm.timeout_ttft_max_ms}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, timeout_ttft_max_ms: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-500 text-xs">毫秒</span>
          </div>
          <p className="text-zinc-500 text-xs">建流后等待首个内容块，超时则判定异常并切换。默认 10s</p>
        </div>

        {/* 块间静默超时 */}
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">块间静默超时</label>
          <div className="flex items-center gap-3">
            <input
              type="number"
              min={1}
              value={timeoutForm.timeout_silence_min_ms}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, timeout_silence_min_ms: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-400">~</span>
            <input
              type="number"
              min={1}
              value={timeoutForm.timeout_silence_max_ms}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, timeout_silence_max_ms: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-500 text-xs">毫秒</span>
          </div>
          <p className="text-zinc-500 text-xs">两个数据块之间无数据，判定卡死并切换。默认 5s</p>
        </div>

        {/* 切换前并行探测数 */}
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">切换前并行探测数</label>
          <div className="flex items-center gap-3">
            <input
              type="number"
              min={1}
              value={timeoutForm.failover_probe_min}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, failover_probe_min: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-400">~</span>
            <input
              type="number"
              min={1}
              value={timeoutForm.failover_probe_max}
              onChange={(e) => setTimeoutForm({ ...timeoutForm, failover_probe_max: Number(e.target.value) })}
              className="w-28 px-3 py-2 border rounded-lg"
            />
            <span className="text-zinc-500 text-xs">个</span>
          </div>
          <p className="text-zinc-500 text-xs">切换前并行探测候选节点数量。默认 2~3</p>
        </div>

        {/* 日志保留上限 */}
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">调用日志保留上限</label>
          <input
            type="number"
            min={100}
            value={timeoutForm.call_log_max}
            onChange={(e) => setTimeoutForm({ ...timeoutForm, call_log_max: Number(e.target.value) })}
            className="w-28 px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">日志页最多保留的请求记录数。默认 5000</p>
        </div>

        {/* 节点前缀展示开关 */}
        <div className="flex items-center space-x-3">
          <label className="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={showNodePrefix}
              onChange={(e) => handleShowNodePrefixChange(e.target.checked)}
              className="sr-only peer"
            />
            <div className="w-11 h-6 bg-zinc-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-zinc-900"></div>
          </label>
          <span className="text-sm text-zinc-700">对话流首段展示「节点 · 模型」前缀</span>
        </div>
        <p className="text-zinc-500 text-xs">开启后每条回复显示由哪个实例/模型回答（切换节点时重新标注）。默认关闭</p>

        <button
          onClick={handleSaveTimeout}
          className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700"
        >
          保存超时配置
        </button>
      </div>

      {/* 订阅自动拉取（main 功能 M1） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">订阅自动拉取</h2>
        <p className="text-zinc-500 text-xs">配置订阅地址后，后台按间隔自动拉取并导入为实例（重复节点自动跳过）</p>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">订阅 URL</label>
          <input
            type="text"
            placeholder="https://example.com/sub"
            value={subscribeUrl}
            onChange={(e) => setSubscribeUrl(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">支持 Clash YAML / base64 / v2ray 链接（vmess/vless/trojan/ss/hysteria2）</p>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">拉取间隔（分钟，0 = 不自动拉取）</label>
          <input
            type="number"
            min={0}
            value={subscribeInterval}
            onChange={(e) => setSubscribeInterval(Number(e.target.value))}
            className="w-28 px-3 py-2 border rounded-lg"
          />
        </div>

        {/* 一键拉取并导入（main 功能 M1）：目标 = 独享 / 进池 */}
        <div className="flex items-center gap-3">
          <div className="flex items-center rounded-lg border border-zinc-200 bg-white p-0.5">
            <button
              type="button"
              onClick={() => setSubscribeTarget('solo')}
              className={clsx(
                'px-3 py-1 rounded-md text-[13px] transition-colors',
                subscribeTarget === 'solo' ? 'bg-zinc-900 text-white' : 'text-zinc-500 hover:bg-zinc-100',
              )}
              title="导入为独享实例（一人一实例，默认）"
            >
              独享
            </button>
            <button
              type="button"
              onClick={() => setSubscribeTarget('pool')}
              className={clsx(
                'px-3 py-1 rounded-md text-[13px] transition-colors',
                subscribeTarget === 'pool' ? 'bg-zinc-900 text-white' : 'text-zinc-500 hover:bg-zinc-100',
              )}
              title="导入并标记进实例池（聚合到统一网关）"
            >
              进池
            </button>
          </div>
          <button
            type="button"
            onClick={() => void handleSubscribeImport()}
            disabled={subscribeBusy}
            className="flex items-center gap-1.5 bg-green-600 text-white rounded-lg px-4 py-2 hover:bg-green-700 disabled:opacity-60 disabled:cursor-not-allowed"
          >
            {subscribeBusy ? '拉取中…' : '一键拉取并导入'}
          </button>
        </div>

        <button onClick={handleSaveSubscribe} className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700">
          保存
        </button>
      </div>

      {/* 统一网关密钥（main 功能 M6） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">统一网关密钥</h2>
        <p className="text-zinc-500 text-xs">
          客户端访问统一网关（实例池地址）时使用的 API 密钥{config.has_gateway_key ? '（当前已设置，留空则不修改）' : '（默认 sk-unified-local）'}
        </p>
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">新密钥（至少 8 字符，留空 = 重置为默认）</label>
          <input
            type="password"
            placeholder={config.has_gateway_key ? '输入新密钥以更换' : ''}
            value={gatewayKey}
            onChange={(e) => setGatewayKey(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg"
          />
        </div>
        <button onClick={handleSaveGatewayKey} className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700">
          保存
        </button>
      </div>

      {/* 健康巡检（main 功能 M2） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">健康巡检</h2>
        <p className="text-zinc-500 text-xs">周期探测实例 API 端口，连续失败达阈值自动重启（0 = 不启用）</p>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">检查间隔（秒，0 = 不巡检）</label>
          <input
            type="number"
            min={0}
            value={healthInterval}
            onChange={(e) => setHealthInterval(Number(e.target.value))}
            className="w-28 px-3 py-2 border rounded-lg"
          />
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">连续失败自动重启阈值（0 = 不重启）</label>
          <input
            type="number"
            min={0}
            value={healthThreshold}
            onChange={(e) => setHealthThreshold(Number(e.target.value))}
            className="w-28 px-3 py-2 border rounded-lg"
          />
        </div>

        <button onClick={handleSaveHealth} className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700">
          保存
        </button>
      </div>

      {/* 实例池性能模式（P1/P2） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">实例池性能模式</h2>
        <p className="text-zinc-500 text-xs">
          链路级主动探活（经实例出口发真实请求）+ 质量加权路由：坏节点自动降权/剔除，熔断到期自动回归，全程无感。
        </p>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">探活间隔（秒，0 = 不自动探活）</label>
          <input
            type="number"
            min={0}
            value={poolForm.pool_probe_interval_sec}
            onChange={(e) => setPoolForm({ ...poolForm, pool_probe_interval_sec: Number(e.target.value) })}
            className="w-28 px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">后台按此间隔探测全部运行实例的链路质量。默认 45</p>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">质量窗口（分钟）</label>
          <input
            type="number"
            min={1}
            value={poolForm.pool_quality_window_min}
            onChange={(e) => setPoolForm({ ...poolForm, pool_quality_window_min: Number(e.target.value) })}
            className="w-28 px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">质量分统计最近 N 分钟内的探活样本。默认 10</p>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">熔断阈值（连续失败次数）</label>
          <input
            type="number"
            min={1}
            value={poolForm.pool_breaker_threshold}
            onChange={(e) => setPoolForm({ ...poolForm, pool_breaker_threshold: Number(e.target.value) })}
            className="w-28 px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">连续失败达阈值后节点进入熔断，路由不再选中。默认 3</p>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">半开间隔（秒）</label>
          <input
            type="number"
            min={1}
            value={poolForm.pool_halfopen_interval_sec}
            onChange={(e) => setPoolForm({ ...poolForm, pool_halfopen_interval_sec: Number(e.target.value) })}
            className="w-28 px-3 py-2 border rounded-lg"
          />
          <p className="text-zinc-500 text-xs">熔断到期后放行 1 个探测请求，成功即自动回归池子。默认 60</p>
        </div>

        {/* 并发设置（D3） */}
        <div className="pt-2 border-t border-zinc-100">
          <div className="text-sm font-medium text-zinc-700 mb-2">并发设置</div>
          <div className="grid grid-cols-2 gap-x-6 gap-y-2">
            {([
              ['pool_race_copies', '竞速并行（1~4）', 1, 4],
              ['scan_concurrency', '节点扫描并发（1~16）', 1, 16],
              ['batch_concurrency', '批量启停/释放（1~16）', 1, 16],
              ['test_concurrency', '一键测试并发（1~16）', 1, 16],
              ['pool_probe_concurrency', '链路探活并发（1~16）', 1, 16],
            ] as const).map(([key, label, lo, hi]) => (
              <div key={key} className="flex items-center justify-between gap-3">
                <label className="text-[13px] text-zinc-600">{label}</label>
                <input
                  type="number"
                  min={lo}
                  max={hi}
                  value={poolForm[key]}
                  onChange={(e) => setPoolForm({ ...poolForm, [key]: Number(e.target.value) })}
                  className="w-20 px-2 py-1.5 border rounded-lg text-[13px] text-right"
                />
              </div>
            ))}
          </div>
          <p className="text-zinc-500 text-xs mt-1.5">并发过高可能引起进程风暴，建议保持默认</p>
        </div>

        {/* 探活启用开关 */}
        <div className="flex items-center space-x-3">
          <label className="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={poolProbeEnabled}
              onChange={(e) => setPoolProbeEnabled(e.target.checked)}
              className="sr-only peer"
            />
            <div className="w-11 h-6 bg-zinc-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-zinc-900"></div>
          </label>
          <span className="text-sm text-zinc-700">链路主动探活</span>
        </div>

        {/* 性能模式开关 */}
        <div className="flex items-center space-x-3">
          <label className="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={perfMode}
              onChange={(e) => setPerfMode(e.target.checked)}
              className="sr-only peer"
            />
            <div className="w-11 h-6 bg-zinc-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-zinc-900"></div>
          </label>
          <span className="text-sm text-zinc-700">性能模式（质量加权路由 + 熔断自动恢复）</span>
        </div>
        <p className="text-zinc-500 text-xs">关闭后路由行为与基线一致（纯游标 + 冷却），探活记录保留</p>

        <button onClick={handleSavePool} className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700">
          保存
        </button>
      </div>

      {/* 开机自启 */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">开机自启</h2>
        
        <div className="flex items-center space-x-3">
          <label className="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={autostart}
              onChange={(e) => handleAutostartChange(e.target.checked)}
              className="sr-only peer"
            />
            <div className="w-11 h-6 bg-zinc-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-zinc-900"></div>
          </label>
          <span className="text-sm text-zinc-700">开机时自动启动管理器</span>
        </div>
        <p className="text-zinc-500 text-xs">Windows 注册表</p>
      </div>

      {/* 残留进程清理（孤儿实例 / 探针残留） */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h2 className="text-lg font-medium text-zinc-900">残留进程清理</h2>
            <p className="text-zinc-500 text-xs">
              探测「占着进程但未使用」的节点/实例/探针残留（扫描残留、已停止实例的孤儿进程），勾选后一键清除；运行中的实例与网关自动跳过。
            </p>
          </div>
          <button
            onClick={() => void doScanOrphans()}
            disabled={orphanBusy}
            className="flex items-center gap-1.5 bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700 disabled:opacity-60 disabled:cursor-not-allowed whitespace-nowrap"
          >
            {orphanBusy ? <Loader2 size={14} className="animate-spin" /> : <Search size={14} />}
            {orphanBusy ? '探测中…' : '探测残留'}
          </button>
        </div>

        {orphans.length > 0 && (
          <>
            <div className="rounded-lg border border-zinc-200 overflow-hidden">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="text-left text-zinc-400 bg-zinc-50 border-b border-zinc-100">
                    <th className="py-2 pl-3 w-8">
                      <input
                        type="checkbox"
                        checked={selected.size === orphans.length && orphans.length > 0}
                        onChange={toggleAll}
                        title="全选/取消"
                      />
                    </th>
                    <th className="py-2 pl-2">进程</th>
                    <th className="py-2 pl-2">PID</th>
                    <th className="py-2 pl-2">类型</th>
                    <th className="py-2 pl-2 pr-3">说明</th>
                  </tr>
                </thead>
                <tbody>
                  {orphans.map((o) => (
                    <tr key={o.pid} className="border-b border-zinc-50 hover:bg-zinc-50/60">
                      <td className="py-2 pl-3">
                        <input type="checkbox" checked={selected.has(o.pid)} onChange={() => toggleOne(o.pid)} />
                      </td>
                      <td className="py-2 pl-2 text-zinc-700 font-mono">{o.name}</td>
                      <td className="py-2 pl-2 text-zinc-500">{o.pid}</td>
                      <td className="py-2 pl-2">
                        <span
                          className={clsx(
                            'inline-block px-2 py-0.5 rounded-full text-[11px] font-medium',
                            o.category === 'probe' ? 'bg-orange-50 text-orange-600' : 'bg-amber-50 text-amber-700',
                          )}
                        >
                          {o.category === 'probe' ? '探针残留' : '实例残留'}
                        </span>
                        {o.instance && <span className="ml-1.5 text-[12px] text-zinc-500">{o.instance}</span>}
                        {o.port! > 0 && <span className="ml-1.5 text-[12px] text-zinc-400">端口 {o.port}</span>}
                      </td>
                      <td className="py-2 pl-2 pr-3 text-zinc-500">{o.detail}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex items-center justify-end gap-3">
              <span className="text-[12px] text-zinc-400">
                已选 {selected.size} / {orphans.length}
              </span>
              <button
                onClick={() => void doKillOrphans()}
                disabled={killBusy || selected.size === 0}
                className="flex items-center gap-1.5 bg-red-600 text-white rounded-lg px-4 py-2 text-sm hover:bg-red-700 disabled:opacity-60 disabled:cursor-not-allowed"
              >
                {killBusy ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
                {killBusy ? '清除中…' : '一键清除'}
              </button>
            </div>
          </>
        )}
      </div>

      {/* 清除数据 */}
      <div className="bg-white rounded-2xl border p-5 space-y-4 border-red-200">
        <h2 className="text-lg font-medium text-red-700">清除数据</h2>
        <p className="text-zinc-500 text-xs">
          遇到环境异常（实例/端口残留、配置损坏）时可清理本地数据。执行前会自动停止所有实例与网关。
        </p>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => handleDataClean(1)}
            className="px-4 py-2 rounded-lg border border-zinc-300 text-sm hover:bg-zinc-100"
          >
            清理运行数据
          </button>
          <button
            onClick={() => handleDataClean(2)}
            className="px-4 py-2 rounded-lg border border-amber-300 text-sm text-amber-700 hover:bg-amber-50"
          >
            清空实例记录
          </button>
          <button
            onClick={() => handleDataClean(3)}
            className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm hover:bg-red-700"
          >
            全部重置
          </button>
        </div>
        <p className="text-zinc-500 text-xs">全部重置会删除 config.json（备份为 config.json.bak），需重新配置</p>
      </div>

      {/* 关于 */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">关于</h2>
        
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">二进制目录</label>
          <code className="block text-sm bg-zinc-100 px-3 py-2 rounded border font-mono">
            {binariesInfo.bin_dir}
          </code>
        </div>

        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">子程序状态</label>
          <div className="space-y-1">
            <div className="flex items-center space-x-2 text-sm">
              <span className={binariesInfo.oc_exists ? 'text-green-600' : 'text-red-600'}>
                {binariesInfo.oc_exists ? '✓' : '✗'}
              </span>
              <span>opencode2api.exe</span>
            </div>
            <div className="flex items-center space-x-2 text-sm">
              <span className={binariesInfo.sb_exists ? 'text-green-600' : 'text-red-600'}>
                {binariesInfo.sb_exists ? '✓' : '✗'}
              </span>
              <span>sing-box.exe</span>
            </div>
          </div>
        </div>

        <p className="text-zinc-500 text-xs">子程序随主程序内嵌，运行时不满足时自动释放</p>

        {/* D1：退出程序（二次确认由 App 层弹窗负责） */}
        <div className="pt-3 border-t border-zinc-100">
          <button
            type="button"
            onClick={() => onRequestExit?.()}
            className="flex items-center gap-1.5 px-4 py-2 rounded-lg text-[13px] font-medium text-red-600 bg-red-50 hover:bg-red-100 transition-colors"
          >
            <LogOut size={14} />
            退出程序
          </button>
          <p className="text-zinc-500 text-xs mt-2">退出前可先释放全部实例（停止并删除）；不释放则实例留在后台继续运行</p>
        </div>
      </div>
    </div>
  )
}