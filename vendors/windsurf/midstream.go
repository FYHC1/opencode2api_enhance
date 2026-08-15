// 流中无感换号（P3-B7）。
//
// 把"单个账号的 SSE 流"包装成"自动换号续写流"：流内出现错误事件或提前中断
// （未收到 [DONE]）时，已吐内容作为上下文回卷，换下一个健康账号重发续写，
// 用户无感。换号逻辑与 core/gateway 断点续写（gateway_timeout.go 的
// buildResumeBody）口径一致：assistant(已吐内容) + user("请继续……")。
// 仅在全部账号都失败时，才把错误作为最后一个 SSE 事件上抛给调用方
// （core 的 streamWithResume 会据此做厂商级兜底）。
package windsurf

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// maxMidStreamSwitch 流内无感换号的最多换号次数（初始流之外追加的流段数上限）。
// 与 Chat 的非流式重试（2 次）口径相当：给 2 次换号机会，避免无限换号。
const maxMidStreamSwitch = 2

// midstreamContinuePrompt 续写提示词，与 core/gateway 断点续写同文，保证口径一致。
const midstreamContinuePrompt = "请继续上面的回复，从中断处接着写。"

// midStreamSwitch 实现 io.ReadCloser：内部维护"当前账号流"，自动换号续写。
type midStreamSwitch struct {
	v    *Vendor
	ctx  context.Context
	orig *contract.Message // 原始归一化请求（换号时基于它追加 assistant 上下文）

	mu       sync.Mutex
	src      io.ReadCloser // 当前账号的底层流
	rd       *bufio.Reader // 当前流的行读取器
	acct     string        // 当前账号（邮箱）
	attempts int           // 已执行的换号次数
	segments int           // 已开启的流段数（初始 1）

	accumulated string   // 已吐给调用方的可见内容（续写上下文；reasoning 不计入）
	pending     []string // 待输出行（不含换行符）
	done        bool     // 已正常结束（见到 [DONE] 或已上抛终态错误）
	closed      bool
	lastErr     string // 最近一次换号失败原因（供日志/终态）
}

// newMidStreamSwitch 构造流内换号包装器（接管 src 与 acct 的生命周期）。
func newMidStreamSwitch(v *Vendor, ctx context.Context, msg *contract.Message, src io.ReadCloser, acct string) *midStreamSwitch {
	return &midStreamSwitch{
		v: v, ctx: ctx, orig: msg,
		src: src, rd: bufio.NewReader(src),
		acct:     acct,
		segments: 1,
	}
}

// Read 实现 io.Reader：逐行读取底层流；发现错误/中断时自动换号续写。
// 锁策略（CONC-6）：网络读（nextLine）与换号网段（oneSwitch）不持 m.mu——
// Read 只在大括号临界区（取 pending / 查 closed / 提交状态）持锁，
// 因此 Close 总能及时拿到锁（关 src 解除 Read 的阻塞读），不会被网络 IO 饿死。
// pending/done/accumulated 等仅由本 Read 协程访问，无需锁。
func (m *midStreamSwitch) Read(p []byte) (int, error) {
	for {
		// 锁内：查关闭 / 取待发数据
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if len(m.pending) > 0 {
			line := m.pending[0]
			m.pending = m.pending[1:]
			m.mu.Unlock()
			n := copy(p, line)
			if n < len(line) {
				// 目标缓冲区不足：剩余部分放回队首，下次再取
				m.pending = append([]string{line[n:]}, m.pending...)
			}
			return n, nil
		}
		m.mu.Unlock()

		// 锁外：网络读一行（可能阻塞；Close 可并发拿锁关 src）
		line, err := m.nextLine()

		// 锁内：处理读到的行 / 决定是否换号
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if err != nil {
			m.mu.Unlock()
			if err == io.EOF {
				if m.done {
					return 0, io.EOF
				}
				// 未见过 [DONE] 即 EOF：视为流中断，换号续写
				if serr := m.trySwitch("stream ended without [DONE]"); serr != nil {
					m.surfaceFinalError(serr)
					continue
				}
				continue
			}
			// 传输层读错误：同样视为可换号信号
			if serr := m.trySwitch("stream read error: " + err.Error()); serr != nil {
				m.surfaceFinalError(serr)
				continue
			}
			continue
		}
		if line == "" {
			m.mu.Unlock()
			continue
		}
		if midstreamIsErrorLine(line) {
			// 上游显式错误事件（capacity/限流等）→ 换号；错误行不转发（无感）
			m.mu.Unlock()
			if serr := m.trySwitch(midstreamErrorText(line)); serr != nil {
				m.surfaceFinalError(serr)
				continue
			}
			continue
		}
		// 转发行必须保留 '\n' 终止符：core/gateway 的 streamWithResume
		// 用 bufio.Reader.ReadString('\n') 逐行读取，缺换行会导致行粘连。
		m.accumulate(line)
		if midstreamIsDoneLine(line) {
			m.done = true
		}
		m.pending = append(m.pending, line+"\n")
		m.mu.Unlock()
	}
}

