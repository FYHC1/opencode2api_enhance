// opencode 厂商自注册：扩增即生效，无需配置声明。
package opencode

import (
	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// ConfigKey 在 ProviderSpec.Params 中读取运行时装配参数的键。
const (
	// ParamTransport 键：contract.Transport（core 注入的 HTTP 传输，含代理池）。
	ParamTransport = "_transport"
	// ParamAdminPassword 键：本地门禁密钥（public 判定用）。
	ParamAdminPassword = "_admin_password"
)

func init() {
	contract.Register("opencode", func(spec contract.ProviderSpec) (contract.Vendor, error) {
		cfg := Config{
			ID:   spec.ID,
			Name: spec.Name,
		}
		if tr, ok := spec.Params[ParamTransport].(contract.Transport); ok && tr != nil {
			cfg.Transport = tr
		}
		if pw, ok := spec.Params[ParamAdminPassword].(string); ok {
			cfg.AdminPassword = pw
		}
		return New(cfg), nil
	})
}