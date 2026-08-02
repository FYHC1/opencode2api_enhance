import React, { useState, useEffect } from 'react'
import { Plus, Play, Square, Trash2 } from 'lucide-react'
import { api, type Instance } from '../lib/api'

export function InstancesTab() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [loading, setLoading] = useState(false)

  // 加载实例列表
  const loadInstances = async () => {
    setLoading(true)
    try {
      const data = await api.getInstances()
      setInstances(data)
    } catch (error) {
      console.error('Failed to load instances:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadInstances()
  }, [])

  // 创建实例
  const handleCreateInstance = async () => {
    // TODO: 实现创建实例对话框
    console.log('Create instance')
  }

  // 删除实例
  const handleDeleteInstance = async (id: string) => {
    if (!confirm('确定要删除此实例吗？')) return
    
    try {
      const success = await api.deleteInstance(id)
      if (success) {
        await loadInstances()
      }
    } catch (error) {
      console.error('Failed to delete instance:', error)
    }
  }

  // 启动实例
  const handleStartInstance = async (id: string) => {
    try {
      const success = await api.startInstance(id)
      if (success) {
        await loadInstances()
      }
    } catch (error) {
      console.error('Failed to start instance:', error)
    }
  }

  // 停止实例
  const handleStopInstance = async (id: string) => {
    try {
      const success = await api.stopInstance(id)
      if (success) {
        await loadInstances()
      }
    } catch (error) {
      console.error('Failed to stop instance:', error)
    }
  }

  const getStatusColor = (status: Instance['status']) => {
    switch (status) {
      case 'running':
        return 'text-green-600 bg-green-50'
      case 'stopped':
        return 'text-gray-600 bg-gray-50'
      case 'error':
        return 'text-red-600 bg-red-50'
      default:
        return 'text-gray-600 bg-gray-50'
    }
  }

  const getStatusText = (status: Instance['status']) => {
    switch (status) {
      case 'running':
        return '运行中'
      case 'stopped':
        return '已停止'
      case 'error':
        return '错误'
      default:
        return '未知'
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* 标题栏 */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-zinc-200">
        <h2 className="text-lg font-semibold text-zinc-900">实例管理</h2>
        <button
          onClick={handleCreateInstance}
          className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 transition-colors"
        >
          <Plus size={16} />
          新建实例
        </button>
      </div>

      {/* 实例列表 */}
      <div className="flex-1 overflow-auto p-6">
        {loading ? (
          <div className="flex items-center justify-center h-full text-zinc-500">
            加载中...
          </div>
        ) : instances.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-zinc-500">
            <p className="text-lg mb-2">暂无实例</p>
            <p className="text-sm">点击"新建实例"按钮创建第一个实例</p>
          </div>
        ) : (
          <div className="space-y-3">
            {instances.map((instance) => (
              <div
                key={instance.id}
                className="flex items-center justify-between p-4 bg-white border border-zinc-200 rounded-lg hover:border-zinc-300 transition-colors"
              >
                <div className="flex-1">
                  <div className="flex items-center gap-3 mb-2">
                    <h3 className="text-base font-medium text-zinc-900">{instance.name}</h3>
                    <span
                      className={`px-2 py-0.5 text-xs font-medium rounded-full ${getStatusColor(instance.status)}`}
                    >
                      {getStatusText(instance.status)}
                    </span>
                  </div>
                  <div className="flex items-center gap-4 text-sm text-zinc-600">
                    <span>端口: {instance.port}</span>
                    <span>创建时间: {new Date(instance.createdAt).toLocaleString()}</span>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  {instance.status === 'running' ? (
                    <button
                      onClick={() => handleStopInstance(instance.id)}
                      className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-zinc-700 bg-zinc-100 rounded-md hover:bg-zinc-200 transition-colors"
                    >
                      <Square size={14} />
                      停止
                    </button>
                  ) : (
                    <button
                      onClick={() => handleStartInstance(instance.id)}
                      className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-green-600 rounded-md hover:bg-green-700 transition-colors"
                    >
                      <Play size={14} />
                      启动
                    </button>
                  )}
                  <button
                    onClick={() => handleDeleteInstance(instance.id)}
                    className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-red-600 bg-red-50 rounded-md hover:bg-red-100 transition-colors"
                  >
                    <Trash2 size={14} />
                    删除
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}