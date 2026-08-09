// API 对接层：桌面(壳)与 Web 共用，统一调用 core 的 /api/admin/* HTTP 接口。
// 开机自启由 Go core 承载（写 Windows 注册表），经 HTTP 调用，与其它接口一致。
// 注意：窗口内容由 core 的 HTTP 服务提供（127.0.0.1:<port>），非 Tauri webview 环境，invoke 不可用。

// ─── 类型定义（与 Rust 端 serde 结构一一对应） ───────────────────────

export type InstanceStatus =
  | 'Stopped'
  | 'Starting'
  | 'Running'
  | 'Stopping'
  | { Error: string }

export type Instance = {
  name: string
  port: number
  node: string
  password: string
  ip: string
  singbox_port: number
  pid: number | null
  singbox_pid: number | null
  /** 是否加入统一网关池（默认 false = 独享实例） */
  join_gateway: boolean
  status: InstanceStatus
}

export type NodeView = {
  name: string
  node_type: string
  server: string
  port: number
  has_cred: boolean
  group: string
}

export type TestResult = {
  name: string
  port: number
  ok: boolean
  status_code: number | null
  model_count: number | null
  message: string
  latency_ms: number
}

export type BatchAddItem = {
  node: string
  name?: string | null
  port?: number | null
}

export type BatchAddResult = {
  added: { name: string; port: number; node: string }[]
  errors: { node: string; error: string }[]
  added_count: number
  error_count: number
}

export type BatchOpResult = {
  success: string[]
  errors: Record<string, string>
  success_count: number
  error_count: number
}

export type PortCheckResult = {
  available: boolean
  reason: string
}

export type ScanStatus = 'idle' | 'running' | 'stopping' | 'done' | 'error'

export type ProbeResult = {
  node: string
  node_type: string
  server: string
  port: number
  ok: boolean
  category: string
  status_code: number | null
  model_count: number | null
  message: string
  latency_ms: number
}

export type ScanProgress = {
  status: ScanStatus
  total: number
  current: number
  current_node: string | null
  results: ProbeResult[]
  error: string | null
  api_port: number
  socks_port: number
  started_ms: number | null
  finished_ms: number | null
}

export type ConfigView = {
  base_url: string
  default_password: string
  has_password: boolean
  clash_external_url: string
  has_clash_token: boolean
  timeout_ttft_min_ms: number
  timeout_ttft_max_ms: number
  timeout_silence_min_ms: number
  timeout_silence_max_ms: number
  failover_probe_min: number
  failover_probe_max: number
  call_log_max: number
  show_node_prefix: boolean
  subscribe_url: string
  subscribe_interval_min: number
  health_check_interval_sec: number
  health_restart_threshold: number
  has_gateway_key: boolean
  gateway_key: string
}

export type BinariesInfo = {
  bin_dir: string
  oc_exists: boolean
  sb_exists: boolean
}

// ─── 全流程调用日志 ─────────────────────────────────────────────

export type CallLogEvent = {
  type: string
  node?: string
  detail?: string
  at?: string
}

export type CallLogRecord = {
  req_id: string
  ts: string
  path?: string
  model?: string
  stream?: boolean
  route_mode?: string
  nodes?: string[]
  events?: CallLogEvent[]
  status: string
  prompt_tokens?: number
  completion_tokens?: number
  duration_ms?: number
  err_msg?: string
}

// ─── 统一网关（实例池） ─────────────────────────────────────────────

export type GatewayStatus = {
  running: boolean
  address: string
  port: number
  api_key: string
  running_instances: number
  total_instances: number
  message: string
  route_mode: 'smart' | 'failover' | 'round_robin'
  free_models: string[]
  free_models_updated_at: number | null
  free_models_loading: boolean
  free_models_error: string | null
}

export type RestartPoolResult = {
  stopped: number
  started: number
  freed_ports: number[]
  gateway_running: boolean
  error: string | null
}

// ─── Token 统计（按实例） ─────────────────────────────────────────────

