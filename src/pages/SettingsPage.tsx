import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import type { ConfigView, BinariesInfo } from '../lib/api'

export default function SettingsPage({ toast }: { toast: (msg: string, ok?: boolean) => void }) {
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

        <button
          onClick={handleSaveTimeout}
          className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700"
        >
          保存超时配置
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
      </div>
    </div>
  )
}