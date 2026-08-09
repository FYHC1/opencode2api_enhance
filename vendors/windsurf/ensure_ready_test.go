package windsurf

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// slowRegistrar delays each registration to prove EnsureReady is non-blocking
// when the pool already has an available account.
type slowRegistrar struct {
	fakeRegistrar
	duration time.Duration
}

func (s *slowRegistrar) Register(ctx context.Context, mb MailboxProvider) (*RegisterResult, error) {
	select {
	case <-time.After(s.duration):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fakeRegistrar.Register(ctx, mb)
}

// TestEnsureReadyNonBlockingTopUp: with >=1 available account, EnsureReady returns
// immediately (does not wait for registration); the deficit is topped up in the
// background toward MinAvailable, so the user request is never blocked.
func TestEnsureReadyNonBlockingTopUp(t *testing.T) {
	v := New(Config{
		Mailbox:      &fakeMailbox{},
		Registrar:    &slowRegistrar{duration: 300 * time.Millisecond},
		MinAvailable: 3, Cooldown: time.Hour, HTTPClient: http.DefaultClient,
	})
	// Seed 1 available account: the serving path has an account, top-up must not block.
	v.pool.add(&Account{Email: "a1@t", WindsurfSessionToken: "tok-1", QuotaDaily: 100, QuotaWeekly: 100})

	start := time.Now()
	if err := v.EnsureReady(context.Background()); err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("EnsureReady blocked %v (must be non-blocking when accounts available)", elapsed)
	}
	if got := v.pool.status(time.Now()).Available; got < 1 {
		t.Fatalf("available = %d, want >= 1 for serving", got)
	}
	// Background top-up must eventually fill the pool to MinAvailable.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v.pool.status(time.Now()).Available >= 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := v.pool.status(time.Now()).Available
	t.Fatalf("available = %d, want >= 3 (background top-up did not complete)", got)
}