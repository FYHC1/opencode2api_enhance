package windsurf

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestGenerateSessionNotFoundCapped 验证 session_not_found 递归重试有深度上限：
// 服务端持续返回该错误时，请求总数收敛为 1+maxGenerateRecursion 并返回错误，
// 而不是无限递归 + 无限外部请求。
func TestGenerateSessionNotFoundCapped(t *testing.T) {
	oldBase := tmailyBase
	t.Cleanup(func() { tmailyBase = oldBase })

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/generate") {
			atomic.AddInt32(&calls, 1)
			fmt.Fprint(w, `{"error":"session_not_found"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	tmailyBase = srv.URL

	mb := NewTMailyMailbox(srv.Client()).(*tmailyMailbox)
	_, err := mb.generate(context.Background(), true, "hqpdf.com", "wsfabc")
	if err == nil {
		t.Fatal("持续 session_not_found 应返回错误")
	}
	if want := fmt.Sprintf("after %d retries", maxGenerateRecursion); !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want contains %q", err, want)
	}
	if got := atomic.LoadInt32(&calls); got != int32(1+maxGenerateRecursion) {
		t.Fatalf("generate 请求数 = %d, want %d", got, 1+maxGenerateRecursion)
	}
}
