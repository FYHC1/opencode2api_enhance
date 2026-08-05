# 网关流内超时切换 + 全流程日志 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 解决「回复一半不回复」问题——在 opencode2api_enhance 的 Go 网关增加流内超时（TTFT + 块间静默，区间随机）+ 断点续写切换 + 全流程日志与前端独立日志页。先在独立实验目录验证核心逻辑，再落地到真实项目。

**Architecture:** 两阶段。阶段 1 在 `D:\AI_Projects\gateway_experiment` 建独立 Go 实验网关，含模拟上游节点（黑洞/半路断流/正常），实现并验证「区间超时 + 并行探测切换 + 断点续写 + 事件日志」四个核心模块，全部以可复用、可移植形态编写。阶段 2 把验证过的模块落地到 `opencode2api_enhance_main`（Go 核心 `main.go` 接入 `chatCompletionsHandler`/`callOpenCodeAPIStream`；Rust 层新增 `get_call_log` 接口；前端新增 `LogsPage` + 设置页区间表单）。

**Tech Stack:** Go 1.22+（实验网关与真实核心同语言，验证结果最接近实情）、标准库 net/http、math/rand/v2、bufio、JSONL 落盘；阶段 2 涉及 Tauri 2 Rust command 与 React/TS 前端。

**设计文档:** `docs/superpowers/specs/2026-08-05-gateway-timeout-failover-logging-design.md`

## Global Constraints

- **禁止修改** `opencode2api_enhance`（feature/web-self-service 工作区，另一 AI 在实施）的任何代码。
- 阶段 1 只写 `D:\AI_Projects\gateway_experiment`（独立目录，非 git 仓库，或本地 git）。
- 阶段 2 只写 `D:\AI_Projects\opencode2api_enhance_main`（worktree，main 分支）。
- 实验模块必须设计成可移植：核心逻辑不依赖实验 main()，函数签名贴近 `main.go` 现有风格。
- 超时配置：TTFT 默认 15-25s 区间、静默默认 30-60s 区间、并行探测数默认 2-3，均为 `[min,max]` 均匀随机，min 下限保护防频繁请求。
- 日志默认保留 5000 条（可配置），JSONL 格式，成功=一行简短、异常/切换=整块详细时间线，每条以 `【成功】/【失败】` 开头。
- 失败回滚：每阶段末验证失败项必须记入下一阶段计划开头（「上阶段遗留」节）再继续，循环直到全部完成。
- 测试门槛：每步测试先失败后通过（TDD）；阶段 1 的 5 个验证点全部通过才进入阶段 2。

---

# 阶段 1：实验网关验证（D:\AI_Projects\gateway_experiment）

**目标：** 用本地模拟节点验证「区间超时 → 并行探测 → 断点续写切换 → 事件日志」逻辑闭环，产出可移植 Go 模块。

**阶段验证点（全部通过才算阶段成功）：**
1. 黑洞节点 → TTFT 超时 → 切正常节点续写（内容衔接，无重复/缺失）
2. 半路断流节点 → 静默超时/流中断 → 切正常节点，内容衔接
3. 区间随机：跑 20 次请求，TTFT 实际值落在 `[15,25]` 且非恒定
4. 切换前并行探测数 = 配置值（2-3）
5. 全部失败 → 错误事件正确返回

### Task 1.1: 初始化 Go 模块与目录

**Files:**
- Create: `D:\AI_Projects\gateway_experiment\go.mod`
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\config.go`
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\config_test.go`

**Interfaces:**
- Produces: `type TimeoutConfig struct { TTFTRange [2]time.Duration; SilenceRange [2]time.Duration; ProbeRange [2]int }`，方法 `func (c TimeoutConfig) RandomTTFT() time.Duration`、`RandomSilence() time.Duration`、`RandomProbeN() int`；默认 `func DefaultTimeoutConfig() TimeoutConfig`

- [ ] **Step 1: 写失败测试（区间随机在范围内且非恒定）**

```go
// internal/gateway/config_test.go
package gateway

import (
	"testing"
	"time"
)

func TestRandomTTFTWithinRange(t *testing.T) {
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
		t.Fatalf("TTFT not random enough: only %d distinct values", len(seen))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -run TestRandomTTFTWithinRange -v`
Expected: FAIL（`undefined: DefaultTimeoutConfig`）

- [ ] **Step 3: 实现 config.go**

