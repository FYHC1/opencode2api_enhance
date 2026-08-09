// windsurf 厂商自注册：扩增即生效，无需配置声明。
package windsurf

import (
	"net/http"
	"os"
	"path/filepath"
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
			ID:       spec.ID,
			Name:     spec.Name,
			Cooldown: 24 * time.Hour,
		}
		// 真实接缝（P3-B 落地）：临时邮箱 / Devin 注册链 / Connect-RPC 聊天。
		// 无号自动注册 → 借号对话 → 24h 冷却，全部可用（SMOKE_REAL=1 真机验证过）。
		hc := &http.Client{Timeout: 90 * time.Second}
		cfg.HTTPClient = hc
		cfg.Mailbox = NewTMailyMailbox(&http.Client{Timeout: 90 * time.Second})
		cfg.Registrar = NewDevinRegistrar(&http.Client{Timeout: 90 * time.Second})
		cfg.Chatter = NewConnectChatter(&http.Client{Timeout: 90 * time.Second})
		// 账号库持久化：优先配置 store_file，缺省落在数据目录下（跨重启复用账号）。
		if s, ok := spec.Params[paramStoreFile].(string); ok && s != "" {
			cfg.StoreFile = s
		} else {
			cfg.StoreFile = defaultStoreFile()
		}
		if v, ok := numParam(spec.Params, paramMinAvailable); ok {
			cfg.MinAvailable = int(v)
		}
		if v, ok := numParam(spec.Params, paramQuotaThreshold); ok {
			cfg.QuotaThreshold = v
		}
		if v, ok := numParam(spec.Params, paramCooldownSeconds); ok {
			cfg.Cooldown = time.Duration(v) * time.Second
		}
		return New(cfg), nil
	})
}

// defaultStoreFile 账号库默认路径：优先 OPCODE2API_DATA_DIR（环境隔离），
// 否则用户配置目录下的 opencode2api-manager，再失败回退当前目录。
func defaultStoreFile() string {
	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		if base, err := os.UserConfigDir(); err == nil && base != "" {
			dir = filepath.Join(base, "opencode2api-manager")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "windsurf_accounts.json")
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
