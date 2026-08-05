// 临时端到端验证：recordCall 真实落盘 call_log.jsonl 的格式
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordCallEndToEnd(t *testing.T) {
	dir := t.TempDir()
	oldPath := callLogPath
	oldEnabled := callLogEnabled
	oldLog := callLog
	defer func() {
		callLogPath = oldPath
		callLogEnabled = oldEnabled
		callLog = oldLog
	}()

	callLogPath = filepath.Join(dir, "call_log.jsonl")
	callLogEnabled = true
	initCallLog()

	recordCall(CallRecord{
		ReqID: "e2e-1", TS: time.Now().Format(time.RFC3339),
		Path: "/v1/chat/completions", Model: "test-model", Stream: true,
		RouteMode: "failover", Status: "ok",
		Nodes: []string{"127.0.0.1:28100"},
		Events: []CallEvent{
			{Type: "connect_ok", Node: "127.0.0.1:28100", Detail: "connected", At: time.Now()},
			{Type: "complete", Node: "127.0.0.1:28100", Detail: "done", At: time.Now()},
		},
		PromptTok: 12, CompletionTok: 345, DurationMS: 42000,
	})
	recordCall(CallRecord{
		ReqID: "e2e-2", TS: time.Now().Format(time.RFC3339),
		Model: "test-model", Status: "fail", ErrMsg: "所有节点均失败，回复中断",
		Events: []CallEvent{
			{Type: "ttft_timeout", Node: "n1", Detail: "no first token", At: time.Now()},
			{Type: "switch", Node: "n1", Detail: "switching", At: time.Now()},
			{Type: "all_failed", Detail: "all failed", At: time.Now()},
		},
	})

	data, err := os.ReadFile(callLogPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if len(content) == 0 {
		t.Fatal("call_log.jsonl empty")
	}
	// 每行一条 JSON，含 req_id 与 status
	for _, want := range []string{"e2e-1", "e2e-2", "\"status\":\"ok\"", "\"status\":\"fail\""} {
		if !containsStr(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
	t.Logf("call_log.jsonl 内容:\n%s", content)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
