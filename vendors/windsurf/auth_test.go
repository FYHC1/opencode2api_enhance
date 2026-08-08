package windsurf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTMailyCreateAndWaitCode(t *testing.T) {
	oldBase := tmailyBase
	t.Cleanup(func() { tmailyBase = oldBase })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/domains"):
			fmt.Fprint(w, `{"domains":["hqpdf.com"]}`)
		case strings.HasPrefix(r.URL.Path, "/generate"):
			fmt.Fprint(w, `{"address":"wsfabc123@hqpdf.com"}`)
		case strings.HasPrefix(r.URL.Path, "/emails"):
			fmt.Fprint(w, `[{"subject":"Devin code","from":"devin@cognition.ai","text":"your code 654321","html":""}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tmailyBase = srv.URL

	mb := NewTMailyMailbox(srv.Client())
	addr, err := mb.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if addr != "wsfabc123@hqpdf.com" {
		t.Fatalf("addr = %q", addr)
	}
	code, err := mb.WaitCode(context.Background(), addr, "devin|cognition", 3*time.Second)
	if err != nil {
		t.Fatalf("WaitCode: %v", err)
	}
	if code != "654321" {
		t.Fatalf("code = %q, want 654321", code)
	}
}

// fakeMailboxForRegistrar 让注册链测试用固定邮箱+固定验证码。
type fakeMailboxForRegistrar struct{}

func (f *fakeMailboxForRegistrar) Create(_ context.Context) (string, error) {
	return "newuser@hqpdf.com", nil
}

func (f *fakeMailboxForRegistrar) WaitCode(_ context.Context, _ string, _ string, _ time.Duration) (string, error) {
	return "123456", nil
}

func TestDevinRegistrarChain(t *testing.T) {
	oldD, oldC := devinBase, codeiumBase
	t.Cleanup(func() { devinBase, codeiumBase = oldD, oldC })

	var sessionSent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/auth1/connections":
			fmt.Fprint(w, `{"ok":true}`)
		case r.URL.Path == "/api/auth1/email/start":
			fmt.Fprint(w, `{"email_verification_token":"vt-1"}`)
		case r.URL.Path == "/api/auth1/email/complete":
			fmt.Fprint(w, `{"token":"auth1-x","user_id":"u1","status":"ok"}`)
		case r.URL.Path == "/api/users/post-auth":
			fmt.Fprint(w, `{"org_id":"org-1","org_name":"newuser"}`)
		case r.URL.Path == "/api/users/set-attachment-cookie":
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/api/auth/windsurf/eligible-organizations":
			fmt.Fprint(w, `[{"plan_slug":"Free"}]`)
		case r.URL.Path == "/api/auth/windsurf/legacy-enterprise-check":
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/api/auth/windsurf/continue":
			fmt.Fprint(w, `{"code":"dc-1"}`)
		case r.URL.Path == "/exa.seat_management_pb.SeatManagementService/ExchangeDevinCode":
			// protobuf: field1 (wire2) = "devin-session-token$abc"（23 字节）
			sessionSent = true
			tok := []byte("devin-session-token$abc")
			body := []byte{0x0A, byte(len(tok))}
			body = append(body, tok...)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	devinBase = srv.URL
	codeiumBase = srv.URL

	reg := NewDevinRegistrar(srv.Client())
	res, err := reg.Register(context.Background(), &fakeMailboxForRegistrar{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res.Email != "newuser@hqpdf.com" {
		t.Fatalf("email = %q", res.Email)
	}
	if res.SessionToken != "devin-session-token$abc" {
		t.Fatalf("session = %q", res.SessionToken)
	}
	if !sessionSent {
		t.Fatal("ExchangeDevinCode not called")
	}
}

var _ = json.Marshal
