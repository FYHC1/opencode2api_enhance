// 管理器核心：实例注册表（instances.json 持久化，Rust instance.rs 移植）。
// P4-1 先落地注册表 + 状态类型；生命周期（spawn/stop）在 P4-2。
package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

// InstanceStatus 实例状态（外部标签形态，与 Rust serde 一致）：
// "Stopped" | "Starting" | "Running" | "Stopping" | {"Error":["msg"]}。
type InstanceStatus struct {
	State string   `json:"-"` // Stopped/Starting/Running/Stopping
	Error []string `json:"-"` // 非空表示 Error 态
}

// MarshalJSON 编码为外部标签形态。
func (s InstanceStatus) MarshalJSON() ([]byte, error) {
	if s.State == "" && len(s.Error) == 0 {
		return json.Marshal("Stopped")
	}
	if len(s.Error) > 0 {
		return json.Marshal(map[string][]string{"Error": s.Error})
	}
	return json.Marshal(s.State)
}

// UnmarshalJSON 兼容 "Stopped" 与 {"Error":[...]} 两种形态。
func (s *InstanceStatus) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		s.State = str
		s.Error = nil
		return nil
	}
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err == nil {
		if errs, ok := m["Error"]; ok {
			s.State = "Error"
			s.Error = errs
			return nil
		}
	}
	return errors.New("invalid instance status")
}

// Status helpers。
func StatusStopped() InstanceStatus  { return InstanceStatus{State: "Stopped"} }
func StatusStarting() InstanceStatus { return InstanceStatus{State: "Starting"} }
func StatusRunning() InstanceStatus  { return InstanceStatus{State: "Running"} }
func StatusStopping() InstanceStatus { return InstanceStatus{State: "Stopping"} }
func StatusError(msg string) InstanceStatus {
	return InstanceStatus{State: "Error", Error: []string{msg}}
}

// Instance 单实例契约（JSON 字段与前端/Rust 一致）。
type Instance struct {
	Name        string         `json:"name"`
	Port        uint16         `json:"port"`
	Node        string         `json:"node"`
	Password    string         `json:"password"`
	IP          string         `json:"ip"`
	SingboxPort uint16         `json:"singbox_port"`
	PID         *int           `json:"pid,omitempty"`
	SingboxPID  *int           `json:"singbox_pid,omitempty"`
	JoinGateway bool           `json:"join_gateway"`
	Status      InstanceStatus `json:"status"`
}

// Manager 持有管理器状态（数据目录 + 运行目录 + 注册表）。
type Manager struct {
	paths Paths
	mu    sync.Mutex

	seamsMu sync.Mutex
	seamsFn *SeamFuncs // 可插拔接缝（P4-3 填充；nil = 未装配）
}

// New 创建管理器。
func New(dataDir string) *Manager {
	return &Manager{paths: ResolvePaths(dataDir)}
}

// Paths 暴露路径集合（供探针/网关等子模块复用）。
func (m *Manager) Paths() Paths { return m.paths }

// ListInstances 返回实例列表（按名称排序）。
func (m *Manager) ListInstances() []Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// FindInstance 按名称查实例。
func (m *Manager) FindInstance(name string) (Instance, bool) {
	for _, inst := range m.ListInstances() {
		if inst.Name == name {
			return inst, true
		}
	}
	return Instance{}, false
}

// AddInstance 添加实例（重名/端口冲突拒绝）。
func (m *Manager) AddInstance(inst Instance) error {
	if inst.Name == "" {
		return errors.New("实例名不能为空")
	}
	if inst.Port < 1024 {
		return errors.New("端口必须 ≥ 1024")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for _, e := range list {
		if e.Name == inst.Name {
			return errors.New("实例名已存在: " + inst.Name)
		}
		if e.Port == inst.Port {
			return errors.New("端口已被实例占用: " + itoa(inst.Port))
		}
	}
	inst.Status = StatusStopped()
	list = append(list, inst)
	return m.save(list)
}

// RemoveInstance 删除实例（生命周期归零；运行中由 P4-2 先停止）。
func (m *Manager) RemoveInstance(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for i, e := range list {
		if e.Name == name {
			list = append(list[:i], list[i+1:]...)
			return m.save(list)
		}
	}
	return nil
}

// UpdateInstance 就地更新实例并持久化（供状态机流转）。
func (m *Manager) UpdateInstance(inst Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for i, e := range list {
		if e.Name == inst.Name {
			list[i] = inst
			return m.save(list)
		}
	}
	return errors.New("实例不存在: " + inst.Name)
}

// load 读取注册表（锁内调用）。
func (m *Manager) load() []Instance {
	data, err := readFile(m.paths.Instances)
	if err != nil {
		return []Instance{}
	}
	var list []Instance
	if err := json.Unmarshal(data, &list); err != nil {
		return []Instance{}
	}
	if list == nil {
		return []Instance{}
	}
	return list
}

// save 持久化注册表（锁内调用）。
func (m *Manager) save(list []Instance) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeFileMkdir(m.paths.Instances, data)
}

// itoa 小工具。
func itoa(v uint16) string {
	return strconv.FormatUint(uint64(v), 10)
}

// 小工具：文件读写（目录自动创建）。
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeFileMkdir(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
