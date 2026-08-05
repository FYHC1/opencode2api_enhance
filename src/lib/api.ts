import { invoke } from '@tauri-apps/api/core'

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

// ─── Tauri command 封装 ─────────────────────────────────────────────

export const api = {
  // 节点
  listNodes: () => invoke<NodeView[]>('list_nodes'),

  // 实例
  listInstances: () => invoke<Instance[]>('list_instances'),
  /** 手动刷新指定实例的状态（返回这些实例的最新状态） */
  refreshStates: (names: string[]) => invoke<Instance[]>('refresh_states', { names }),
  addInstance: (name: string, port: number, node: string, password: string) =>
    invoke<Instance>('add_instance', { name, port, node, password }),
  removeInstance: (name: string) => invoke<void>('remove_instance', { name }),
  startInstance: (name: string) => invoke<void>('start_instance', { name }),
  stopInstance: (name: string) => invoke<void>('stop_instance', { name }),
  testInstance: (name: string) => invoke<TestResult>('test_instance', { name }),
  batchAdd: (nodes: BatchAddItem[], basePort?: number, useNodeName?: boolean, namePrefix?: string) =>
    invoke<BatchAddResult>('batch_add', {
      nodes,
      basePort: basePort ?? null,
      useNodeName: useNodeName ?? null,
      namePrefix: namePrefix ?? null,
    }),
  batchStart: (names: string[]) => invoke<BatchOpResult>('batch_start', { names }),
  batchStop: (names: string[]) => invoke<BatchOpResult>('batch_stop', { names }),
  batchDelete: (names: string[]) => invoke<BatchOpResult>('batch_delete', { names }),

  // 端口
  portSuggest: () => invoke<number>('port_suggest'),
  portCheck: (port: number) => invoke<PortCheckResult>('port_check', { port }),

  // 扫描
  scanStart: (opts?: {
    nodes?: string[]
    apiPort?: number
    socksPort?: number
    timeout?: number
  }) =>
    invoke<ScanProgress>('scan_start', {
      nodes: opts?.nodes ?? null,
      apiPort: opts?.apiPort ?? null,
      socksPort: opts?.socksPort ?? null,
      timeout: opts?.timeout ?? null,
    }),
  scanStatus: () => invoke<ScanProgress>('scan_status'),
  scanStop: () => invoke<ScanProgress>('scan_stop'),

  // 配置
  configGet: () => invoke<ConfigView>('config_get'),
  configSet: (key: string, value: string) => invoke<void>('config_set', { key, value }),

  // 开机自启
  autostartGet: () => invoke<boolean>('autostart_get'),
  autostartSet: (enabled: boolean) => invoke<void>('autostart_set', { enabled }),

  // 二进制信息
  getBinariesInfo: () => invoke<BinariesInfo>('get_binaries_info'),

  // Token 统计（按实例）
  getStats: () => invoke<StatsSummary>('get_stats'),

  // 全流程调用日志
  getCallLog: (limit?: number) =>
    invoke<CallLogRecord[]>('get_call_log', { limit: limit ?? null }),

  // 统一网关（实例池）
  gatewayStatus: () => invoke<GatewayStatus>('gateway_status'),
  gatewaySetRouteMode: (mode: 'smart' | 'failover' | 'round_robin') => invoke<void>('gateway_set_route_mode', { mode }),
  gatewayStop: () => invoke<void>('gateway_stop'),
  setJoinGateway: (name: string, join: boolean) => invoke<void>('set_join_gateway', { name, join }),
}