// Close 实现 io.Closer：关闭当前流，归还当前账号（不标记耗尽）。
// 锁内只置 closed + 取出 src/acct 引用；关流与还账号在锁外——
// 关 src 会解除 Read 的阻塞读，且本函数不等待任何网络 IO。
func (m *midStreamSwitch) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	src := m.src
	m.src = nil
	acct := m.acct
	m.acct = ""
	m.mu.Unlock()

	if src != nil {
		_ = src.Close()
	}
	// 归还当前账号：仅更新 LastUsedAt，不进入冷却（客户端主动断开不是账号故障）
	if acct != "" {
		m.v.pool.release(acct, time.Now(), false)
	}
	return nil
}

// nextLine 从当前流读一行（不含换行符）。处理"末尾无换行"的残段。
func (m *midStreamSwitch) nextLine() (string, error) {
	line, err := m.rd.ReadString('\n')
	if err != nil && len(line) > 0 {
		// 末尾残段：本行先返回，下一调用再报 err
		return strings.TrimSuffix(line, "\n"), nil
	}
	return strings.TrimSuffix(line, "\n"), err
}

// trySwitch 尝试换号续写（最多 maxMidStreamSwitch 次；每次失败耗一个账号）。
// 由 Read 在锁外调用（换号网段不持 m.mu）。
func (m *midStreamSwitch) trySwitch(reason string) error {
	for m.attempts < maxMidStreamSwitch {
		if err := m.oneSwitch(reason); err == nil {
			return nil
		} else {
			slog.Warn("windsurf: 流中换号失败", "err", err)
			m.lastErr = err.Error()
			reason = err.Error()
		}
	}
	if m.lastErr != "" {
		return errors.New("windsurf: 流中换号失败: " + m.lastErr)
	}
	return errors.New("windsurf: 流中换号已达上限")
}

// oneSwitch 执行一次换号：标旧号耗尽 → 借新号 → 以已吐内容为上下文续接请求。
// 网络段（markExhausted/Acquire/DoChatStream）不持 m.mu；commit 段锁内校验
// 未关闭才安装新流（换号期间被 Close → 丢弃新流、归还新号，不安装）。
func (m *midStreamSwitch) oneSwitch(reason string) error {
	m.attempts++
	// 1) 锁内取当前账号并清空（避免与 Close 竞态）；锁外标旧号耗尽+冷却（触发后台预注册）
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("windsurf: 流已关闭")
	}
	old := m.acct
	m.acct = ""
	m.mu.Unlock()
	if old != "" {
		m.v.markExhausted(contract.AcctID(old))
	}
	// 2) 借新账号（受冷却与健康约束）——锁外
	acct, err := m.v.Acquire()
	if err != nil {
		m.lastErr = fmt.Sprintf("无可换账号: %v", err)
		return err
	}
	token := m.v.pool.tokenOf(string(acct))
	if token == "" {
		m.v.pool.release(string(acct), time.Now(), true)
		m.lastErr = "新账号无会话令牌"
		return errors.New("windsurf: 换号账号无会话令牌")
	}
	// 3) 续接请求：原消息 + assistant(已吐内容) + user(请继续)——锁外网络
	nextMsg := resumeMessageOf(m.orig, m.accumulated)
	stream, err := m.v.cfg.Chatter.DoChatStream(m.ctx, token, nextMsg)
	if err != nil || stream == nil {
		if stream != nil {
			_ = stream.Close()
		}
		m.v.markExhausted(contract.AcctID(acct))
		m.lastErr = fmt.Sprintf("换号重连失败: %v", err)
		return fmt.Errorf("windsurf: 换号重连失败: %v", err)
	}
	// 4) commit 段：锁内校验未关闭才替换当前流
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		if stream.ReadCloser != nil {
			_ = stream.ReadCloser.Close()
		}
		m.v.pool.release(string(acct), time.Now(), false)
		m.lastErr = "换号期间流已关闭"
		return errors.New("windsurf: 换号期间流已关闭")
	}
	oldSrc := m.src
	m.src = stream.ReadCloser
	m.rd = bufio.NewReader(m.src)
	m.acct = string(acct)
	m.segments++
	m.mu.Unlock()
	if oldSrc != nil {
		_ = oldSrc.Close()
	}
	m.v.pool.touch(string(acct), time.Now())
	slog.Info("windsurf: 流中无感换号",
		"reason", reason,
		"accumulated_chars", len(m.accumulated),
		"segment", m.segments,
		"email", maskEmail(string(acct)))
	return nil
}

