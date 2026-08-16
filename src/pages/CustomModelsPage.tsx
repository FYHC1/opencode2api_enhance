import { useEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import { Loader2, Pencil, Plus, PlugZap, Trash2, X } from 'lucide-react'
import { api, type CustomProviderInput, type CustomProviderTestResult, type CustomProviderView, type CustomProtocol } from '../lib/api'

// 自定义模型源表单（新增/编辑共用）。编辑时 key 留空 = 保留原 key。
type FormState = {
  id: string
  name: string
  protocol: CustomProtocol
  base_url: string
  api_key: string
  via_proxy: boolean
  enabled: boolean
  /** 编辑中的原条目 id（空 = 新增） */
  editing: string | null
}

const emptyForm = (): FormState => ({
  id: '',
  name: '',
  protocol: 'openai',
  base_url: '',
  api_key: '',
  via_proxy: false,
  enabled: true,
  editing: null,
})

const PROTOCOLS: { value: CustomProtocol; label: string; hint: string }[] = [
  { value: 'openai', label: 'OpenAI 兼容', hint: 'https://api.openai.com/v1' },
  { value: 'anthropic', label: 'Anthropic', hint: 'https://api.anthropic.com/v1' },
  { value: 'responses', label: 'OpenAI Responses', hint: 'https://api.openai.com/v1' },
  { value: 'gemini', label: 'Google Gemini', hint: 'https://generativelanguage.googleapis.com/v1beta' },
]

/** id 规则与后端一致：字母数字开头，可含 - _，≤32 字符 */
const validId = (id: string) => /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$/.test(id)

export default function CustomModelsPage({ toast }: { toast: (msg: string, ok?: boolean) => void }) {
  const [list, setList] = useState<CustomProviderView[] | null>(null)
  const [form, setForm] = useState<FormState | null>(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testResult, setTestResult] = useState<CustomProviderTestResult | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  // toast 用 ref 封装（App 的 showToast 每次渲染重建），effect 只跑一次
  const toastRef = useRef(toast)
  toastRef.current = toast

  const reload = async () => {
    try {
      const r = await api.customProvidersList()
      setList(r.providers ?? [])
    } catch (e) {
      console.error('加载自定义模型源失败', e)
      toastRef.current('加载自定义模型源失败', false)
    }
  }

  useEffect(() => {
    void reload()
  }, [])

  const openAdd = () => {
    setTestResult(null)
    setForm(emptyForm())
  }

  const openEdit = (p: CustomProviderView) => {
    setTestResult(null)
    setForm({
      id: p.id,
      name: p.name,
      protocol: p.protocol,
      base_url: p.base_url,
      api_key: p.api_key ?? '', // 回填已存 key：测试连通免重新粘贴
      via_proxy: p.via_proxy,
      enabled: p.enabled,
      editing: p.id,
    })
  }

  // 表单 → 保存请求项（id 编辑中不可改）
  const formToInput = (f: FormState): CustomProviderInput | null => {
    if (!validId(f.id.trim())) {
      toast('源 ID 需字母数字开头，可含 - _，不超过 32 字符', false)
      return null
    }
    if (!/^https?:\/\/.+/.test(f.base_url.trim())) {
      toast('API 地址需为 http(s) URL', false)
      return null
    }
    return {
      id: f.id.trim(),
      name: f.name.trim() || f.id.trim(),
      protocol: f.protocol,
      base_url: f.base_url.trim(),
      api_key: f.api_key.trim() || undefined,
      via_proxy: f.via_proxy,
      enabled: f.enabled,
    }
  }

  // 「测试并获取模型」：当前表单临时拉取目录（不落盘）
  const doTest = async () => {
    if (!form) return
    const input = formToInput(form)
    if (!input) return
    setTesting(true)
    setTestResult(null)
    try {
      const r = await api.customProvidersTest(input)
      setTestResult(r)
    } catch (e) {
      setTestResult({ ok: false, error: String(e) })
    } finally {
      setTesting(false)
    }
  }

  // 保存：整表提交（现有列表去掉编辑项 + 表单项）
  const doSave = async () => {
    if (!form) return
    const input = formToInput(form)
    if (!input) return
    setSaving(true)
    try {
      const others = (list ?? [])
        .filter((p) => p.id !== form.editing)
        .map<CustomProviderInput>((p) => ({
          id: p.id,
          name: p.name,
          protocol: p.protocol,
          base_url: p.base_url,
          via_proxy: p.via_proxy,
          enabled: p.enabled,
        }))
      const r = await api.customProvidersSave([...others, input])
      setList(r.providers ?? [])
      setForm(null)
      setTestResult(null)
      toast(`已保存：模型名前缀 ${input.id}/，立即生效`, true)
    } catch (e) {
      console.error('保存失败', e)
      toast(`保存失败：${String(e)}`, false)
    } finally {
      setSaving(false)
    }
  }

  // 删除 / 启停 / via_proxy 切换：整表保存（去掉或修改目标项）
  const saveAll = async (providers: CustomProviderInput[], okMsg: string) => {
    setSaving(true)
    try {
      const r = await api.customProvidersSave(providers)
      setList(r.providers ?? [])
      toast(okMsg, true)
    } catch (e) {
      console.error('保存失败', e)
      toast(`保存失败：${String(e)}`, false)
    } finally {
      setSaving(false)
    }
  }

  const toInputs = (ps: CustomProviderView[]): CustomProviderInput[] =>
    ps.map((p) => ({
      id: p.id,
      name: p.name,
      protocol: p.protocol,
      base_url: p.base_url,
      via_proxy: p.via_proxy,
      enabled: p.enabled,
    }))

  const doDelete = async (id: string) => {
    setConfirmDelete(null)
    await saveAll(toInputs((list ?? []).filter((p) => p.id !== id)), `已删除 ${id}`)
  }

  const toggleEnabled = async (p: CustomProviderView) => {
    await saveAll(
      toInputs((list ?? []).map((x) => (x.id === p.id ? { ...x, enabled: !x.enabled } : x))),
      p.enabled ? `已停用 ${p.id}（模型不再出现在 /v1/models）` : `已启用 ${p.id}`,
    )
  }

  return (
    <div className="p-6 space-y-6 max-w-3xl mx-auto">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-zinc-900">自定义模型</h1>
          <p className="text-zinc-500 text-xs mt-1">
            接入自带 API Key 的第三方模型供应商（OpenAI / Anthropic / Gemini 三种协议），可同时接入多个。
            保存后模型进入 /v1/models（模型名带 <code className="bg-zinc-100 px-1 rounded">源ID/</code> 前缀），调用、日志、统计与节点池全部复用统一网关。
          </p>
        </div>
        <button
          onClick={openAdd}
          className="flex items-center gap-1.5 bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700 whitespace-nowrap"
        >
          <Plus size={14} />
          添加模型源
        </button>
      </div>

      {/* 源列表 */}
      {list === null ? (
        <div className="text-zinc-500">加载中...</div>
      ) : list.length === 0 ? (
        <div className="bg-white rounded-2xl border p-8 text-center text-zinc-500 text-sm">
          还没有自定义模型源。点击右上角「添加模型源」，填入供应商的 API 地址与 Key 即可接入。
        </div>
      ) : (
        <div className="space-y-3">
          {list.map((p) => (
            <div key={p.id} className={clsx('bg-white rounded-2xl border p-4', !p.enabled && 'opacity-60')}>
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0 space-y-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-[15px] font-medium text-zinc-900">{p.name || p.id}</span>
                    <span className="text-[11px] px-1.5 py-0.5 rounded bg-zinc-100 text-zinc-600 font-mono">{p.protocol}</span>
                    {!p.enabled && <span className="text-[11px] px-1.5 py-0.5 rounded bg-zinc-200 text-zinc-500">已停用</span>}
                    {p.via_proxy && <span className="text-[11px] px-1.5 py-0.5 rounded bg-amber-50 text-amber-700">走节点池</span>}
                  </div>
                  <div className="text-xs text-zinc-500 font-mono truncate">{p.base_url}</div>
                  <div className="text-xs text-zinc-500">
                    前缀 <code className="bg-zinc-100 px-1 rounded">{p.id}/</code> · {p.models} 个模型
                    {p.api_key_set ? ' · Key 已配置' : ' · 无 Key'}
                    {p.last_error ? <span className="text-red-500"> · {p.last_error}</span> : null}
                  </div>
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                  {/* 启停 */}
                  <button
                    type="button"
                    onClick={() => void toggleEnabled(p)}
                    disabled={saving}
                    className={clsx(
                      'relative inline-flex h-6 w-11 items-center rounded-full transition-colors disabled:opacity-50',
                      p.enabled ? 'bg-zinc-900' : 'bg-zinc-200',
                    )}
                    aria-label={p.enabled ? '停用' : '启用'}
                  >
                    <span
                      className={clsx(
                        'inline-block h-5 w-5 transform rounded-full bg-white border border-zinc-300 transition-transform',
                        p.enabled ? 'translate-x-[22px]' : 'translate-x-[2px]',
                      )}
                    />
                  </button>
                  <button
                    type="button"
                    onClick={() => openEdit(p)}
                    className="p-2 rounded-lg text-zinc-500 hover:bg-zinc-100"
                    aria-label="编辑"
                  >
                    <Pencil size={15} />
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmDelete(p.id)}
                    className="p-2 rounded-lg text-red-500 hover:bg-red-50"
                    aria-label="删除"
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              </div>

              {/* 删除二次确认 */}
              {confirmDelete === p.id && (
                <div className="mt-3 flex items-center gap-2 text-xs bg-red-50 rounded-lg p-2.5">
                  <span className="flex-1 text-red-700">删除源 {p.id}？使用其前缀模型的客户端将不可用。</span>
                  <button type="button" onClick={() => void doDelete(p.id)} disabled={saving} className="px-2.5 py-1 rounded bg-red-600 text-white hover:bg-red-700 disabled:opacity-50">
                    删除
                  </button>
                  <button type="button" onClick={() => setConfirmDelete(null)} className="px-2.5 py-1 rounded bg-white border border-zinc-200 text-zinc-600">
                    取消
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* 新增/编辑弹层 */}
      {form && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-900/40 p-4" onClick={() => !saving && setForm(null)}>
          <div className="bg-white rounded-2xl shadow-xl w-[520px] max-h-[90vh] overflow-y-auto p-6 space-y-4" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between">
              <div className="text-[15px] font-semibold text-zinc-900">{form.editing ? `编辑模型源 · ${form.editing}` : '添加模型源'}</div>
              <button type="button" onClick={() => setForm(null)} className="p-1.5 rounded-lg text-zinc-400 hover:bg-zinc-100">
                <X size={16} />
              </button>
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-zinc-700">源 ID</label>
              <input
                type="text"
                placeholder="如 myglm / openrouter"
                value={form.id}
                disabled={!!form.editing}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
                className="w-full px-3 py-2 border rounded-lg font-mono disabled:bg-zinc-50 disabled:text-zinc-400"
              />
              <p className="text-zinc-500 text-xs">字母数字开头，可含 - _；模型将以 <code className="bg-zinc-100 px-1 rounded">{form.id || '源ID'}/模型名</code> 形式出现在 /v1/models</p>
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-zinc-700">显示名称</label>
              <input
                type="text"
                placeholder="如 智谱 GLM"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className="w-full px-3 py-2 border rounded-lg"
              />
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-zinc-700">协议</label>
              <div className="grid grid-cols-2 gap-2">
                {PROTOCOLS.map((p) => (
                  <button
                    key={p.value}
                    type="button"
                    onClick={() => setForm({ ...form, protocol: p.value })}
                    className={clsx(
                      'px-3 py-2 rounded-lg border text-[13px] transition-colors',
                      form.protocol === p.value ? 'border-zinc-900 bg-zinc-900 text-white' : 'border-zinc-200 text-zinc-600 hover:bg-zinc-50',
                    )}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
              <p className="text-zinc-500 text-xs">API 根地址示例：{PROTOCOLS.find((p) => p.value === form.protocol)?.hint}</p>
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-zinc-700">API 地址（base_url）</label>
              <input
                type="text"
                placeholder={PROTOCOLS.find((p) => p.value === form.protocol)?.hint}
                value={form.base_url}
                onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                className="w-full px-3 py-2 border rounded-lg font-mono text-[13px]"
              />
              <p className="text-zinc-500 text-xs">填到版本根路径（含 /v1 或 /v1beta），不要带尾斜杠</p>
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-zinc-700">API Key</label>
              <input
                type="password"
                placeholder="sk-...（本地无鉴权网关可留空）"
                value={form.api_key}
                onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                className="w-full px-3 py-2 border rounded-lg font-mono text-[13px]"
              />
              <p className="text-zinc-500 text-xs">Key 保存在本机配置中，由网关持有；调用方无需携带</p>
            </div>

            <div className="flex items-center space-x-3">
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.via_proxy}
                  onChange={(e) => setForm({ ...form, via_proxy: e.target.checked })}
                  className="sr-only peer"
                />
                <div className="w-11 h-6 bg-zinc-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-zinc-900"></div>
              </label>
              <span className="text-sm text-zinc-700">出站走节点池代理</span>
              <span className="text-zinc-500 text-xs">（默认直连；供应商有地区限制时开启）</span>
            </div>

            {/* 测试结果 */}
            {testResult && (
              <div className={clsx('rounded-lg p-3 text-xs space-y-1', testResult.ok ? 'bg-emerald-50 text-emerald-800' : 'bg-red-50 text-red-700')}>
                {testResult.ok ? (
                  <>
                    <div className="font-medium">
                      连通成功 · {testResult.count} 个模型 · {testResult.latency_ms}ms
                    </div>
                    <div className="font-mono break-all leading-relaxed">
                      {(testResult.models ?? []).slice(0, 12).join(' · ')}
                      {(testResult.count ?? 0) > 12 ? ' …' : ''}
                    </div>
                  </>
                ) : (
                  <div className="font-medium">连通失败：{testResult.error}</div>
                )}
              </div>
            )}

            <div className="flex items-center gap-2 pt-1">
              <button
                type="button"
                onClick={() => void doTest()}
                disabled={testing || saving}
                className="flex items-center gap-1.5 px-4 py-2 rounded-lg border border-zinc-300 text-zinc-700 hover:bg-zinc-50 disabled:opacity-60 disabled:cursor-not-allowed"
              >
                {testing ? <Loader2 size={14} className="animate-spin" /> : <PlugZap size={14} />}
                {testing ? '测试中…' : '测试并获取模型'}
              </button>
              <div className="flex-1" />
              <button
                type="button"
                onClick={() => void doSave()}
                disabled={testing || saving}
                className="flex items-center gap-1.5 bg-zinc-900 text-white rounded-lg px-4 py-2 hover:bg-zinc-700 disabled:opacity-60 disabled:cursor-not-allowed"
              >
                {saving ? <Loader2 size={14} className="animate-spin" /> : null}
                {saving ? '保存中…' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
