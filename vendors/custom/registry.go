// 自定义模型源自注册：providers[] 一条 type:"custom" 条目 = 一个用户自定义源，
// 同类型多条即多个源（id 各异）；未配置 providers 时不会自动注册（无参数无从构造）。
package custom

import (
	"github.com/6Kmfi6HP/opencode2api/core/contract"
	"github.com/6Kmfi6HP/opencode2api/vendors/opencode"
)

// Params 键（ProviderSpec.Params；配置 providers[].params 同名透传）。
const (
	ParamBaseURL     = "base_url"
	ParamAPIKey      = "api_key"
	ParamAPIKeys     = "api_keys"     // 多 key（优先于 api_key，两者合并去重）
	ParamKeyStrategy = "key_strategy" // round_robin（默认）| failover；仅作用于本源
	ParamProtocol    = "protocol"
	ParamViaProxy    = "via_proxy"
)

func init() {
	contract.Register("custom", func(spec contract.ProviderSpec) (contract.Vendor, error) {
		cfg := Config{
			ID:          spec.ID,
			Name:        spec.Name,
			BaseURL:     strParam(spec.Params, ParamBaseURL),
			APIKey:      strParam(spec.Params, ParamAPIKey),
			APIKeys:     strSliceParam(spec.Params, ParamAPIKeys),
			KeyStrategy: strParam(spec.Params, ParamKeyStrategy),
			Protocol:    strParam(spec.Params, ParamProtocol),
			ViaProxy:    boolParam(spec.Params, ParamViaProxy),
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

// strSliceParam 读字符串数组参数（兼容 JSON 反序列化的 []any 与内存直传的 []string）。
func strSliceParam(p map[string]any, key string) []string {
	switch arr := p[key].(type) {
	case []any:
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return arr
	}
	return nil
}
