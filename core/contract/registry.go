// 厂商注册表：扩增供应商时，厂商在 init() 里自注册"类型 → 工厂"，
// 中间层据此自动装配全部已编译厂商 —— 无需在配置里手动声明 providers。
//
// 用法（vendors/xxx/xxx.go）：
//
//	func init() {
//	    contract.Register("myvendor", func(spec contract.ProviderSpec) (contract.Vendor, error) {
//	        // 从 spec.Params 取默认值/覆盖值，构造厂商
//	        return New(defaultCfg), nil
//	    })
//	}
//
// 配置语义（可选覆盖，缺省自动全启用）：
//   - providers 未配置         → 自动注册所有已 Register 的厂商
//   - providers[].enabled=false → 跳过该厂商
//   - providers[].id/name       → 覆盖默认 ID/展示名
//   - providers[].params        → 透传给工厂（厂商自定义参数）
package contract

import "sync"

// ProviderSpec 中间层传给厂商工厂的装配参数。
// Params 为任意厂商自定义键值（由厂商自行解释；core 不透传语义）。
type ProviderSpec struct {
	Type   string         `json:"type"`   // 厂商类型（对应 Register 的 typeName）
	ID     string         `json:"id"`     // 实例厂商标识（空 = 默认）
	Name   string         `json:"name"`   // 展示名（空 = 默认）
	Enabled bool          `json:"enabled"` // false 显式禁用（缺省自动启用）
	Params map[string]any `json:"params,omitempty"` // 厂商自定义参数
}

// Factory 厂商工厂：按 spec 构造一个 Vendor 实例。
type Factory func(spec ProviderSpec) (Vendor, error)

// 注册表（进程级单例；厂商 init() 注册，中间层装配时读取）。
var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register 注册一个厂商类型工厂。typeName 与配置 providers[].type 一致。
// 通常由厂商包 init() 调用；重复注册后到为准（便于测试覆写）。
func Register(typeName string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[typeName] = f
}

// RegisteredTypes 返回全部已注册的厂商类型名（自动装配用）。
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	return out
}

// Create 按类型构造厂商实例。未注册的类型返回错误。
func Create(typeName string, spec ProviderSpec) (Vendor, error) {
	registryMu.RLock()
	f, ok := registry[typeName]
	registryMu.RUnlock()
	if !ok {
		return nil, &UnknownVendorError{Type: typeName}
	}
	return f(spec)
}

// HasType 判断类型是否已注册。
func HasType(typeName string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[typeName]
	return ok
}

// UnknownVendorError 未知厂商类型错误。
type UnknownVendorError struct{ Type string }

func (e *UnknownVendorError) Error() string { return "unknown vendor type: " + e.Type }