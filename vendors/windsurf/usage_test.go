package windsurf

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/vendors/windsurf/connect"
)

func TestFetchUsage(t *testing.T) {
	oldC := codeiumBase
	t.Cleanup(func() { codeiumBase = oldC })

	// 构造 GetUserStatus 响应：field1 用户对象(>100B) 内含 field13 用量信封 {14:99,15:88}
	envelope := concatPB(
		connect.EncodeVarintField(14, 99),
		connect.EncodeVarintField(15, 88),
		connect.EncodeVarintField(17, 1_700_000_000),
	)
	userObj := connect.EncodeString(8, strings.Repeat("x", 120))
	userObj = append(userObj, connect.EncodeMessageField(13, envelope)...)
	body := connect.EncodeMessageField(1, userObj)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/exa.seat_management_pb.SeatManagementService/GetUserStatus" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	}))
	defer srv.Close()
	codeiumBase = srv.URL

	daily, weekly, err := fetchUsage(context.Background(), "devin-session-token$x", srv.Client())
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if daily != 99 || weekly != 88 {
		t.Fatalf("daily=%v weekly=%v, want 99/88", daily, weekly)
	}
}

func concatPB(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestBuildGetUserStatusBody(t *testing.T) {
	b := buildGetUserStatusBody("devin-session-token$x")
	if len(b) < 20 {
		t.Fatalf("body too short: %d", len(b))
	}
	_ = fmt.Sprintf
}