export type ModelStat = {
  model: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export type GatewayNodeStat = {
  name: string
  addr: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export type InstanceStat = {
  name: string
  /** 实例目录存在但实例列表中已无（已删除/历史实例）时为 false */
  exists: boolean
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  models: ModelStat[]
  /** 仅统一网关条目：按节点（SOCKS5 出口）拆分的调用统计 */
  nodes?: GatewayNodeStat[]
}

export type StatsSummary = {
  total_requests: number
  total_prompt_tokens: number
  total_completion_tokens: number
  total_tokens: number
  instances: InstanceStat[]
}

export type ResetStatsResult = {
  /** 成功重置的项数（含实例与统一网关） */
  reset_count: number
  /** 清除的「已删除实例」历史统计目录数 */
  deleted_count: number
  /** 失败明细 */
  failed: string[]
}

// ─── 订阅（main 功能迁移 M1） ─────────────────────────────────────────────

export type SubscribeNode = {
  name: string
  server: string
  port: number
  node_type: string
  password?: string
  uuid?: string
  cipher?: string
  sni?: string
  network?: string
  ws_path?: string
  flow?: string
  tls: boolean
  raw: string
}

export type SubscribeResult = {
  nodes: SubscribeNode[]
  count: number
}

// ─── 健康巡检（main 功能迁移 M2） ─────────────────────────────────────────────

export type HealthRecord = {
  name: string
  healthy: boolean
  last_check_ts: number
  consecutive_failures: number
  last_error?: string
}

export type HealthSummary = {
  total: number
  healthy: number
  unhealthy: number
  records: HealthRecord[]
  last_scan_ts: number
}

// ─── 日志过滤与聚合（main 功能迁移 M4） ─────────────────────────────────────────────

export type CallLogFilter = {
  node?: string
  keyword?: string
  status?: 'ok' | 'error'
  limit?: number
  offset?: number
  from_ts?: string
  to_ts?: string
}

export type CallLogAggregate = {
  instance: string
  total: number
  errors: number
  last_ts: string
}

// ─── HTTP 对接层（core /api/admin/*） ─────────────────────────────

/** 后端基础地址：Web 同源为空；桌面壳可经构建注入 VITE_API_BASE。 */
const API_BASE: string = (import.meta.env?.VITE_API_BASE as string | undefined) ?? ''

async function req<T>(method: string, path: string, body?: unknown, qs?: Record<string, unknown>): Promise<T> {
  let url = API_BASE + '/api/admin' + path
  if (qs) {
    const p = new URLSearchParams()
    for (const [k, v] of Object.entries(qs)) {
      if (v !== undefined && v !== null) p.set(k, String(v))
    }
    const s = p.toString()
    if (s) url += '?' + s
  }
  const opts: RequestInit = { method, headers: {} }
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(url, opts)
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const j = await res.json()
      if (j && j.error) msg = j.error
    } catch { /* ignore */ }
    throw new Error(msg)
  }
  const ct = res.headers.get('content-type') ?? ''
  if (ct.includes('json')) return (await res.json()) as T
  return (await res.text()) as unknown as T
}

