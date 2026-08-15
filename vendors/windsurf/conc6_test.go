// CONC-6 并发安全测试：账号借出互斥（H5）、预注册防抖（L4）、
// midstream Close 不被锁内网络 IO 饿死。
package windsurf

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// countingRegistrar 线程安全注册计数（fakeRegistrar 无锁，不便并发断言）。
type countingRegistrar struct {
	mu    sync.Mutex
	calls int
}

func (c *countingRegistrar) Register(_ context.Context, mb MailboxProvider) (*RegisterResult, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	addr, err := mb.Create(context.Background())
	if err != nil {
		return nil, err
	}
	return &RegisterResult{Email: addr, SessionToken: "tok-" + addr}, nil
}

// TestConcurrentAcquireNoReuse 账号池并发借出不重号：
// 池 2 账号、20 并发 acquire（不 release）→ 恰好 2 个成功且账号互不相同，
// 其余全部 ErrNoAccount；释放后恢复可借。
func TestConcurrentAcquireNoReuse(t *testing.T) {
	p := newPool(time.Hour, "")
	p.add(&Account{Email: "a@t", QuotaDaily: 100, QuotaWeekly: 100})
	p.add(&Account{Email: "b@t", QuotaDaily: 100, QuotaWeekly: 100})

	const n = 20
	var (
		mu       sync.Mutex
		got      []string
		errCount int
		wg       sync.WaitGroup
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := p.acquire(time.Now())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, ErrNoAccount) {
					errCount++
					return
				}
				t.Errorf("acquire unexpected err: %v", err)
				return
			}
			got = append(got, a.Email)
		}()
	}
	wg.Wait()

	if len(got) != 2 {
		t.Fatalf("want exactly 2 granted accounts, got %v", got)
	}
	if got[0] == got[1] {
		t.Fatalf("same account granted twice concurrently: %v", got)
	}
	if errCount != n-2 {
		t.Fatalf("want %d ErrNoAccount, got %d", n-2, errCount)
	}

	// 释放后恢复可借
	p.release(got[0], time.Now(), false)
	p.release(got[1], time.Now(), false)
	if a, err := p.acquire(time.Now()); err != nil || a.Email == "" {
		t.Fatalf("acquire after release failed: %v %v", a, err)
	}
}

// TestPreRegisterIfLowDebounceOnce 并发触发预注册只 spawn 一次注册：
// 20 并发 preRegisterIfLow（池额度低于阈值）→ 注册计数 == 1。
func TestPreRegisterIfLowDebounceOnce(t *testing.T) {
	v := New(Config{
		MinAvailable:   1,
		Cooldown:       time.Hour,
		HTTPClient:     http.DefaultClient,
		QuotaThreshold: 100, // 100（含）以内合法；quotaMin(100) > 100 为 false 才进入预注册
	})
	v.pool.add(&Account{Email: "acc@t", QuotaDaily: 100, QuotaWeekly: 100})
	reg := &countingRegistrar{}
	v.cfg.Registrar = reg
	v.cfg.Mailbox = &fakeMailbox{}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.preRegisterIfLow()
		}()
	}
	wg.Wait()

	// 预注册是异步 goroutine，轮询等待完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reg.mu.Lock()
		calls := reg.calls
		reg.mu.Unlock()
		if calls >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.calls != 1 {
		t.Fatalf("want exactly 1 registration, got %d", reg.calls)
	}
}

// TestMidStreamCloseNotBlockedByNetworkRead Read 阻塞于网络读时 Close 不被饿死：
// 阻塞源（io.Pipe 无数据）下启动 Read，随后 Close 必须在短时限内返回。
// 修复前 Read 全程持 m.mu，Close 会卡在锁上等网络读返回（本测试将超时）。
func TestMidStreamCloseNotBlockedByNetworkRead(t *testing.T) {
	v := newTestVendor()
	pr, _ := io.Pipe()
	m := newMidStreamSwitch(v, context.Background(), chatMsg(), pr, "acc1@t")

	readDone := make(chan struct{})
	go func() {
		_, _ = m.Read(make([]byte, 64))
		close(readDone)
	}()
	time.Sleep(100 * time.Millisecond) // 让 Read 进入阻塞读

	start := time.Now()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close blocked behind network read for %v", d)
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Read goroutine did not exit after Close")
	}
}
