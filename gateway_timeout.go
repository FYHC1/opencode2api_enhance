package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ======================== 流内超时配置（区间随机） ========================
// 从 model-gateway 分析结论：SSE 流一旦建立，现有代码无 TTFT/静默超时，
// 上游挂起时 reader.ReadString 无限阻塞。本模块为每个请求在 [min,max]
// 区间内随机取一个超时值，避免固定超时被上游识别为定时扫描/竞速探测，
// 同时 min 下限保护防止过密重试。

// 区间默认值（生产）
const (
	DefaultTTFTMin    = 15 * time.Second
	DefaultTTFTMax    = 25 * time.Second
	DefaultSilenceMin = 30 * time.Second
	DefaultSilenceMax = 60 * time.Second
	DefaultProbeMin   = 2
	DefaultProbeMax   = 3
	// DefaultCallLogMax 日志保留上限（条），前端设置页可改
	DefaultCallLogMax = 5000
)

type TimeoutConfig struct {
	TTFTRange    [2]time.Duration
	SilenceRange [2]time.Duration
	ProbeRange   [2]int
}

func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		TTFTRange:    [2]time.Duration{DefaultTTFTMin, DefaultTTFTMax},
		SilenceRange: [2]time.Duration{DefaultSilenceMin, DefaultSilenceMax},
		ProbeRange:   [2]int{DefaultProbeMin, DefaultProbeMax},
	}
}

// randDuration 返回 [min,max] 均匀随机值（含端点）
func randDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int64N(int64(max-min)+1))
}

func (c TimeoutConfig) RandomTTFT() time.Duration {
	return randDuration(c.TTFTRange[0], c.TTFTRange[1])
}

func (c TimeoutConfig) RandomSilence() time.Duration {
	return randDuration(c.SilenceRange[0], c.SilenceRange[1])
}

func (c TimeoutConfig) RandomProbeN() int {
	if c.ProbeRange[1] <= c.ProbeRange[0] {
		return c.ProbeRange[0]
	}
	return c.ProbeRange[0] + rand.IntN(c.ProbeRange[1]-c.ProbeRange[0]+1)
}

// ======================== 全流程调用日志 ========================
// 记录每个请求的完整决策链：接口/模型/节点/路由模式/连接结果/超时/切换/结果。
// 前端日志页按「成功一行简短、异常整块详细」渲染，每条以【成功】/【失败】开头。

