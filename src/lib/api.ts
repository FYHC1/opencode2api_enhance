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
  singbox_port: number
  pid: number | null
  singbox_pid: number | null
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
}

export type BinariesInfo = {
  bin_dir: string
  oc_exists: boolean
  sb_exists: boolean
}

// ─── Tauri command 封装 ─────────────────────────────────────────────

export const api = {
  // 节点
  listNodes: () => invoke<NodeView[]>('list_nodes'),

  // 实例
  listInstances: () => invoke<Instance[]>('list_instances'),
  addInstance: (name: string, port: number, node: string) =>
    invoke<Instance>('add_instance', { name, port, node }),
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
}
