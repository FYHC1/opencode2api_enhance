// S4：responses / claude 协议 handler 走查——断言 CallRecord 经 recordCall 落盘
// call_log.jsonl（httptest + 临时目录），对齐 chat_handler 三态（成功/失败）。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupCallLogDir 把调用日志重定向到临时目录并启用 JSONL 落盘。
func setupCallLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldPath, oldEnabled, oldLog := callLogPath, callLogEnabled, callLog
	callLogPath = filepath.Join(dir, "call_log.jsonl")
	callLogEnabled = true
	initCallLog()
	t.Cleanup(func() {
		callLogPath = oldPath
		callLogEnabled = oldEnabled
		callLog = oldLog
	})
	return dir
}

// readRecords 解析落盘的 call_log.jsonl（每行一条 CallRecord）。
func readRecords(t *testing.T, path string) []CallRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read call_log.jsonl: %v", err)
	}
	var recs []CallRecord
	for _, line := range splitJSONLines(data) {
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec CallRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal record %q: %v", line, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestResponsesHandlerRecordsCallLogSuccess(t *testing.T) {
	dir := setupCallLogDir(t)
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{{
		status: http.StatusOK,
		body:   `{"id":"resp_1","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"primary-model","input":"hello"}`))
	responsesHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "ok" || r.Path != "/v1/responses" || r.Model != "primary-model" || r.Stream {
		t.Fatalf("record = %+v", r)
	}
	if r.ReqID == "" || r.TS == "" || r.RouteMode == "" {
		t.Fatalf("record missing req_id/ts/route_mode: %+v", r)
	}
	if len(r.Nodes) != 1 {
		t.Fatalf("nodes = %+v, want 1 entry", r.Nodes)
	}
	if !hasCallEvent(r, "complete") {
		t.Fatalf("record events = %+v, want complete", r.Events)
	}
	if r.PromptTok != 7 || r.CompletionTok != 3 {
		t.Fatalf("tokens p=%d c=%d, want 7/3", r.PromptTok, r.CompletionTok)
	}
}

func TestResponsesHandlerRecordsCallLogUpstream429(t *testing.T) {
	dir := setupCallLogDir(t)
	// 429 属 opencode Retryable，耗尽 maxRetries=3 次重试后透传 429。
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"primary-model","input":"hello"}`))
	responsesHandler(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "fail" || !strings.Contains(r.ErrMsg, "429") {
		t.Fatalf("record = %+v, want fail with 429", r)
	}
	if !hasCallEvent(r, "upstream_error") {
		t.Fatalf("record events = %+v, want upstream_error", r.Events)
	}
}

func TestResponsesHandlerRecordsCallLogStream(t *testing.T) {
	dir := setupCallLogDir(t)
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{{
		status: http.StatusOK,
		body: strings.Join([]string{
			`data: {"id":"resp_1","created":123,"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"primary-model","input":"hello","stream":true}`))
	responsesHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "ok" || !r.Stream || r.Path != "/v1/responses" {
		t.Fatalf("record = %+v", r)
	}
	if !hasCallEvent(r, "connect_ok") || !hasCallEvent(r, "complete") {
		t.Fatalf("record events = %+v, want connect_ok+complete", r.Events)
	}
}

func TestClaudeMessagesHandlerRecordsCallLogSuccess(t *testing.T) {
	dir := setupCallLogDir(t)
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{{
		status: http.StatusOK,
		body:   `{"id":"chatcmpl_test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":35,"total_tokens":155}}`,
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"primary-model","messages":[]}`))
	claudeMessagesHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "ok" || r.Path != "/v1/messages" || r.Model != "primary-model" || r.Stream {
		t.Fatalf("record = %+v", r)
	}
	if r.PromptTok != 120 || r.CompletionTok != 35 {
		t.Fatalf("tokens p=%d c=%d, want 120/35", r.PromptTok, r.CompletionTok)
	}
	if !hasCallEvent(r, "complete") {
		t.Fatalf("record events = %+v, want complete", r.Events)
	}
}

func TestClaudeMessagesHandlerRecordsCallLogUpstream429(t *testing.T) {
	dir := setupCallLogDir(t)
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
		{status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limit exceeded"}}`},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"primary-model","messages":[]}`))
	claudeMessagesHandler(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "fail" || !strings.Contains(r.ErrMsg, "429") {
		t.Fatalf("record = %+v, want fail with 429", r)
	}
	if !hasCallEvent(r, "upstream_error") {
		t.Fatalf("record events = %+v, want upstream_error", r.Events)
	}
}

func TestClaudeMessagesHandlerRecordsCallLogStream(t *testing.T) {
	dir := setupCallLogDir(t)
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{{
		status: http.StatusOK,
		body: strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":""}]}`,
			``,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"primary-model","messages":[],"stream":true}`))
	claudeMessagesHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	recs := readRecords(t, filepath.Join(dir, "call_log.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	r := recs[0]
	if r.Status != "ok" || !r.Stream || r.Path != "/v1/messages" {
		t.Fatalf("record = %+v", r)
	}
	if !hasCallEvent(r, "connect_ok") || !hasCallEvent(r, "complete") {
		t.Fatalf("record events = %+v, want connect_ok+complete", r.Events)
	}
}

// hasCallEvent 判断记录事件链中是否含指定类型。
func hasCallEvent(r CallRecord, typ string) bool {
	for _, e := range r.Events {
		if e.Type == typ {
			return true
		}
	}
	return false
}
