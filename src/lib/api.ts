import { invoke } from '@tauri-apps/api/core'

/**
 * API 响应基础类型
 */
export interface ApiResponse<T = unknown> {
  success: boolean
  data?: T
  error?: string
}

/**
 * 设置配置
 */
export interface Settings {
  closeToTray?: boolean
  port?: number
  host?: string
  apiKey?: string
  // 其他设置项可以根据需要添加
}

/**
 * 实例信息
 */
export interface Instance {
  id: string
  name: string
  status: 'running' | 'stopped' | 'error'
  port: number
  createdAt: string
  updatedAt: string
}

/**
 * 节点信息
 */
export interface Node {
  id: string
  name: string
  endpoint: string
  status: 'active' | 'inactive' | 'error'
  lastCheck: string
}

/**
 * API 封装类
 */
export const api = {
  /**
   * 获取设置
   */
  async getSettings(): Promise<Settings> {
    try {
      const result = await invoke<ApiResponse<Settings>>('get_settings')
      return result.data || {}
    } catch (error) {
      console.error('Failed to get settings:', error)
      return {}
    }
  },

  /**
   * 保存设置
   */
  async saveSettings(settings: Settings): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('save_settings', { settings })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to save settings:', error)
      return false
    }
  },

  /**
   * 获取实例列表
   */
  async getInstances(): Promise<Instance[]> {
    try {
      const result = await invoke<ApiResponse<Instance[]>>('get_instances')
      return result.data || []
    } catch (error) {
      console.error('Failed to get instances:', error)
      return []
    }
  },

  /**
   * 创建实例
   */
  async createInstance(name: string, port: number): Promise<Instance | null> {
    try {
      const result = await invoke<ApiResponse<Instance>>('create_instance', { name, port })
      return result.data || null
    } catch (error) {
      console.error('Failed to create instance:', error)
      return null
    }
  },

  /**
   * 删除实例
   */
  async deleteInstance(id: string): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('delete_instance', { id })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to delete instance:', error)
      return false
    }
  },

  /**
   * 启动实例
   */
  async startInstance(id: string): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('start_instance', { id })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to start instance:', error)
      return false
    }
  },

  /**
   * 停止实例
   */
  async stopInstance(id: string): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('stop_instance', { id })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to stop instance:', error)
      return false
    }
  },

  /**
   * 获取节点列表
   */
  async getNodes(): Promise<Node[]> {
    try {
      const result = await invoke<ApiResponse<Node[]>>('get_nodes')
      return result.data || []
    } catch (error) {
      console.error('Failed to get nodes:', error)
      return []
    }
  },

  /**
   * 添加节点
   */
  async addNode(name: string, endpoint: string): Promise<Node | null> {
    try {
      const result = await invoke<ApiResponse<Node>>('add_node', { name, endpoint })
      return result.data || null
    } catch (error) {
      console.error('Failed to add node:', error)
      return null
    }
  },

  /**
   * 删除节点
   */
  async deleteNode(id: string): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('delete_node', { id })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to delete node:', error)
      return false
    }
  },

  /**
   * 测试节点连接
   */
  async testNode(id: string): Promise<boolean> {
    try {
      const result = await invoke<ApiResponse<boolean>>('test_node', { id })
      return result.success && result.data === true
    } catch (error) {
      console.error('Failed to test node:', error)
      return false
    }
  },
}