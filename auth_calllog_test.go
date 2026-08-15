// U5-③：鉴权失败（401）需落盘一条调用日志（含 Path/原因/ReqID），成功路径由业务 handler 记录。
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIKey401RecordsCallLog(t *testing.T) {
	dir := setupCallLogDir(t)
	const key = "sk-401logkey0123456789"
	oldPassword := adminPassword
	adminPassword = key
	t.Cleanup(func() { adminPassword = oldPassword })

	call := func(auth, xapi string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if xapi != "" {
			req.Header.Set("x-api-key", xapi)
		}
		rec := httptest.NewRecorder()
		apiKeyAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})(rec, req)
		return rec
	}

	rec := call("", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: status=%d, want 401", rec.Code)
	}
	rec = call("", "sk-wrongkey0123456789")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status=%d, want 401", rec.Code)
	}
	// 正确 x-api-key 放行，且不产生 401 日志（成功路径由业务 handler recordCall）
	rec = call("", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct x-api-key: status=%d, want 200", rec.Code)
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2（两条 401 各记一条）", len(recs))
	}
	for _, r := range recs {
		if r.Path != "/v1/messages" || r.Status != "fail" {
			t.Fatalf("record = %+v", r)
		}
		if !strings.HasPrefix(r.ErrMsg, "鉴权失败：") {
			t.Fatalf("err_msg = %q, want 鉴权失败 前缀", r.ErrMsg)
		}
		if r.ReqID == "" || r.TS == "" {
			t.Fatalf("record missing req_id/ts: %+v", r)
		}
		if !hasCallEvent(r, "auth_failed") {
			t.Fatalf("record events = %+v, want auth_failed", r.Events)
		}
	}
	// 原因区分：缺头 vs key 不匹配
	if !strings.Contains(recs[0].ErrMsg, "缺少") {
		t.Fatalf("missing-header record err_msg = %q", recs[0].ErrMsg)
	}
	if !strings.Contains(recs[1].ErrMsg, "key 不匹配") {
		t.Fatalf("wrong-key record err_msg = %q", recs[1].ErrMsg)
	}
}

// TestAPIKey401RecordsCallLogDisabled 未启用调用日志（callLogEnabled=false）时不落盘。
func TestAPIKey401CallLogDisabled(t *testing.T) {
	dir := t.TempDir()
	oldPath, oldEnabled, oldLog := callLogPath, callLogEnabled, callLog
	oldLog.Stop() // 停掉上一测试残留的后台写者
	callLogPath = filepath.Join(dir, "call_log.jsonl")
	callLogEnabled = false
	initCallLog()
	t.Cleanup(func() {
		callLog.Stop()
		callLogPath = oldPath
		callLogEnabled = oldEnabled
		callLog = oldLog
	})

	const key = "sk-offlogkey0123456789"
	oldPassword := adminPassword
	adminPassword = key
	t.Cleanup(func() { adminPassword = oldPassword })

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	apiKeyAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "call_log.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("call_log.jsonl should not be written when callLogEnabled=false")
	}
}
