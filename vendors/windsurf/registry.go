// windsurf 厂商自注册：扩增即生效，无需配置声明。
package windsurf

import (
	"net/http"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// Params 键（ProvderSpec.Params；配置 providers[].params 可覆盖默认值）。
const (
	paramMinAvailable    = "min_available"
	paramQuotaThreshold  = "quota_threshold"
	paramCooldownSeconds = "cooldown_seconds"
	paramStoreFile       = "store_file"
)

func init() {
	contract.Register("windsurf", func(spec contract.ProviderSpec) (contract.Vendor, error) {
		cfg := Config{
			ID:     spec.ID,
			Name:   spec.Name,
			Cooldown: 24 * time.Hour,
		}
		cfg.HTTPClient = http.DefaultClient
		if v, ok := numParam(spec.Params, paramMinAvailable); ok {
			cfg.MinAvailable = int(v)
		}
		if v, ok := numParam(spec.Params, paramQuotaThreshold); ok {
			cfg.QuotaThreshold = v
		}
		if v, ok := numParam(spec.Params, paramCooldownSeconds); ok {
			cfg.Cooldown = time.Duration(v) * time.Second
		}
		if s, ok := spec.Params[paramStoreFile].(string); ok && s != "" {
			cfg.StoreFile = s
		}
		return New(cfg), nil
	})
}

// numParam 取数字参数（int/float64）。
func numParam(p map[string]any, key string) (float64, bool) {
	switch v := p[key].(type) {
	case int:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}