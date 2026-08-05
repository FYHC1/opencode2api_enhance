package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimeoutConfigRandomRange(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		v := cfg.RandomTTFT()
		if v < cfg.TTFTRange[0] || v > cfg.TTFTRange[1] {
			t.Fatalf("TTFT %v out of range %v-%v", v, cfg.TTFTRange[0], cfg.TTFTRange[1])
		}
		seen[v] = true
	}
	if len(seen) < 5 {
		t.Fatalf("TTFT not random: only %d distinct", len(seen))
	}
	// 静默区间同测
	seen2 := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		v := cfg.RandomSilence()
		if v < cfg.SilenceRange[0] || v > cfg.SilenceRange[1] {
			t.Fatalf("silence %v out of range", v)
		}
		seen2[v] = true
	}
	if len(seen2) < 5 {
		t.Fatalf("silence not random: only %d distinct", len(seen2))
	}
	// 探测数
	for i := 0; i < 50; i++ {
		n := cfg.RandomProbeN()
		if n < cfg.ProbeRange[0] || n > cfg.ProbeRange[1] {
			t.Fatalf("probe %d out of range", n)
		}
	}
}

func TestCallStatusText(t *testing.T) {
	if got := CallStatusText(CallRecord{Status: "ok"}); got != "【成功】" {
		t.Fatalf("got %q", got)
	}
	if got := CallStatusText(CallRecord{Status: "fail"}); got != "【失败】" {
		t.Fatalf("got %q", got)
	}
}

func TestCallLogRingBuffer(t *testing.T) {
	l := NewEventLog(3)
	for i := 0; i < 5; i++ {
		l.Append(CallRecord{ReqID: string(rune('a' + i)), Status: "ok"})
	}
	recs := l.ReadAll()
	if len(recs) != 3 {
		t.Fatalf("expected 3, got %d", len(recs))
	}
	if recs[0].ReqID != "c" {
		t.Fatalf("expected oldest dropped, first %q", recs[0].ReqID)
	}
}

func TestCallLogJSONLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call_log.jsonl")
	l := NewEventLog(10)
	l.SetPath(path)
	l.Append(CallRecord{ReqID: "r1", Status: "ok", Model: "m1", Events: []CallEvent{{Type: "switch", Node: "a", Detail: "ttft", At: time.Now()}}})
	l.Append(CallRecord{ReqID: "r2", Status: "fail", ErrMsg: "boom"})
	restored, err := LoadCallLogFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recs := restored.ReadAll()
	if len(recs) != 2 {
		t.Fatalf("expected 2 after load, got %d", len(recs))
	}
	if recs[1].Status != "fail" || recs[1].ErrMsg != "boom" {
		t.Fatalf("fields lost: %+v", recs[1])
	}
	if len(recs[0].Events) != 1 || recs[0].Events[0].Type != "switch" {
		t.Fatalf("events lost: %+v", recs[0])
	}
}

func TestSetTimeoutConfigFromApp(t *testing.T) {
	// 先重置为默认
	timeoutCfg = DefaultTimeoutConfig()
	cfg := AppConfig{
		TTFTMinMS:    1000,
		TTFTMaxMS:    2000,
		SilenceMinMS: 3000,
		SilenceMaxMS: 5000,
		ProbeMin:     1,
		ProbeMax:     2,
		CallLogMax:   100,
	}
	setTimeoutConfigFromApp(cfg)
	if timeoutCfg.TTFTRange[0] != 1000*time.Millisecond || timeoutCfg.TTFTRange[1] != 2000*time.Millisecond {
		t.Fatalf("TTFT range not applied: %v", timeoutCfg.TTFTRange)
	}
	if timeoutCfg.SilenceRange[0] != 3000*time.Millisecond || timeoutCfg.SilenceRange[1] != 5000*time.Millisecond {
		t.Fatalf("silence range not applied: %v", timeoutCfg.SilenceRange)
	}
	if timeoutCfg.ProbeRange != [2]int{1, 2} {
		t.Fatalf("probe range not applied: %v", timeoutCfg.ProbeRange)
	}
	// 非法区间（min>max）应被忽略，保持旧值
	setTimeoutConfigFromApp(AppConfig{TTFTMinMS: 5000, TTFTMaxMS: 1000})
	if timeoutCfg.TTFTRange[0] != 1000*time.Millisecond {
		t.Fatalf("invalid range should be ignored, got %v", timeoutCfg.TTFTRange)
	}
	// CallLogMax 重置环形上限
	setTimeoutConfigFromApp(AppConfig{CallLogMax: 5})
	if callLog.MaxRecords() != 5 {
		t.Fatalf("callLogMax not applied: %d", callLog.MaxRecords())
	}
	// 恢复，避免影响其他测试
	timeoutCfg = DefaultTimeoutConfig()
	callLog = NewEventLog(DefaultCallLogMax)
	_ = os.Remove("call_log.jsonl")
}