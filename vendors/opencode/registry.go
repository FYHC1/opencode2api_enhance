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
	// ParamRaceCopies 键：竞速并行数上限（int；S1 缺口修复——聚合厂商路径注入）。
	ParamRaceCopies = "_race_copies"
	// ParamRaceBudgetMS 键：竞速整体预算毫秒（int；S1 缺口修复）。
	ParamRaceBudgetMS = "_race_budget_ms"
	// ParamRacePressureLow / ParamRacePressureHigh 键：压力系数分段阈值（float64，S5）。
	ParamRacePressureLow  = "_race_pressure_low"
	ParamRacePressureHigh = "_race_pressure_high"
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
		if n, ok := spec.Params[ParamRaceCopies].(int); ok {
			cfg.RaceCopies = n
		}
		if n, ok := spec.Params[ParamRaceBudgetMS].(int); ok {
			cfg.RaceBudgetMS = n
		}
		if f, ok := spec.Params[ParamRacePressureLow].(float64); ok {
			cfg.RacePressureLow = f
		}
		if f, ok := spec.Params[ParamRacePressureHigh].(float64); ok {
			cfg.RacePressureHigh = f
		}
		return New(cfg), nil
	})
}