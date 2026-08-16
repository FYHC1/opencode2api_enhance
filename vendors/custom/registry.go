// 自定义模型源自注册：providers[] 一条 type:"custom" 条目 = 一个用户自定义源，
// 同类型多条即多个源（id 各异）；未配置 providers 时不会自动注册（无参数无从构造）。
package custom

import (
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/vendors/opencode"
)

func init() {
	contract.Register("custom", func(spec contract.ProviderSpec) (contract.Vendor, error) {
		cfg := Config{
			ID:       spec.ID,
			Name:     spec.Name,
			BaseURL:  strParam(spec.Params, ParamBaseURL),
			APIKey:   strParam(spec.Params, ParamAPIKey),
			Protocol: strParam(spec.Params, ParamProtocol),
			ViaProxy: boolParam(spec.Params, ParamViaProxy),
		}
		if tr, ok := spec.Params[opencode.ParamTransport].(contract.Transport); ok && tr != nil {
			cfg.Transport = tr
		}
		return New(cfg)
	})
}

func strParam(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}

func boolParam(p map[string]any, key string) bool {
	b, _ := p[key].(bool)
	return b
}
