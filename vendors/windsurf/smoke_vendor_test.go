// 池级全链路冒烟：无号自动注册 → 借号 → 真实对话（P3 验收项 #13 完整版）。
// 与真实用户调用完全相同：EnsureReady（池空→自动注册）→ Vendor.Chat（借号→上游→还号）。
// 运行：SMOKE_REAL=1 go test -run TestSmokeVendorChat -v ./vendors/windsurf/ -timeout 8m
package windsurf

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

func TestVendorChat(t *testing.T) {
	if os.Getenv("SMOKE_REAL") != "1" {
		t.Skip("设置 SMOKE_REAL=1 才执行真实 Vendor 冒烟")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	t.Log("== 装配 windsurf Vendor（真实 Registrar/Mailbox/Chatter）==")
	v := New(Config{
		HTTPClient:     &http.Client{Timeout: 90 * time.Second},
		MinAvailable:   1,
		QuotaThreshold: 20,
		Cooldown:       24 * time.Hour,
		Mailbox:        NewTMailyMailbox(&http.Client{Timeout: 90 * time.Second}),
		Registrar:      NewDevinRegistrar(&http.Client{Timeout: 90 * time.Second}),
		Chatter:        NewConnectChatter(&http.Client{Timeout: 90 * time.Second}),
	})

	// == EnsureReady：池空 → 自动注册 ==
	t.Log("== EnsureReady（池空 → 自动注册）==")
	if err := v.EnsureReady(ctx); err != nil {
		t.Fatalf("EnsureReady 失败: %v", err)
	}
	t.Log("   自动注册完成（池中已有可用账号）")

	// == Chat：借号 → 真实对话（swe-1-6-slow）==
	t.Log("== Chat（借号 → 真实对话）==")
	reply, err := v.Chat(ctx, &contract.Message{
		Model:    "swe-1-6-slow",
		Messages: []contract.Msg{{Role: "user", Content: "Reply with the single word OK"}},
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	t.Logf("   Status=%d Body=%q", reply.Status, truncateStr(string(reply.Body), 200))
	if reply.Status < 200 || reply.Status >= 300 {
		t.Fatalf("Chat 非 2xx: %d", reply.Status)
	}
	if strings.TrimSpace(string(reply.Body)) == "" {
		t.Log("   注意：回复为空但 2xx（可能是空 token 用量），需看上游语义")
	}
	t.Log("== Vendor 全链路冒烟通过（无号自动注册 → 真实对话）==")
}