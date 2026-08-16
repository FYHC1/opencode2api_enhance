// 自定义模型源目录磁盘缓存（stale-while-revalidate）：
// ListModels 成功时把模型清单写入 <dataDir>/custom_models/<id>.json；
// 拉取失败（重启后网络未就绪/上游抖动）时按 内存 → 磁盘 顺序兜底，
// 保证进程重启后 /v1/models 首个请求即带上自定义模型，后台刷新再修正。
// 同环境多进程（面板/各实例）共享 dataDir，互为缓存（原子写，后写覆盖无害）。
package custom

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// cacheDir 返回缓存目录：优先 OPCODE2API_DATA_DIR（壳按环境注入，实例子进程继承），
// 否则用户配置目录下 opencode2api-manager（与 windsurf 账号库同规则）。
func cacheDir() string {
	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		if base, err := os.UserConfigDir(); err == nil && base != "" {
			dir = filepath.Join(base, "opencode2api-manager")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "custom_models")
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// cachePath 本源缓存文件路径（id 已受保存端校验约束，仍兜底替换不安全字符）。
func (v *Vendor) cachePath() string {
	id := unsafeFilename.ReplaceAllString(v.cfg.ID, "_")
	if id == "" {
		id = "default"
	}
	return filepath.Join(cacheDir(), id+".json")
}

// modelsCacheFile 磁盘缓存结构。
type modelsCacheFile struct {
	SavedAt time.Time        `json:"saved_at"`
	Models  []contract.Model `json:"models"`
}

// saveModelsCache 原子写缓存（tmp + rename，读侧不会读到半截文件）。尽力而为，失败仅告警。
func (v *Vendor) saveModelsCache(models []contract.Model) {
	path := v.cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(modelsCacheFile{SavedAt: time.Now(), Models: models})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Debug("custom: rename models cache failed", "id", v.cfg.ID, "error", err)
		_ = os.Remove(tmp)
	}
}

// loadModelsCache 读磁盘缓存（损坏/缺失返回 nil）。
func (v *Vendor) loadModelsCache() []contract.Model {
	data, err := os.ReadFile(v.cachePath())
	if err != nil {
		return nil
	}
	var f modelsCacheFile
	if json.Unmarshal(data, &f) != nil || len(f.Models) == 0 {
		return nil
	}
	out := make([]contract.Model, 0, len(f.Models))
	for _, m := range f.Models {
		// 防手工篡改：缓存必须仍是本源前缀命名。
		if !strings.HasPrefix(m.ID, v.prefix()) || m.Provider != v.cfg.ID {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PurgeAllModelCaches 删除全部自定义源的目录磁盘缓存（「清空全部自定义源」时一并清理，
// 防止同 id 重建源时读到旧清单）。
func PurgeAllModelCaches() error {
	return os.RemoveAll(cacheDir())
}