type CallEvent struct {
	Type   string    `json:"type"`
	Node   string    `json:"node,omitempty"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

type CallRecord struct {
	ReqID         string      `json:"req_id"`
	TS            string      `json:"ts"`
	Path          string      `json:"path"`
	Model         string      `json:"model"`
	Stream        bool        `json:"stream"`
	RouteMode     string      `json:"route_mode"`
	Nodes         []string    `json:"nodes"`
	Events        []CallEvent `json:"events"`
	Status        string      `json:"status"`
	PromptTok     int64       `json:"prompt_tokens"`
	CompletionTok int64       `json:"completion_tokens"`
	DurationMS    int64       `json:"duration_ms"`
	ErrMsg        string      `json:"err_msg,omitempty"`
}

func CallStatusText(rec CallRecord) string {
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

// LoadCallLogFromFile 从 JSONL 恢复（重启后读取历史）
func LoadCallLogFromFile(path string) (*EventLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEventLog(DefaultCallLogMax), nil
		}
		return nil, err
	}
	l := NewEventLog(DefaultCallLogMax)
	for _, line := range splitJSONLines(data) {
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

func splitJSONLines(b []byte) [][]byte {
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

// ======================== 全局状态 ========================

var (
	timeoutCfg     = DefaultTimeoutConfig()
	callLog        = NewEventLog(DefaultCallLogMax)
	callLogPath    = "call_log.jsonl"
	callLogMu      sync.RWMutex
	callLogEnabled = true // 仅网关/代理池模式启用（避免直连实例产生无人读取的日志）
)

// ======================== SSE 调试（诊断 JSON 拼接） ========================
// 临时诊断工具：把流式转发收到的原始行与转发行写入 sse_debug.log，
// 便于定位 "Unexpected non-whitespace character after JSON" 的拼接现场。
var (
	sseDebugMu   sync.Mutex
	sseDebugFile *os.File
)

// sseDebugf 追加一行到 sse_debug.log（失败静默）
func sseDebugf(format string, args ...any) {
	sseDebugMu.Lock()
	defer sseDebugMu.Unlock()
	if sseDebugFile == nil {
		f, err := os.OpenFile("sse_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		sseDebugFile = f
	}
	fmt.Fprintf(sseDebugFile, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}

// closeSSEDebug 关闭调试文件（进程退出时调用）
func closeSSEDebug() {
	sseDebugMu.Lock()
	defer sseDebugMu.Unlock()
	if sseDebugFile != nil {
		sseDebugFile.Close()
		sseDebugFile = nil
	}
}

// initCallLog 在进程启动时加载历史并启用落盘
func initCallLog() {
	loaded, err := LoadCallLogFromFile(callLogPath)
	if err == nil {
		callLog = loaded
	}
	callLog.SetPath(callLogPath)
}

// recordCall 便捷包装：追加一条调用日志（失败不阻塞请求）
func recordCall(rec CallRecord) {
	if !callLogEnabled {
		return
	}
	if err := callLog.Append(rec); err != nil {
		slog.Error("call log append failed", "error", err)
	}
}

// setTimeoutConfigFromApp 从 AppConfig 读取区间配置并应用（热加载）
func setTimeoutConfigFromApp(cfg AppConfig) {
	if cfg.TTFTMinMS > 0 && cfg.TTFTMaxMS >= cfg.TTFTMinMS {
		timeoutCfg.TTFTRange = [2]time.Duration{
			time.Duration(cfg.TTFTMinMS) * time.Millisecond,
			time.Duration(cfg.TTFTMaxMS) * time.Millisecond,
		}
	}
	if cfg.SilenceMinMS > 0 && cfg.SilenceMaxMS >= cfg.SilenceMinMS {
		timeoutCfg.SilenceRange = [2]time.Duration{
			time.Duration(cfg.SilenceMinMS) * time.Millisecond,
			time.Duration(cfg.SilenceMaxMS) * time.Millisecond,
		}
	}
	if cfg.ProbeMin > 0 && cfg.ProbeMax >= cfg.ProbeMin {
		timeoutCfg.ProbeRange = [2]int{cfg.ProbeMin, cfg.ProbeMax}
	}
	if cfg.CallLogMax > 0 {
		callLogMu.Lock()
		callLog = NewEventLog(cfg.CallLogMax)
		callLog.SetPath(callLogPath)
		callLogMu.Unlock()
	}
}

// ======================== 流内超时 + 断点续写切换 ========================
// 阶段1实验验证过的核心逻辑落地：SSE 读循环加 TTFT/静默计时，
// 超时或流中断时把已吐内容作为上下文续写，重新请求上游（自动换健康代理）。

// resumeStreamResult 描述一次流式转发的最终结果
type resumeStreamResult struct {
	OK         bool   // 是否成功完成（读到 [DONE] 或 EOF）
	Switched   bool   // 是否发生过节点切换
	PromptTok  int64  // 最终 usage
	Completion int64
	ErrMsg     string
	DoneAt     time.Time
}

// streamWithResume 从初始上游响应开始，带 TTFT/静默超时地读取 SSE 并转发。
// 超时/中断时续写重试（最多 maxResume 次）。返回结果供调用方记录日志。
//
// 参数：
//   - w: 客户端响应写入器
//   - r: 客户端请求（用于取消上下文）
//   - upstreamBody: 原始上游请求体（续写时基于它构造新 body）
//   - model: 模型 ID
//   - auth: 上游鉴权
//   - initial: 初始上游响应（可能为 nil，此时直接尝试重连）
//   - keepReasoning: 是否保留 reasoning 内容
//   - callRec: 调用日志记录（追加事件）
func streamWithResume(w http.ResponseWriter, r *http.Request, upstreamBody []byte, model string, auth UpstreamAuth, initial io.ReadCloser, keepReasoning bool, callRec *CallRecord) resumeStreamResult {
	reqID := ""
	if callRec != nil {
		reqID = callRec.ReqID
	}
	maxResume := maxRouteRetries() // 复用现有重试上限
	if maxResume > 3 {
		maxResume = 3 // 续写重试上限，避免无限循环
	}
	attempt := 0
	res := resumeStreamResult{DoneAt: time.Now()}
	accumulated := ""
	// 已有部分内容时，通过续写 body 重连
	currentBody := upstreamBody
	sseDebugf("[%s] streamWithResume start, model=%s, keepReasoning=%v", reqID, model, keepReasoning)

	// 当前活动的上游响应；attempt 0 用 initial，后续用重连结果
	upResp := initial
	proxyAddr := ""
	doneSeen := false

	for attempt <= maxResume {
		// 若需要重连（initial 为 nil 或上次超时）
		if upResp == nil {
			var err error
			upResp, _, _, proxyAddr, err = callOpenCodeAPIStream(currentBody, model, auth)
			if err != nil {
				res.ErrMsg = err.Error()
				callRec.Events = append(callRec.Events, CallEvent{Type: "connect_error", Node: proxyAddr, Detail: err.Error(), At: time.Now()})
				attempt++
				continue
			}
			callRec.Events = append(callRec.Events, CallEvent{Type: "connect_ok", Node: proxyAddr, Detail: "reconnected", At: time.Now()})
			callRec.Nodes = append(callRec.Nodes, proxyAddr)
		}

		reader := bufio.NewReader(upResp)
		ttft := timeoutCfg.RandomTTFT()
		silence := timeoutCfg.RandomSilence()
		gotFirst := false

		// 常驻读 goroutine：阻塞读转 channel，主循环 select timer
		type lineResult struct {
			line string
			err  error
		}
		lineCh := make(chan lineResult, 1)
		readDone := make(chan struct{})
		stopRead := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				ln, er := reader.ReadString('\n')
				select {
				case lineCh <- lineResult{ln, er}:
				case <-stopRead:
					return
				}
				if er != nil {
					return
				}
			}
		}()

		var lastUsage map[string]any
		interrupted := false

	readLoop:
		for {
			dur := silence
			if !gotFirst {
				dur = ttft
			}
			timer := time.NewTimer(dur)
			select {
			case resLine := <-lineCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if resLine.err != nil {
					if resLine.err == io.EOF {
						// 正常 EOF：若见过 [DONE] 视为成功，否则视为中断
						if !doneSeen {
							interrupted = true
							res.ErrMsg = "EOF without [DONE]"
							callRec.Events = append(callRec.Events, CallEvent{Type: "stream_interrupt", Node: proxyAddr, Detail: "EOF without [DONE]", At: time.Now()})
						}
						break readLoop
					}
					interrupted = true
					res.ErrMsg = resLine.err.Error()
					callRec.Events = append(callRec.Events, CallEvent{Type: "stream_error", Node: proxyAddr, Detail: resLine.err.Error(), At: time.Now()})
					break readLoop
				}
				line := strings.TrimSpace(resLine.line)
				sseDebugf("[%s] RAW<< %q", reqID, resLine.line)
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "data: [DONE]") || line == "[DONE]" {
					doneSeen = true
					sseDebugf("[%s] DONE>> %q", reqID, "data: [DONE]\n\n")
					w.Write([]byte("data: [DONE]\n\n"))
					flushWriter(w)
					interrupted = false
					break readLoop
				}
				if !strings.HasPrefix(line, "data: ") {
					// 非 data 行原样转发（如 event:/id:）
					sseDebugf("[%s] META>> %q", reqID, resLine.line)
					w.Write([]byte(resLine.line))
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					continue
				}
				dataStr := line[6:]
				// 累积内容（续写用）——从原始 JSON 提取
				var obj map[string]any
				if json.Unmarshal([]byte(dataStr), &obj) == nil {
					if u, ok := obj["usage"].(map[string]any); ok {
						lastUsage = u
					}
					if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
						if first, ok := chs[0].(map[string]any); ok {
							if delta, ok := first["delta"].(map[string]any); ok {
								if c, ok := delta["content"].(string); ok {
									accumulated += c
								}
								if rc, ok := delta["reasoning_content"].(string); ok {
									accumulated += rc
								}
							}
						}
					}
				} else {
					sseDebugf("[%s] !! JSON parse fail on data payload: %q", reqID, dataStr)
				}
				if !gotFirst {
					gotFirst = true
				}
				// 转发：复用现有转换（清洗 delta/usage/cost 字段），保持协议兼容
				out, chunkUsage := convertStreamChunkWithUsage(line, keepReasoning)
				if chunkUsage != nil {
					if tt, _ := chunkUsage["total_tokens"].(float64); tt > 0 {
						lastUsage = chunkUsage
					}
				}
				sseDebugf("[%s] FWD>> %q", reqID, out)
				w.Write([]byte(out))
				w.Write([]byte("\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			case <-timer.C:
				if !gotFirst {
					res.ErrMsg = fmt.Sprintf("TTFT timeout (%v)", ttft)
					callRec.Events = append(callRec.Events, CallEvent{Type: "ttft_timeout", Node: proxyAddr, Detail: res.ErrMsg, At: time.Now()})
				} else {
					res.ErrMsg = fmt.Sprintf("silence timeout (%v)", silence)
					callRec.Events = append(callRec.Events, CallEvent{Type: "silence_timeout", Node: proxyAddr, Detail: res.ErrMsg, At: time.Now()})
				}
				interrupted = true
				break readLoop
			case <-r.Context().Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				interrupted = true
				res.ErrMsg = "client disconnected"
				break readLoop
			}
		}

		// 关闭当前流：先通知读 goroutine 退出，再关连接，最后等 goroutine 结束
		close(stopRead)
		upResp.Close()
		<-readDone

		if lastUsage != nil {
			if pt, _ := lastUsage["prompt_tokens"].(float64); pt > 0 {
				res.PromptTok = int64(pt)
			}
			if ct, _ := lastUsage["completion_tokens"].(float64); ct > 0 {
				res.Completion = int64(ct)
			}
		}

		if !interrupted {
			res.OK = true
			res.DoneAt = time.Now()
			return res
		}

		// 中断：记录切换事件，续写重试
		res.Switched = true
		attempt++
		if attempt > maxResume {
			res.OK = false
			res.ErrMsg = "所有候选节点均失败，回复中断"
			callRec.Events = append(callRec.Events, CallEvent{Type: "all_failed", Node: proxyAddr, Detail: res.ErrMsg, At: time.Now()})
			return res
		}
		callRec.Events = append(callRec.Events, CallEvent{Type: "switch", Node: proxyAddr, Detail: fmt.Sprintf("switching (resume, accumulated=%d chars)", len(accumulated)), At: time.Now()})
		// 续写 body：原 messages + assistant(已吐内容) + user(请继续)
		if len(accumulated) > 0 {
			var bodyMap map[string]any
			if json.Unmarshal(currentBody, &bodyMap) == nil {
				msgs, _ := bodyMap["messages"].([]any)
				msgs = append(msgs,
					map[string]any{"role": "assistant", "content": accumulated},
					map[string]any{"role": "user", "content": "请继续上面的回复，从中断处接着写。"},
				)
				bodyMap["messages"] = msgs
				if b, err := json.Marshal(bodyMap); err == nil {
					currentBody = b
				}
			}
		}
		// 向客户端发一条切换提示（可观察）
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"[已切换节点，续写]\"}}]}\n\n"))
		flushWriter(w)
		// 下一轮重连
		upResp = nil
	}

	res.OK = false
	res.ErrMsg = "所有候选节点均失败，回复中断"
	return res
}

func flushWriter(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// buildResumeBody 供测试使用的续写 body 构造（独立于 streamWithResume 内部逻辑）
func buildResumeBody(body []byte, accumulated string) []byte {
	var bodyMap map[string]any
	if json.Unmarshal(body, &bodyMap) != nil {
		return body
	}
	msgs, _ := bodyMap["messages"].([]any)
	msgs = append(msgs,
		map[string]any{"role": "assistant", "content": accumulated},
		map[string]any{"role": "user", "content": "请继续上面的回复，从中断处接着写。"},
	)
	bodyMap["messages"] = msgs
	b, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return b
}