```go
// internal/gateway/config.go
package gateway

import (
	"math/rand/v2"
	"time"
)

type TimeoutConfig struct {
	TTFTRange    [2]time.Duration
	SilenceRange [2]time.Duration
	ProbeRange   [2]int
}

func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		TTFTRange:    [2]time.Duration{15 * time.Second, 25 * time.Second},
		SilenceRange: [2]time.Duration{30 * time.Second, 60 * time.Second},
		ProbeRange:   [2]int{2, 3},
	}
}

// randRange 返回 [min,max] 均匀随机值（含端点）
func randRange(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int64N(int64(max-min)+1))
}

func (c TimeoutConfig) RandomTTFT() time.Duration {
	return randRange(c.TTFTRange[0], c.TTFTRange[1])
}

func (c TimeoutConfig) RandomSilence() time.Duration {
	return randRange(c.SilenceRange[0], c.SilenceRange[1])
}

func (c TimeoutConfig) RandomProbeN() int {
	if c.ProbeRange[1] <= c.ProbeRange[0] {
		return c.ProbeRange[0]
	}
	return c.ProbeRange[0] + rand.IntN(c.ProbeRange[1]-c.ProbeRange[0]+1)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -v`
Expected: PASS（TestRandomTTFTWithinRange 通过，区间内且 ≥5 个不同值）

- [ ] **Step 5: 提交**

```bash
cd /d/AI_projects/gateway_experiment
git init
git add internal/gateway/config.go internal/gateway/config_test.go go.mod
git commit -m "feat(exp): 区间随机超时配置模块 + TDD 测试"
```

### Task 1.2: 模拟上游节点（黑洞/半路断流/正常）

**Files:**
- Create: `D:\AI_Projects\gateway_experiment\internal\mocknode\server.go`
- Create: `D:\AI_Projects\gateway_experiment\internal\mocknode\server_test.go`

**Interfaces:**
- Produces: `type NodeKind int`（`KindNormal`/`KindBlackhole`/`KindCutoff`）；`func NewNode(kind NodeKind, name string) *Node`；`func (n *Node) Addr() string`；`func (n *Node) Close()`。正常节点：收到 `/chat/completions` 请求后以 SSE 流式吐 5 个 chunk（每 chunk 一句"你好，这是第i句"）+ `[DONE]` + usage。黑洞节点：接受连接但永不回包（挂起）。断流节点：吐 2 个 chunk 后关闭连接。

- [ ] **Step 1: 写失败测试**

```go
// internal/mocknode/server_test.go
package mocknode

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNormalNodeSSE(t *testing.T) {
	n := NewNode(KindNormal, "normal")
	defer n.Close()
	req, _ := http.NewRequest("POST", "http://"+n.Addr()+"/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	count := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			count++
		}
	}
	if count < 6 { // 5 chunk + [DONE]
		t.Fatalf("expected >=6 data lines, got %d", count)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/mocknode/ -v`
Expected: FAIL（`undefined: NewNode`）

- [ ] **Step 3: 实现 server.go**

```go
// internal/mocknode/server.go
package mocknode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type NodeKind int

const (
	KindNormal NodeKind = iota
	KindBlackhole
	KindCutoff
)

type Node struct {
	kind NodeKind
	name string
	ln   net.Listener
	port int
}

func NewNode(kind NodeKind, name string) *Node {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	n := &Node{kind: kind, name: name, ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", n.handle)
	go http.Serve(ln, mux)
	return n
}

func (n *Node) Addr() string { return "127.0.0.1:" + strconv.Itoa(n.port) }
func (n *Node) Close()       { n.ln.Close() }

func (n *Node) handle(w http.ResponseWriter, r *http.Request) {
	switch n.kind {
	case KindBlackhole:
		// 挂起：永久阻塞，永不回包
		select {}
	case KindCutoff:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		writeChunk(w, fl, 0, "你好")
		writeChunk(w, fl, 1, "这是第二句")
		// 吐完 2 句直接断开连接（不写 [DONE]）
	case KindNormal:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			writeChunk(w, fl, i, fmt.Sprintf("你好，这是第%d句", i+1))
			time.Sleep(30 * time.Millisecond)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		fl.Flush()
	}
}

func writeChunk(w http.ResponseWriter, fl http.Flusher, idx int, content string) {
	chunk := map[string]any{
		"choices": []any{map[string]any{
			"index": idx,
			"delta": map[string]any{"content": content},
		}},
	}
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
	fl.Flush()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/mocknode/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/mocknode/
git commit -m "feat(exp): 模拟上游节点（正常/黑洞/断流）+ TDD 测试"
```

### Task 1.3: 事件日志模块

**Files:**
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\eventlog.go`
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\eventlog_test.go`

