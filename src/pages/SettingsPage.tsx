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

  // 实例默认密码表单
  const [defaultPassword, setDefaultPassword] = useState('')

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
        setDefaultPassword('')
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

  const handleSavePassword = async () => {
    try {
      if (defaultPassword.trim()) {
        await api.configSet('default_password', defaultPassword)
        toast('已保存', true)
        const cfg = await api.configGet()
        setConfig(cfg)
        setDefaultPassword('')
      } else {
        toast('请输入密码', false)
      }
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
          <label className="block text-sm font-medium text-zinc-700">Token 密码</label>
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

      {/* 实例默认密码 */}
      <div className="bg-white rounded-2xl border p-5 space-y-4">
        <h2 className="text-lg font-medium text-zinc-900">实例默认密码</h2>
        
        <div className="space-y-2">
          <label className="block text-sm font-medium text-zinc-700">密码</label>
          <input
            type="password"
            placeholder={config.has_password ? '已设置，留空不修改' : ''}
            value={defaultPassword}
            onChange={(e) => setDefaultPassword(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg"
          />
          {config.has_password && (
            <p className="text-zinc-500 text-xs">已设置，留空不修改</p>
          )}
        </div>

        <button
          onClick={handleSavePassword}
          className="bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700"
        >
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