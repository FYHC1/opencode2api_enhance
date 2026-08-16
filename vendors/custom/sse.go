// SSE 流转换管道（Anthropic / Gemini 出站协议共用）：
// 读上游原生 SSE 事件 → transform 闭包转出 OpenAI Chat chunk → 流结束 finish 闭包补尾
// （finish_reason / usage / [DONE]）。
package custom

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// chunkCtx 流 chunk 的公共头字段（首事件时从上游报文捕获）。
type chunkCtx struct {
	id    string
	model string
}

// nowUnix 当前秒级时间戳。
func nowUnix() int64 { return time.Now().Unix() }

// writeChunk 输出一条 OpenAI Chat chunk（SSE data 行）。finish 为 nil 表示中间增量。
func writeChunk(w *bytes.Buffer, cc *chunkCtx, delta map[string]any, finish *string) {
	chunk := map[string]any{
		"id":      cc.id,
		"object":  "chat.completion.chunk",
		"created": nowUnix(),
		"model":   cc.model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	w.WriteString("data: ")
	w.Write(b)
	w.WriteString("\n\n")
}

// writeUsageDone 输出末尾 usage 汇总 chunk + [DONE]（OpenAI include_usage 语义）。
func writeUsageDone(w *bytes.Buffer, cc *chunkCtx, prompt, completion float64) {
	chunk := map[string]any{
		"id":      cc.id,
		"object":  "chat.completion.chunk",
		"created": nowUnix(),
		"model":   cc.model,
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     int64(prompt),
			"completion_tokens": int64(completion),
			"total_tokens":      int64(prompt + completion),
		},
	}
	if b, err := json.Marshal(chunk); err == nil {
		w.WriteString("data: ")
		w.Write(b)
		w.WriteString("\n\n")
	}
	w.WriteString("data: [DONE]\n\n")
}

// sseConverter 把厂商原生 SSE 转为 OpenAI Chat SSE 的 io.ReadCloser。
// transform 逐事件追加输出；finish 在上游 EOF 后追加尾部。
type sseConverter struct {
	rc        io.ReadCloser
	scan      *bufio.Scanner
	out       *bytes.Buffer
	transform func(ev *sseEvent, w *bytes.Buffer)
	finish    func(w *bytes.Buffer)
	done      bool // finish 已执行
	closed    bool
}

// sseEvent 一条上游 SSE 事件（event 名 + data 载荷）。
type sseEvent struct {
	event string
	data  []byte
}

// newSSEConverter 构造转换器。
func newSSEConverter(rc io.ReadCloser, transform func(*sseEvent, *bytes.Buffer), finish func(*bytes.Buffer)) *sseConverter {
	scan := bufio.NewScanner(rc)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseConverter{rc: rc, scan: scan, out: &bytes.Buffer{}, transform: transform, finish: finish}
}

// pump 持续消费上游事件直到输出缓冲非空或 EOF。
// SSE 事件名与 data 分行到达：event 行先暂存，data 行到达时合并分发后复位。
func (c *sseConverter) pump() {
	var curEvent string
	for c.out.Len() == 0 && !c.done {
		if !c.scan.Scan() {
			if c.finish != nil {
				c.finish(c.out)
			}
			c.done = true
			return
		}
		line := c.scan.Bytes()
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			curEvent = string(line[7:])
		case bytes.HasPrefix(line, []byte("data: ")):
			data := bytes.TrimSpace(line[6:])
			if len(data) > 0 {
				c.transform(&sseEvent{event: curEvent, data: data}, c.out)
			}
			curEvent = ""
		}
	}
}

// Read 实现 io.Reader（输出缓冲空时再消费上游）。
func (c *sseConverter) Read(b []byte) (int, error) {
	if c.out.Len() == 0 {
		if c.done {
			return 0, io.EOF
		}
		c.pump()
		if c.out.Len() == 0 && c.done {
			return 0, io.EOF
		}
	}
	return c.out.Read(b)
}

// Close 实现 io.Closer。
func (c *sseConverter) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.rc.Close()
}

var _ io.ReadCloser = (*sseConverter)(nil)