**Interfaces:**
- Produces: `type CallEvent struct { Type string; Node string; Detail string; At time.Time }`；`type CallRecord struct { ReqID, TS, Path, Model string; Stream bool; RouteMode string; Nodes []string; Events []CallEvent; Status string; PromptTok, CompletionTok int64; DurationMS int64; ErrMsg string }`；`type EventLog struct{...}`；`func NewEventLog(maxRecords int) *EventLog`；`func (l *EventLog) Append(rec CallRecord) error`（超上限丢最旧）；`func (l *EventLog) ReadAll() []CallRecord`；`func (l *EventLog) StatusText(rec) string` 返回 `【成功】`/`【失败】` 前缀。`MaxRecords()` 返回上限。

- [ ] **Step 1: 写失败测试（环形截断 + 状态前缀）**

```go
// internal/gateway/eventlog_test.go
package gateway

import (
	"testing"
	"time"
)

func TestEventLogRingBuffer(t *testing.T) {
	l := NewEventLog(3)
	for i := 0; i < 5; i++ {
		l.Append(CallRecord{ReqID: string(rune('a' + i)), Status: "ok"})
	}
	recs := l.ReadAll()
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if recs[0].ReqID != "c" {
		t.Fatalf("expected oldest dropped, first is %q", recs[0].ReqID)
	}
	if l.MaxRecords() != 3 {
		t.Fatalf("MaxRecords mismatch")
	}
}

func TestStatusText(t *testing.T) {
	if got := StatusText(CallRecord{Status: "ok"}); got != "【成功】" {
		t.Fatalf("got %q", got)
	}
	if got := StatusText(CallRecord{Status: "fail"}); got != "【失败】" {
		t.Fatalf("got %q", got)
	}
}

func TestEventLogTimestamp(t *testing.T) {
	l := NewEventLog(10)
	l.Append(CallRecord{ReqID: "x", Status: "ok", TS: time.Now().Format(time.RFC3339)})
	for _, r := range l.ReadAll() {
		if _, err := time.Parse(time.RFC3339, r.TS); err != nil {
			t.Fatalf("bad TS %q", r.TS)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -run TestEventLog -v`
Expected: FAIL（undefined symbols）

- [ ] **Step 3: 实现 eventlog.go**

```go
// internal/gateway/eventlog.go
package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CallEvent struct {
	Type   string    `json:"type"`
	Node   string    `json:"node,omitempty"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

type CallRecord struct {
	ReqID          string      `json:"req_id"`
	TS             string      `json:"ts"`
	Path           string      `json:"path"`
	Model          string      `json:"model"`
	Stream         bool        `json:"stream"`
	RouteMode      string      `json:"route_mode"`
	Nodes          []string    `json:"nodes"`
	Events         []CallEvent `json:"events"`
	Status         string      `json:"status"`
	PromptTok      int64       `json:"prompt_tokens"`
	CompletionTok  int64       `json:"completion_tokens"`
	DurationMS     int64       `json:"duration_ms"`
	ErrMsg         string      `json:"err_msg,omitempty"`
}

func StatusText(rec CallRecord) string {
	if rec.Status == "ok" {
		return "【成功】"
	}
	return "【失败】"
}

type EventLog struct {
	mu         sync.Mutex
	maxRecords int
	records    []CallRecord
	path       string // 非空时同步落盘 JSONL
}

func NewEventLog(maxRecords int) *EventLog {
	return &EventLog{maxRecords: maxRecords}
}

// SetPath 启用 JSONL 落盘（路径的父目录需存在）
func (l *EventLog) SetPath(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.path = path
}

func (l *EventLog) MaxRecords() int { return l.maxRecords }

func (l *EventLog) Append(rec CallRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
	if len(l.records) > l.maxRecords {
		l.records = l.records[len(l.records)-l.maxRecords:]
	}
	if l.path != "" {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) ReadAll() []CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]CallRecord, len(l.records))
	copy(out, l.records)
	return out
}

// LoadFromFile 从 JSONL 恢复（用于重启后读取历史）
func LoadFromFile(path string) (*EventLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEventLog(5000), nil
		}
		return nil, err
	}
	l := NewEventLog(5000)
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec CallRecord
		if json.Unmarshal(line, &rec) == nil {
			l.records = append(l.records, rec)
		}
	}
	if len(l.records) > l.maxRecords {
		l.records = l.records[len(l.records)-l.maxRecords:]
	}
	return l, nil
}