export const api = {
  // 节点
  listNodes: () => req<NodeView[]>('GET', '/nodes'),
  /** 删除订阅缓存节点（外部 Clash 节点只读跳过；main 功能 M5） */
  deleteNode: (name: string) => req<{ removed: number }>('POST', '/nodes/delete', { name }),
  deleteNodes: (names: string[]) => req<{ removed: number }>('POST', '/nodes/delete-batch', { names }),

  // 实例
  listInstances: () => req<Instance[]>('GET', '/instances'),
  /** 手动刷新指定实例的状态（返回这些实例的最新状态） */
  refreshStates: (names: string[]) => req<Instance[]>('POST', '/instances/refresh', { names }),
  addInstance: (name: string, port: number, node: string, password: string) =>
    req<Instance>('POST', '/instances/add', { name, port, node, password }),
  removeInstance: (name: string) => req<void>('POST', '/instances/remove', { name }),
  startInstance: (name: string) => req<void>('POST', '/instances/start', { name }),
  stopInstance: (name: string) => req<void>('POST', '/instances/stop', { name }),
  testInstance: (name: string) => req<TestResult>('POST', '/instances/test', { name }),
  batchAdd: (nodes: BatchAddItem[], basePort?: number, useNodeName?: boolean, namePrefix?: string) =>
    req<BatchAddResult>('POST', '/instances/batch/add', {
      nodes,
      basePort: basePort ?? undefined,
      useNodeName: useNodeName ?? undefined,
      namePrefix: namePrefix ?? undefined,
    }),
  batchStart: (names: string[]) => req<BatchOpResult>('POST', '/instances/batch/start', { names }),
  batchStop: (names: string[]) => req<BatchOpResult>('POST', '/instances/batch/stop', { names }),
  batchDelete: (names: string[]) => req<BatchOpResult>('POST', '/instances/batch/delete', { names }),

  // 端口
  portSuggest: () => req<number>('GET', '/port/suggest'),
  portCheck: (port: number) => req<PortCheckResult>('GET', '/port/check', undefined, { port }),

  // 扫描
  scanStart: (opts?: {
    nodes?: string[]
    apiPort?: number
    socksPort?: number
    timeout?: number
    /** 并发 worker 数（可选，默认后端 8） */
    concurrency?: number
  }) =>
    req<ScanProgress>('POST', '/scan/start', {
      nodes: opts?.nodes ?? undefined,
      apiPort: opts?.apiPort ?? undefined,
      socksPort: opts?.socksPort ?? undefined,
      timeout: opts?.timeout ?? undefined,
      concurrency: opts?.concurrency ?? undefined,
    }),
  scanStatus: () => req<ScanProgress>('GET', '/scan/status'),
  scanStop: () => req<ScanProgress>('POST', '/scan/stop'),

  // 配置
  configGet: () => req<ConfigView>('GET', '/config'),
  configSet: (key: string, value: string) => req<void>('POST', '/config/set', { key, value }),

  // 订阅（main 功能 M1）：preview 拉取解析、import 建实例、import-pool 仅入缓存
  subscribePreview: (url: string) => req<SubscribeResult>('POST', '/subscribe/preview', { url }),
  subscribeImport: (url: string, joinGateway?: boolean) =>
    req<{ status: string; imported: number }>('POST', '/subscribe/import', { url, join_gateway: joinGateway ?? undefined }),
  subscribeImportPool: (url: string) =>
    req<{ status: string; imported: number }>('POST', '/subscribe/import-pool', { url }),

  // 开机自启：由 Go core 承载（写 Windows 注册表），经 HTTP 调用
  autostartGet: async (): Promise<boolean> => {
    const r = await req<{ enabled: boolean }>('GET', '/autostart')
    return r.enabled
  },
  autostartSet: (enabled: boolean) => req<void>('POST', '/autostart/set', { enabled }),

  // 二进制信息
  getBinariesInfo: () => req<BinariesInfo>('GET', '/binaries'),

  // Token 统计（按实例）
  getStats: () => req<StatsSummary>('GET', '/stats'),
  /** 重置全部 Token 统计（clearDeleted=同时清除已删除节点历史统计） */
  resetStats: (clearDeleted?: boolean) =>
    req<ResetStatsResult>('POST', '/stats/reset', undefined, { clearDeleted: clearDeleted ?? undefined }),

  // 全流程调用日志
  getCallLog: (limit?: number) =>
    req<CallLogRecord[]>('GET', '/call-log', undefined, { limit: limit ?? undefined }),
  /** 清空全部调用日志 */
  clearCallLog: () => req<void>('POST', '/call-log/clear'),
  /** 过滤查询日志（main 功能 M4） */
  callLogFiltered: (filter: CallLogFilter) => req<CallLogRecord[]>('POST', '/call-log/filtered', filter),
  /** 按节点组合聚合日志（main 功能 M4） */
  callLogAggregate: () => req<CallLogAggregate[]>('GET', '/call-log/aggregate'),

  // 健康巡检（main 功能 M2）
  healthCheck: () => req<HealthSummary>('POST', '/health/check'),
  healthSummary: () => req<HealthSummary>('GET', '/health/summary'),

  // 报表导出（main 功能 M3）
  exportCallLogCSV: async (limit?: number): Promise<string> => {
    const res = await fetch(API_BASE + '/api/admin/export/call-log.csv' + (limit ? `?limit=${limit}` : ''))
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
  },
  exportInstancesJSON: async (): Promise<string> => {
    const res = await fetch(API_BASE + '/api/admin/export/instances.json')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
  },
  exportStatsJSON: async (): Promise<string> => {
    const res = await fetch(API_BASE + '/api/admin/export/stats.json')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
  },

  // 统一网关（实例池）
  gatewayStatus: () => req<GatewayStatus>('GET', '/gateway/status'),
  gatewaySetRouteMode: (mode: 'smart' | 'failover' | 'round_robin') =>
    req<void>('POST', '/gateway/route-mode', { mode }),
  gatewayStop: () => req<void>('POST', '/gateway/stop'),
  setJoinGateway: (name: string, join: boolean) =>
    req<void>('POST', '/instances/join-gateway', { name, join }),

  // 一键重启实例池（全停→强制清端口→全启→网关同步）
  restartPool: () => req<RestartPoolResult>('POST', '/pool/restart'),

  // 清除数据（1=运行数据, 2=+实例记录, 3=全部重置）
  dataClean: (level: 1 | 2 | 3) => req<void>('POST', '/data/clean', { level }),
}
