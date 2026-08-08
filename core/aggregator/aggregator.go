// Package aggregator 负责模型目录聚合：注册厂商 → 拉取各厂商目录 → 合并缓存。
//
// 上层 /v1/models 与分发层都从这里取数；单厂商配置下输出与基线一致。
package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// Aggregator 聚合多个厂商的模型目录。
type Aggregator struct {
	mu      sync.RWMutex
	vendors []contract.Vendor
	catalog []contract.Model // 最近一次 Refresh 的合并结果
}

// New 构造空聚合器。
func New() *Aggregator { return &Aggregator{} }

// Register 注册一个厂商（幂等：重复注册同一 ID 会追加，由调用方保证唯一）。
func (a *Aggregator) Register(v contract.Vendor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.vendors = append(a.vendors, v)
}

// Vendors 返回已注册厂商快照。
func (a *Aggregator) Vendors() []contract.Vendor {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]contract.Vendor(nil), a.vendors...)
}

// Refresh 遍历所有已注册厂商拉取目录并合并缓存。
// 单个厂商失败不影响其它厂商（记录到 catalog 之外由调用方决定是否告警）。
func (a *Aggregator) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	vendors := a.Vendors()
	var all []contract.Model
	for _, v := range vendors {
		ms, err := v.ListModels(ctx)
		if err != nil || len(ms) == 0 {
			continue
		}
		all = append(all, ms...)
	}
	a.mu.Lock()
	a.catalog = all
	a.mu.Unlock()
	return nil
}

// Catalog 返回合并后的全部模型（含免费与非免费，按出现顺序）。
func (a *Aggregator) Catalog() []contract.Model {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]contract.Model(nil), a.catalog...)
}

// FreeModels 返回全部免费模型（Free==true）。
func (a *Aggregator) FreeModels() []contract.Model {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []contract.Model
	for _, m := range a.catalog {
		if m.Free {
			out = append(out, m)
		}
	}
	return out
}

// HasModel 判断某模型是否存在于某厂商的目录（用于"谁提供 X"的路由匹配）。
func (a *Aggregator) HasModel(provider, modelID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, m := range a.catalog {
		if m.Provider == provider && m.ID == modelID {
			return true
		}
	}
	return false
}