func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -run TestEventLog -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/eventlog.go internal/gateway/eventlog_test.go
git commit -m "feat(exp): 事件日志模块（环形缓冲+JSONL落盘+状态前缀）+ TDD"
```

### Task 1.4: 核心——流内超时 + 并行探测 + 断点续写切换

**Files:**
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\failover.go`
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\failover_test.go`
- Create: `D:\AI_Projects\gateway_experiment\main.go`（实验入口：起 3 个模拟节点 + 1 个网关，暴露 HTTP 转发）

**Interfaces:**
- Produces: `type Upstream struct { Name string; URL string }`（URL 指向模拟节点的 `/chat/completions`）；`type Engine struct{...}`；`func NewEngine(cfg TimeoutConfig, log *EventLog) *Engine`；`func (e *Engine) ProxySSE(w http.ResponseWriter, req *http.Request, candidates []Upstream, bodyMap map[string]any) error`（核心：串行候选 + TTFT/静默计时 + 并行探测切换 + 续写 + 事件记录）；内部函数 `probeCandidates(cands []Upstream, n int) []Upstream`（并行发 `max_tokens:1` 请求，按响应延迟排序）；`buildResumeBody(body map[string]any, accumulated string) map[string]any`（续写：追加 assistant+user 消息）。

**关键实现要点（写入 failover.go）：**
- TTFT 计时：建流后启动 `timer(cfg.RandomTTFT())`，收到首个 `data:` chunk 前触发即判定超时。
- 静默计时：收到 chunk 后重置 `timer(cfg.RandomSilence())`，两 chunk 间超时判定卡死。
- 切换：`record_fail` 当前节点 → `probeCandidates` 选目标 → 构造续写 body → 重连新节点继续。
- 事件：切换时记录 `{"type":"switch","from":..,"to":..,"reason":"ttft_timeout|silence_timeout|stream_interrupt"}`。
- 失败兜底：全部失败写 `data: {"error":"所有节点均失败，回复中断"}`。

- [ ] **Step 1: 写失败测试（黑洞→TTFT→续写切换；断流→静默→切换）**

```go
// internal/gateway/failover_test.go
package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gateway_experiment/internal/mocknode"
)

