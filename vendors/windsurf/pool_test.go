package windsurf

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// ---------------------------------------------------------------- fakes

type fakeMailbox struct {
	seq int
}

func (f *fakeMailbox) Create(_ context.Context) (string, error) {
	f.seq++
	return "wsf" + strings.Repeat("0", 6) + string(rune('a'+f.seq%26)) + "@mail.test", nil
}

func (f *fakeMailbox) WaitCode(_ context.Context, _ string, _ string, _ time.Duration) (string, error) {
	return "123456", nil
}

type fakeRegistrar struct {
	calls int
	err   error
}

func (f *fakeRegistrar) Register(_ context.Context, mb MailboxProvider) (*RegisterResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls++
	addr, err := mb.Create(context.Background())
	if err != nil {
		return nil, err
	}
	return &RegisterResult{Email: addr, SessionToken: "tok-" + addr}, nil
}

type fakeChatter struct {
	// perToken 记录每个会话令牌的行为序列（每项一次调用）。
	perToken map[string][]func(token string) (*contract.Reply, error)
	mu       sync.Mutex
}

func (c *fakeChatter) reply(token string) (*contract.Reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.perToken[token]
	if len(list) == 0 {
		return &contract.Reply{Status: http.StatusTooManyRequests}, nil
	}
	fn := list[0]
	c.perToken[token] = list[1:]
	return fn(token)
}

func (c *fakeChatter) DoChat(_ context.Context, token string, _ *contract.Message) (*contract.Reply, error) {
	return c.reply(token)
}

func (c *fakeChatter) DoChatStream(_ context.Context, _ string, _ *contract.Message) (*contract.Stream, error) {
	return nil, ErrNotWiredChat
}

// ---- tests

func TestPoolCoolingAfterRelease(t *testing.T) {
	p := newPool(24*time.Hour, "")
	p.add(&Account{Email: "a@t", QuotaDaily: 100, QuotaWeekly: 100})
	now := time.Now()

	a, err := p.acquire(now)
	if err != nil || a.Email != "a@t" {
		t.Fatalf("acquire: %v, %v", a, err)
	}
	p.release("a@t", now, true) // dry + cooldown
	if _, err := p.acquire(now.Add(time.Hour)); err != ErrNoAccount {
		t.Fatalf("after release should be cooling: %v", err)
	}
	// 24h 冷却后恢复（Dry 保持 → still 不可用）；冷却+额度恢复才可用
	p.updateUsage("a@t", 80, 80)
	if a2, err := p.acquire(now.Add(25 * time.Hour)); err != nil || a2.Email != "a@t" {
		t.Fatalf("after cooldown+quota restore should be usable: %v %v", a2, err)
	}
}

func TestEnsureReadyRegistersOnDemand(t *testing.T) {
	v := New(Config{
		Mailbox: &fakeMailbox{}, Registrar: &fakeRegistrar{},
		MinAvailable: 2, Cooldown: time.Hour, HTTPClient: http.DefaultClient,
	})
	if err := v.EnsureReady(context.Background()); err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	// 池空路径：EnsureReady 同步注册 1 个恢复服务，差额由后台补齐 → 轮询等待满额。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v.pool.status(time.Now()).Available >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := v.pool.status(time.Now()).Available; got < 2 {
		t.Fatalf("available = %d, want >= 2（后台补齐未生效）", got)
	}
}

func TestEnsureReadyWithoutRegistrar(t *testing.T) {
	v := New(Config{MinAvailable: 2, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	if err := v.EnsureReady(context.Background()); err != ErrUnavailable {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestChatSwitchesAccountOnQuotaError(t *testing.T) {
	v := New(Config{MinAvailable: 2, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	// 先放两个带会话令牌的账号进池（跳过注册链）
	v.pool.add(&Account{Email: "acc1@t", WindsurfSessionToken: "tok-1", QuotaDaily: 100, QuotaWeekly: 100})
	v.pool.add(&Account{Email: "acc2@t", WindsurfSessionToken: "tok-2", QuotaDaily: 100, QuotaWeekly: 100})

	chat := &fakeChatter{perToken: map[string][]func(string) (*contract.Reply, error){
		// tok-1 首次即触发额度/限流 → 适配层换 tok-2 成功
		"tok-1": {func(string) (*contract.Reply, error) { return &contract.Reply{Status: 429}, nil }},
		"tok-2": {func(string) (*contract.Reply, error) { return &contract.Reply{Body: []byte("ok"), Status: 200}, nil }},
	}}
	v.cfg.Chatter = chat

	reply, err := v.Chat(context.Background(), &contract.Message{Model: "swe-1-6-slow"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != 200 || string(reply.Body) != "ok" {
		t.Fatalf("reply = %+v, want ok/200", reply)
	}
	// tok-1 被标记 (Dry+cooling)，tok-2 成功保留
	st := v.PoolStatus()
	if st.Available != 1 || st.Cooling != 1 {
		t.Fatalf("pool status = %+v, want 1 available / 1 cooling", st)
	}
}

func TestChatWithoutTokenFails(t *testing.T) {
	v := New(Config{MinAvailable: 1, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	v.pool.add(&Account{Email: "notok@t", QuotaDaily: 100, QuotaWeekly: 100}) // 无 token
	v.cfg.Chatter = &fakeChatter{}
	if _, err := v.Chat(context.Background(), &contract.Message{}); err == nil {
		t.Fatal("want error for account without session token")
	}
}

func TestChatReturnsUpstreamErrorWhenSwitchNotPossible(t *testing.T) {
	v := New(Config{MinAvailable: 1, Cooldown: time.Hour, HTTPClient: http.DefaultClient})
	v.pool.add(&Account{Email: "only@t", WindsurfSessionToken: "tok-a", QuotaDaily: 100, QuotaWeekly: 100})
	v.cfg.Chatter = &fakeChatter{perToken: map[string][]func(string) (*contract.Reply, error){
		"tok-a": {func(string) (*contract.Reply, error) { return &contract.Reply{Status: 403}, nil }},
	}}
	reply, err := v.Chat(context.Background(), &contract.Message{})
	if err == nil {
		t.Fatal("want error for exhausted single account", err)
	}
	_ = reply
}

func TestPoolUsageRestoresAfterRefresh(t *testing.T) {
	p := newPool(24*time.Hour, "")
	p.add(&Account{Email: "x@t", QuotaDaily: 0, QuotaWeekly: 0, Dry: true})
	if st := p.status(time.Now()); st.Available != 0 {
		t.Fatalf("dry account must be unavailable: %+v", st)
	}
	p.updateUsage("x@t", 60, 50)
	if st := p.status(time.Now()); st.Available != 1 {
		t.Fatalf("after usage refresh account must be usable: %+v", st)
	}
}