// surfaceFinalError 全部账号失败：把错误作为最后一个 SSE 事件上抛。
// pending 行以 '\n' 结尾（core 侧按行读取）。由 Read 协程在锁外调用
// （pending/done 仅该协程访问）。
func (m *midStreamSwitch) surfaceFinalError(err error) {
	if m.done {
		return
	}
	m.done = true
	m.pending = append(m.pending, midstreamErrorLine(err)+"\n")
}

// accumulate 从 content 行累积已吐内容（与 core 断点续写一致：只算可见 content）。
func (m *midStreamSwitch) accumulate(line string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line[6:]), &obj) != nil {
		return
	}
	chs, ok := obj["choices"].([]any)
	if !ok || len(chs) == 0 {
		return
	}
	first, ok := chs[0].(map[string]any)
	if !ok {
		return
	}
	delta, ok := first["delta"].(map[string]any)
	if !ok {
		return
	}
	if c, ok := delta["content"].(string); ok {
		m.accumulated += c
	}
}

// midstreamIsDoneLine 判断 SSE 行是否为结束标记。
func midstreamIsDoneLine(line string) bool {
	return line == "data: [DONE]" || line == "[DONE]"
}

// midstreamIsErrorLine 判断 SSE 行是否为错误事件（data: {"error": ...}）。
func midstreamIsErrorLine(line string) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line[6:]), &obj) != nil {
		return false
	}
	_, ok := obj["error"]
	return ok
}

// midstreamErrorText 提取错误事件的可读信息。
func midstreamErrorText(line string) string {
	if !strings.HasPrefix(line, "data: ") {
		return "upstream stream error"
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line[6:]), &obj) == nil {
		if e, ok := obj["error"]; ok {
			if em, ok := e.(map[string]any); ok {
				if s, ok := em["message"].(string); ok && s != "" {
					return s
				}
			}
			if s, ok := e.(string); ok && s != "" {
				return s
			}
		}
	}
	return "upstream stream error"
}

// midstreamErrorLine 构造 OpenAI 错误形态的 SSE 事件行（终态上报用）。
func midstreamErrorLine(err error) string {
	msg := "windsurf stream failed"
	if err != nil {
		msg = err.Error()
	}
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"type": "midstream_error", "message": msg},
	})
	return "data: " + string(b)
}

// resumeMessageOf 构造换号续接请求：原消息 + assistant(已吐内容) + user(请继续)。
func resumeMessageOf(orig *contract.Message, accumulated string) *contract.Message {
	if accumulated == "" {
		return orig // 尚未吐内容：直接换新号重新请求原消息即可
	}
	out := *orig
	out.Messages = make([]contract.Msg, 0, len(orig.Messages)+2)
	out.Messages = append(out.Messages, orig.Messages...)
	out.Messages = append(out.Messages,
		contract.Msg{Role: "assistant", Content: accumulated},
		contract.Msg{Role: "user", Content: midstreamContinuePrompt},
	)
	return &out
}