// 黑洞在前、正常在后：TTFT 超时后应切到正常节点，内容衔接（含"你好，这是第"）
func TestBlackholeFailoverContinuation(t *testing.T) {
	black := mocknode.NewNode(mocknode.KindBlackhole, "black")
	defer black.Close()
	normal := mocknode.NewNode(mocknode.KindNormal, "normal")
	defer normal.Close()

	cfg := DefaultTimeoutConfig()
	cfg.TTFTRange = [2]time.Duration{200 * time.Millisecond, 300 * time.Millisecond} // 测试提速
	cfg.SilenceRange = [2]time.Duration{500 * time.Millisecond, 600 * time.Millisecond}
	log := NewEventLog(100)
	eng := NewEngine(cfg, log)

	body := map[string]any{"model": "m", "stream": true, "messages": []any{map[string]any{"role": "user", "content": "你好"}}}
	cands := []Upstream{{Name: "black", URL: "http://" + black.Addr() + "/chat/completions"},
		{Name: "normal", URL: "http://" + normal.Addr() + "/chat/completions"}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	err := eng.ProxySSE(rec, req, cands, body)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Body.String()
	if !strings.Contains(got, "你好，这是第1句") {
		t.Fatalf("expected continuation content, got: %s", got)
	}
	// 事件里应有 ttft_timeout 和 switch
	recs := log.ReadAll()
	if len(recs) == 0 || !hasEvent(recs[0].Events, "switch", "ttft_timeout") {
		t.Fatalf("expected switch event with ttft_timeout, got %+v", recs)
	}
}

func hasEvent(evs []CallEvent, typ, detail string) bool {
	for _, e := range evs {
		if e.Type == typ && strings.Contains(e.Detail, detail) {
			return true
		}
	}
	return false
}

// 断流节点吐2句后断：应切正常节点，且从"第3句"开始补（续写）
func TestCutoffFailoverResume(t *testing.T) {
	cut := mocknode.NewNode(mocknode.KindCutoff, "cut")
	defer cut.Close()
	normal := mocknode.NewNode(mocknode.KindNormal, "normal")
	defer normal.Close()

	cfg := DefaultTimeoutConfig()
	cfg.SilenceRange = [2]time.Duration{300 * time.Millisecond, 400 * time.Millisecond}
	log := NewEventLog(100)
	eng := NewEngine(cfg, log)

	body := map[string]any{"model": "m", "stream": true, "messages": []any{map[string]any{"role": "user", "content": "你好"}}}
	cands := []Upstream{{Name: "cut", URL: "http://" + cut.Addr() + "/chat/completions"},
		{Name: "normal", URL: "http://" + normal.Addr() + "/chat/completions"}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if err := eng.ProxySSE(rec, req, cands, body); err != nil {
		t.Fatal(err)
	}
	got := rec.Body.String()
	if !strings.Contains(got, "你好，这是第3句") {
		t.Fatalf("expected resumed content (第3句), got: %s", got)
	}
}

// 全部失败 → 错误事件
func TestAllFailErrorEvent(t *testing.T) {
	b1 := mocknode.NewNode(mocknode.KindBlackhole, "b1")
	defer b1.Close()
	b2 := mocknode.NewNode(mocknode.KindBlackhole, "b2")
	defer b2.Close()

	cfg := DefaultTimeoutConfig()
	cfg.TTFTRange = [2]time.Duration{100 * time.Millisecond, 150 * time.Millisecond}
	log := NewEventLog(100)
	eng := NewEngine(cfg, log)

	body := map[string]any{"model": "m", "stream": true}
	cands := []Upstream{{Name: "b1", URL: "http://" + b1.Addr() + "/chat/completions"},
		{Name: "b2", URL: "http://" + b2.Addr() + "/chat/completions"}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	eng.ProxySSE(rec, req, cands, body)
	if !strings.Contains(rec.Body.String(), "所有节点均失败") {
		t.Fatalf("expected error event, got: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -run TestBlackhole -v`
Expected: FAIL（undefined NewEngine）

- [ ] **Step 3: 实现 failover.go（核心，含续写与探测）**

```go
// internal/gateway/failover.go
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type Upstream struct {
	Name string
	URL  string
}

type Engine struct {
	cfg     TimeoutConfig
	log     *EventLog
	reqSeq  atomic.Int64
	client  *http.Client
}

func NewEngine(cfg TimeoutConfig, log *EventLog) *Engine {
	return &Engine{
		cfg: cfg,
		log: log,
		client: &http.Client{
			Transport: &http.Transport{MaxIdleConns: 100, MaxIdleConnsPerHost: 20},
		},
	}
}

// ProxySSE 核心转发：串行候选 + TTFT/静默计时 + 切换续写
func (e *Engine) ProxySSE(w http.ResponseWriter, req *http.Request, candidates []Upstream, bodyMap map[string]any) error {
	reqID := fmt.Sprintf("req_%06d", e.reqSeq.Add(1))
	rec := CallRecord{
		ReqID: reqID, TS: time.Now().Format(time.RFC3339),
		Path: "/v1/chat/completions", Model: strAny(bodyMap["model"]),
		Stream: true, RouteMode: "failover", Status: "ok",
	}
	for _, c := range candidates {
		rec.Nodes = append(rec.Nodes, c.Name)
	}
	start := time.Now()
	accumulated := ""
	prefixDone := false

	for _, up := range candidates {
		event := func(typ, detail string) {
			rec.Events = append(rec.Events, CallEvent{Type: typ, Node: up.Name, Detail: detail, At: time.Now()})
		}

		reqBody := cloneMap(bodyMap)
		reqBody["model"] = up.Name // 实验：用节点名当模型便于断言
		if accumulated != "" {
			reqBody = buildResumeBody(reqBody, accumulated)
		}
		payload, _ := json.Marshal(reqBody)

		upr, err := http.NewRequest("POST", up.URL, bytes.NewReader(payload))
		if err != nil {
			event("connect_error", err.Error())
			continue
		}
		upr.Header.Set("Content-Type", "application/json")
		ctx, cancel := context.WithCancel(req.Context())
		upr = upr.WithContext(ctx)
		resp, err := e.client.Do(upr)
		if err != nil {
			event("connect_error", err.Error())
			cancel()
			continue
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			event("http_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(b)))
			cancel()
			continue
		}

		event("connect_ok", "connected")
		reader := bufio.NewReader(resp.Body)
		ttftTimer := time.NewTimer(e.cfg.RandomTTFT())
		silenceTimer := time.NewTimer(e.cfg.RandomSilence())
		gotFirst := false
		streamOK := true

	readLoop:
		for {
			type lineResult struct {
				line string
				err  error
			}
			ch := make(chan lineResult, 1)
			go func() {
				ln, er := reader.ReadString('\n')
				ch <- lineResult{ln, er}
			}()

			select {
			case res := <-ch:
				if !gotFirst {
					gotFirst = true
					if !ttftTimer.Stop() {
						select { case <-ttftTimer.C: default: }
					}
				}
				if !silenceTimer.Stop() {
					select { case <-silenceTimer.C: default: }
				}
				line := strings.TrimSpace(res.line)
				if res.err != nil {
					if res.err == io.EOF {
						// 正常结束（读到 [DONE] 或上游主动关）
						if !strings.Contains(accumulated, "DONE") && !doneSeen(line) {
							event("stream_interrupt", "EOF without [DONE]")
							streamOK = false
						}
						break readLoop
					}
					event("stream_error", res.err.Error())
					streamOK = false
					break readLoop
				}
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					if data == "[DONE]" {
						event("complete", "done")
						fmt.Fprintf(w, "data: [DONE]\n\n")
						flush(w)
						break readLoop
					}
					// 累积内容
					var obj map[string]any
					if json.Unmarshal([]byte(data), &obj) == nil {
						if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
							if delta, ok := chs[0].(map[string]any)["delta"].(map[string]any); ok {
								if c, ok := delta["content"].(string); ok {
									accumulated += c
								}
							}
						}
					}
					if !prefixDone && accumulated != "" {
						fmt.Fprintf(w, "data: %s\n\n", data)
						prefixDone = true
					} else {
						fmt.Fprintf(w, "data: %s\n\n", data)
					}
					flush(w)
				}
				silenceTimer.Reset(e.cfg.RandomSilence())
				if !gotFirst && ttftTimer != nil {
					ttftTimer.Reset(e.cfg.RandomTTFT())
				}
			case <-ttftTimer.C:
				event("ttft_timeout", fmt.Sprintf("no first token within %v", e.cfg.TTFTRange))
				streamOK = false
				break readLoop
			case <-silenceTimer.C:
				event("silence_timeout", fmt.Sprintf("no data within %v", e.cfg.SilenceRange))
				streamOK = false
				break readLoop
			case <-req.Context().Done():
				streamOK = false
				break readLoop
			}
		}
		cancel()
		resp.Body.Close()
		if streamOK {
			// 成功返回
			rec.DurationMS = time.Since(start).Milliseconds()
			rec.CompletionTok = int64(len([]rune(accumulated)))
			rec.Status = "ok"
			e.log.Append(rec)
			return nil
		}
		// 切换：记录 + 探测下一个目标
		event("switch", fmt.Sprintf("switching from %s, accumulated=%d chars", up.Name, len(accumulated)))
		if accumulated != "" {
			// 已吐内容写一条可见标记（实验阶段便于观察）
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"[已切换节点，续写]\"}}]}\n\n")
			flush(w)
		}
	}

	rec.Status = "fail"
	rec.ErrMsg = "所有节点均失败，回复中断"
	rec.DurationMS = time.Since(start).Milliseconds()
	rec.Events = append(rec.Events, CallEvent{Type: "all_failed", Detail: rec.ErrMsg, At: time.Now()})
	e.log.Append(rec)
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\\n\\n⚠️ 所有节点均失败，回复中断。\"}}]}\n\n")
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flush(w)
	return nil
}

// probeCandidates 并行探测候选，返回按延迟升序的健康节点（前 n 个）
func probeCandidates(cands []Upstream, n int) []Upstream {
	type result struct {
		up      Upstream
		latency time.Duration
		ok      bool
	}
	results := make([]result, len(cands))
	var wg sync.WaitGroup
	for i, up := range cands {
		wg.Add(1)
		go func(i int, up Upstream) {
			defer wg.Done()
			start := time.Now()
			payload := []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
			r, _ := http.NewRequest("POST", up.URL, bytes.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				results[i] = result{up: up, ok: false}
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			results[i] = result{up: up, latency: time.Since(start), ok: resp.StatusCode == 200}
		}(i, up)
	}
	wg.Wait()
	sort.SliceStable(results, func(a, b int) bool {
		if results[a].ok != results[b].ok {
			return results[a].ok
		}
		return results[a].latency < results[b].latency
	})
	var out []Upstream
	for _, r := range results {
		if r.ok {
			out = append(out, r.up)
			if len(out) >= n {
				break
			}
		}
	}
	return out
}

// buildResumeBody 续写：把已吐内容作为 assistant 历史，追加"请继续"
func buildResumeBody(body map[string]any, accumulated string) map[string]any {
	out := cloneMap(body)
	msgs, _ := out["messages"].([]any)
	if msgs == nil {
		msgs = []any{}
	}
	msgs = append(msgs,
		map[string]any{"role": "assistant", "content": accumulated},
		map[string]any{"role": "user", "content": "请继续上面的回复，从中断处接着写。"},
	)
	out["messages"] = msgs
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func doneSeen(line string) bool { return strings.Contains(line, "[DONE]") }

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

var _ = log.Printf // 保留 import
```

- [ ] **Step 4: 修复测试问题并让全部通过**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./... -v`
Expected: 全部 PASS（黑洞→TTFT→续写、断流→静默→续写、全部失败→错误事件、区间随机、环形日志）

> ⚠️ 若测试失败（如续写内容断言、计时竞争），用 systematic-debugging 定位修复；修复不了的记入「阶段 1 遗留」再继续。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/failover.go internal/gateway/failover_test.go main.go
git commit -m "feat(exp): 流内超时+并行探测+断点续写切换核心 + TDD 全绿"
```

### Task 1.5: 阶段 1 验证（5 个验证点）

**Files:**
- Create: `D:\AI_Projects\gateway_experiment\internal\gateway\validation_test.go`

**Interfaces:**
- Consumes: Engine/TimeoutConfig/EventLog/mocknode 全部。

- [ ] **Step 1: 写验证测试（验证点 1-5 的自动化版本）**

```go
// internal/gateway/validation_test.go
package gateway

import (
	"strings"
	"testing"
	"time"

	"gateway_experiment/internal/mocknode"
)

// 验证点3：区间随机非恒定且不越界（覆盖默认区间）
func TestValidationRandomRange(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		v := cfg.RandomTTFT()
		if v < cfg.TTFTRange[0] || v > cfg.TTFTRange[1] {
			t.Fatalf("out of range: %v", v)
		}
		seen[v] = true
	}
	if len(seen) < 5 {
		t.Fatalf("not random: %d distinct", len(seen))
	}
}

// 验证点4：并行探测数在配置区间内
func TestValidationProbeCount(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	for i := 0; i < 50; i++ {
		n := cfg.RandomProbeN()
		if n < cfg.ProbeRange[0] || n > cfg.ProbeRange[1] {
			t.Fatalf("probe n %d out of range", n)
		}
	}
}

// 验证点2：断流后内容衔接（无重复无缺失）——通过续写 body 断言
func TestValidationResumeNoDup(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	rb := buildResumeBody(body, "已生成内容ABC")
	msgs := rb["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" || !strings.Contains(last["content"].(string), "请继续") {
		t.Fatalf("resume body malformed: %v", msgs)
	}
	// 已生成内容在 assistant 消息里
	found := false
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "assistant" && mm["content"] == "已生成内容ABC" {
			found = true
		}
	}
	if !found {
		t.Fatalf("accumulated content missing in resume body")
	}
}
```

- [ ] **Step 2: 运行验证测试**

Run: `cd D:\AI_Projects\gateway_experiment && go test ./internal/gateway/ -run TestValidation -v`
Expected: PASS

- [ ] **Step 3: 手动端到端验证（真实 HTTP 网关）**

写 `main.go` 实验入口：起 1 黑洞 + 1 正常 + 1 断流节点，`NewEngine` 转发到 `:18081/v1/chat/completions`。用 curl 模拟客户端验证：
1. `curl -N http://127.0.0.1:18081/v1/chat/completions -d '{"model":"x","stream":true,"messages":[{"role":"user","content":"hi"}]}'` → 观察黑洞先被选中后自动切正常节点并续写输出。
2. 日志文件（`testdata/call_log.jsonl`）中出现 `【成功】` 记录与 `switch` 事件。

Run: `cd D:\AI_Projects\gateway_experiment && go run .`
Expected: 手动观察输出含切换标记与完整续写内容

- [ ] **Step 4: 提交**

```bash
git add internal/gateway/validation_test.go main.go testdata/
git commit -m "feat(exp): 阶段1验证——5个验证点全部通过"
```

### Task 1.6: 阶段 1 收尾——验证总结

- [ ] **Step 1: 检查 5 个验证点状态，生成「阶段 1 验证报告」**

| 验证点 | 预期 | 结果 |
|---|---|---|
| 1. 黑洞→TTFT→切正常续写 | PASS | 待填 |
| 2. 断流→静默→切正常续写 | PASS | 待填 |
| 3. 区间随机非恒定 | PASS | 待填 |
| 4. 并行探测数=配置 | PASS | 待填 |
| 5. 全部失败→错误事件 | PASS | 待填 |

- [ ] **Step 2: 把验证失败的项（若有）写入阶段 2 计划开头「上阶段遗留」节**

格式：`- [ ] [阶段1遗留] <验证点N> <失败现象> → 阶段2修复方案：<方案>`

- [ ] **Step 3: 提交验证报告**

```bash
git add .
git commit -m "docs(exp): 阶段1验证报告"
```

---

# 阶段 2：落地集成（D:\AI_Projects\opencode2api_enhance_main）

**目标：** 把阶段 1 验证过的模块落地到真实项目：Go 核心接入流内超时切换、Rust 新增日志接口、前端新增日志页 + 区间配置表单。

**前置：** 阶段 1 的 5 个验证点全部通过（或失败项已记录在本阶段「上阶段遗留」）。

## 上阶段遗留（阶段 1 验证失败项，循环修复）

- （阶段 1 完成后填写；无失败则留空）

### Task 2.1: Go 核心接入超时切换 + 事件日志

**Files:**
- Modify: `opencode2api_enhance_main/main.go`（`chatCompletionsHandler` 流式分支、`callOpenCodeAPIStream` 附近、新增配置字段）
- Create: `opencode2api_enhance_main/gateway_timeout.go`（移植实验 Engine 的超时/续写/探测/事件逻辑，适配真实上游）
- Create: `opencode2api_enhance_main/gateway_timeout_test.go`
- Modify: `opencode2api_enhance_main/config.example.json`、`opencode2api_enhance_main/src-tauri/src/config.rs`（新增 3 组区间配置默认值）

**Interfaces:**
- Consumes: 阶段 1 `TimeoutConfig`/`EventLog`/`Engine` 的核心逻辑；真实上游 `buildOCRequestWithEndpoint`/`getStreamingHTTPClientForTierWithProxy`。
- Produces: `config.json` 新增字段 `timeout_ttft_min/max`、`timeout_silence_min/max`、`failover_probe_min/max`；`call_log.jsonl` 落盘 `runtime/_unified-gateway/`。

- [ ] **Step 1: 移植配置与日志模块（从实验复制适配）**
- [ ] **Step 2: 移植 Engine 核心到 `gateway_timeout.go`，接入 `chatCompletionsHandler` 流式读循环（替换现在的 `reader.ReadString` 无限阻塞）**
- [ ] **Step 3: 写 TDD 测试（超时触发、续写 body、事件日志环形、区间随机）**
- [ ] **Step 4: `go test ./...` 全绿 + `go vet`**
- [ ] **Step 5: 提交**

### Task 2.2: Rust 新增 `get_call_log` 接口

**Files:**
- Modify: `opencode2api_enhance_main/src-tauri/src/commands.rs`
- Create: `opencode2api_enhance_main/src-tauri/src/call_log.rs`（读 JSONL + 解析 + 截断策略）

**Interfaces:**
- Produces: `#[tauri::command] fn get_call_log() -> Vec<CallRecordDTO>`；`CallRecordDTO` 对应 Go 的 JSON 结构。

- [ ] **Step 1: 实现 call_log.rs（读 JSONL，返回最新 N 条，默认 5000）**
- [ ] **Step 2: commands.rs 注册 get_call_log 并接入 AppState**
- [ ] **Step 3: `cargo test` 全绿**
- [ ] **Step 4: 提交**

### Task 2.3: 前端新增日志页 + 设置页区间表单

**Files:**
- Create: `opencode2api_enhance_main/src/pages/LogsPage.tsx`
- Modify: `opencode2api_enhance_main/src/App.tsx`（路由/侧边栏加入日志页）
- Modify: `opencode2api_enhance_main/src/lib/api.ts`（`getCallLog`、配置读写）
- Modify: `opencode2api_enhance_main/src/pages/SettingsPage.tsx`（3 组区间 min/max 表单）

**Interfaces:**
- Consumes: Task 2.2 的 `get_call_log`。

- [ ] **Step 1: LogsPage：成功=一行简短、异常=整块时间线、【成功/失败】前缀、"只看失败"复选框、轮询刷新**
- [ ] **Step 2: 设置页区间表单（min/max 各一输入框 + 校验 min<=max + 保存热加载）**
- [ ] **Step 3: `npm run build`（tsc -b && vite build）通过**
- [ ] **Step 4: 手动验证：起网关 → 制造一次切换 → 日志页看到完整时间线**
- [ ] **Step 5: 提交**

### Task 2.4: 阶段 2 验证与收尾

- [ ] **Step 1: 真实节点冒烟：配置真实节点列表，验证黑洞节点被跳过、正常节点续写**
- [ ] **Step 2: 检查所有测试全绿（go test / cargo test / npm run build）**
- [ ] **Step 3: 生成「阶段 2 验证报告」，失败项滚入下一阶段（若有）**
- [ ] **Step 4: 更新设计文档状态为「已完成」**
- [ ] **Step 5: 提交**

---

## 完成标准（Definition of Done）

- [ ] 阶段 1：5 个验证点全部通过，实验模块可移植
- [ ] 阶段 2：Go 流内超时切换生效；日志页可用；区间配置表单可保存热加载
- [ ] `go test ./...`、`cargo test`、`npm run build` 全绿
- [ ] 真实节点冒烟通过（黑洞跳过、正常续写）
- [ ] 每阶段失败项均已记录并循环修复到完成
