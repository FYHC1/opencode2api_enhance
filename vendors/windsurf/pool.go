package windsurf

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// Account 是账号池中的一个账号。
type Account struct {
	Email       string    `json:"email"`
	QuotaDaily  float64   `json:"quota_daily_pct"`  // 0..100；-1 未知
	QuotaWeekly float64   `json:"quota_weekly_pct"` // 0..100；-1 未知
	Dry         bool      `json:"dry"`              // 额度耗尽标记
	Trouble     int       `json:"trouble"`          // 近期故障计数（滚动窗口）
	CoolUntil   time.Time `json:"cool_until"`       // 冷却截止（防滥用/换号后）
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

// quotaPct 取该账号"实际剩余额度%"（daily/weekly 取小；未知视为 100 乐观值）。
func (a *Account) quotaPct() float64 {
	if a.Dry {
		return 0
	}
	m := 100.0
	if a.QuotaDaily >= 0 && a.QuotaDaily < m {
		m = a.QuotaDaily
	}
	if a.QuotaWeekly >= 0 && a.QuotaWeekly < m {
		m = a.QuotaWeekly
	}
	return m
}

// usable 判断账号当前是否可用（未冷却、未耗尽）。
func (a *Account) usable(now time.Time) bool {
	if a.Dry {
		return false
	}
	return !a.CoolUntil.After(now)
}

// Pool 是线程安全的账号池（内存 + 可选 JSON 持久化）。
type Pool struct {
	mu       sync.Mutex
	cooldown time.Duration
	file     string
	accounts []*Account
}

func newPool(cooldown time.Duration, file string) *Pool {
	return &Pool{cooldown: cooldown, file: file, accounts: []*Account{}}
}

func (p *Pool) add(a *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.accounts {
		if e.Email == a.Email {
			*e = *a
			p.persistLocked()
			return
		}
	}
	p.accounts = append(p.accounts, a)
	p.persistLocked()
}

// available 返回当前可用账号（按健康排序：额度小者优先用）。
func (p *Pool) available(now time.Time) []*Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Account
	for _, a := range p.accounts {
		if a.usable(now) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].quotaPct(), out[j].quotaPct()
		if pi != pj {
			return pi < pj
		}
		if out[i].Trouble != out[j].Trouble {
			return out[i].Trouble < out[j].Trouble
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// acquire 借出一个可用账号（受冷却/健康约束）；无可用返回 ErrNoAccount。
func (p *Pool) acquire(now time.Time) (*Account, error) {
	avail := p.available(now)
	if len(avail) == 0 {
		return nil, ErrNoAccount
	}
	a := avail[0]
	p.mu.Lock()
	a.LastUsedAt = now
	p.persistLocked()
	p.mu.Unlock()
	return a, nil
}

// release 归还账号。exhausted=true 表示额度耗尽/故障：置 Dry + 冷却。
func (p *Pool) release(email string, now time.Time, exhausted bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Email != email {
			continue
		}
		if exhausted {
			a.Dry = true
			a.QuotaDaily = 0
			a.QuotaWeekly = 0
			a.Trouble++
			a.CoolUntil = now.Add(p.cooldown)
		}
		a.LastUsedAt = now
		p.persistLocked()
		return
	}
}

// touch 标记账号最近使用时间（成功会话后）。
func (p *Pool) touch(email string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Email == email {
			a.LastUsedAt = now
			p.persistLocked()
			return
		}
	}
}

// updateUsage 用上游用量刷新账号额度；额度恢复则解除 Dry。
func (p *Pool) updateUsage(email string, daily, weekly float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Email != email {
			continue
		}
		a.QuotaDaily = daily
		a.QuotaWeekly = weekly
		if daily > 0 || weekly > 0 {
			a.Dry = false
		}
		p.persistLocked()
		return
	}
}

// quotaMin 返回全池最低剩余额度（%）；无账号 → 0。
func (p *Pool) quotaMin() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.accounts) == 0 {
		return 0
	}
	m := 100.0
	for _, a := range p.accounts {
		if q := a.quotaPct(); q < m {
			m = q
		}
	}
	return m
}

// status 汇总池状态。
func (p *Pool) status(now time.Time) contract.PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := contract.PoolStatus{QuotaMin: 100}
	for _, a := range p.accounts {
		if a.usable(now) {
			st.Available++
		} else {
			st.Cooling++
		}
		if q := a.quotaPct(); q < st.QuotaMin {
			st.QuotaMin = q
		}
	}
	if st.Available == 0 {
		st.Drought = true
	}
	return st
}

// count 返回账号总数（供注册节流判断）。
func (p *Pool) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.accounts)
}

// persistLocked 写 JSON 持久化（需持锁）。
func (p *Pool) persistLocked() {
	if p.file == "" {
		return
	}
	data, err := json.MarshalIndent(p.accounts, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(p.file, data, 0o644)
}

func (p *Pool) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var list []*Account
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	p.mu.Lock()
	p.accounts = list
	p.mu.Unlock()
	return nil
}
