// Package connect 移植自 windsurf-account-manager 的 Connect-RPC 客户端
// （src-tauri/src/proto_min.rs / devin_connect.rs）。
// 仅依赖 Go 标准库：最小 protobuf（varint / wire 0/1/2/5）+ Connect 帧 + 用量解析。
package connect

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// 编码
// ---------------------------------------------------------------------------

func encodeVarint(n uint64) []byte {
	var out []byte
	for n > 0x7F {
		out = append(out, byte(n)&0x7F|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func decodeVarint(data []byte, i int) (uint64, int, error) {
	var shift uint32
	var n uint64
	for {
		if i >= len(data) {
			return 0, i, errors.New("truncated varint")
		}
		b := data[i]
		i++
		n |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return n, i, nil
		}
		shift += 7
		if shift > 63 {
			return 0, i, errors.New("varint too long")
		}
	}
}

// EncodeString 编码 string 字段（wire type 2）。
func EncodeString(field uint32, value string) []byte {
	return EncodeBytes(field, []byte(value))
}

// EncodeBytes 编码 bytes 字段（wire type 2）。
func EncodeBytes(field uint32, raw []byte) []byte {
	out := append([]byte{}, encodeVarint(uint64(field)<<3|2)...)
	out = append(out, encodeVarint(uint64(len(raw)))...)
	return append(out, raw...)
}

// EncodeVarintField 编码 uvarint 字段（wire type 0）。
func EncodeVarintField(field uint32, value uint64) []byte {
	out := append([]byte{}, encodeVarint(uint64(field)<<3|0)...)
	return append(out, encodeVarint(value)...)
}

// EncodeDoubleField 编码 double 字段（wire type 1，little-endian）。
func EncodeDoubleField(field uint32, value float64) []byte {
	out := append([]byte{}, encodeVarint(uint64(field)<<3|1)...)
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(value))
	return append(out, b[:]...)
}

// EncodeMessageField 编码嵌套 message 字段（wire type 2，msg 已编码）。
func EncodeMessageField(field uint32, msg []byte) []byte {
	return EncodeBytes(field, msg)
}

// ---------------------------------------------------------------------------
// 解码
// ---------------------------------------------------------------------------

// RawMessage 是一次 protobuf 解析结果（repeated → 多值）。
type RawMessage struct {
	Strings map[uint32][]string
	Varints map[uint32][]uint64
	Bytes   map[uint32][][]byte
}

func newRaw() RawMessage {
	return RawMessage{
		Strings: map[uint32][]string{},
		Varints: map[uint32][]uint64{},
		Bytes:   map[uint32][][]byte{},
	}
}

// parseMessage 通用解析（bytes 同时按 UTF8 成功与否存 strings/bytes）。
func parseMessage(data []byte) (RawMessage, error) {
	out := newRaw()
	i := 0
	for i < len(data) {
		key, ni, err := decodeVarint(data, i)
		if err != nil {
			return out, err
		}
		i = ni
		field := uint32(key >> 3)
		switch key & 7 {
		case 0:
			v, ni, err := decodeVarint(data, i)
			if err != nil {
				return out, err
			}
			i = ni
			out.Varints[field] = append(out.Varints[field], v)
		case 1:
			if i+8 > len(data) {
				return out, nil
			}
			i += 8
		case 5:
			if i+4 > len(data) {
				return out, nil
			}
			i += 4
		case 2:
			ln, ni, err := decodeVarint(data, i)
			if err != nil {
				return out, err
			}
			i = ni
			if i+int(ln) > len(data) {
				return out, errors.New("truncated length-delimited")
			}
			raw := append([]byte(nil), data[i:i+int(ln)]...)
			i += int(ln)
			out.Bytes[field] = append(out.Bytes[field], raw)
			if utf8.Valid(raw) {
				out.Strings[field] = append(out.Strings[field], string(raw))
			}
		default:
			return out, nil
		}
	}
	return out, nil
}

func (m *RawMessage) firstString(field uint32) string {
	if v := m.Strings[field]; len(v) > 0 {
		return v[0]
	}
	if v := m.Bytes[field]; len(v) > 0 {
		return string(v[0])
	}
	return ""
}

func (m *RawMessage) firstVarint(field uint32) (uint64, bool) {
	if v := m.Varints[field]; len(v) > 0 {
		return v[0], true
	}
	return 0, false
}

func firstBytes(b map[uint32][][]byte, field uint32) []byte {
	if v := b[field]; len(v) > 0 {
		return v[0]
	}
	return nil
}

// ---------------------------------------------------------------------------
// 用量解析（GetUserStatus → UsageFields）
// ---------------------------------------------------------------------------

// UsageFields 是从 GetUserStatus 体提取的用量。
type UsageFields struct {
	PlanName           string
	RemainingMessages  uint64
	TotalMessages      uint64
	DailyRemainingPct  uint64
	WeeklyRemainingPct uint64
	DailyResetUnix     uint64
	WeeklyResetUnix    uint64
	TeamsTier          uint64
	Email              string
	HasDailyPct        bool
	HasWeeklyPct       bool
	HasMessages        bool
}

func isPlanName(s string) bool {
	switch s {
	case "Free", "Pro", "Max", "Teams", "Enterprise", "Trial":
		return true
	}
	return false
}

func applyUsageVarints(m *RawMessage, out *UsageFields) {
	if v, ok := m.firstVarint(8); ok && v > 0 && v <= 1_000_000 {
		out.RemainingMessages = v
		out.HasMessages = true
	}
	if v, ok := m.firstVarint(14); ok && v <= 100 {
		out.DailyRemainingPct = v
		out.HasDailyPct = true
	}
	if v, ok := m.firstVarint(15); ok && v <= 100 {
		out.WeeklyRemainingPct = v
		out.HasWeeklyPct = true
	}
	if v, ok := m.firstVarint(17); ok && v > 1_600_000_000 {
		out.DailyResetUnix = v
	}
	if v, ok := m.firstVarint(18); ok && v > 1_600_000_000 {
		out.WeeklyResetUnix = v
	}
}

func applyPlanMessage(m *RawMessage, out *UsageFields) {
	if name := m.firstString(2); isPlanName(name) {
		out.PlanName = name
	}
	if t, ok := m.firstVarint(1); ok && t < 1000 {
		out.TeamsTier = t
	}
	if v, ok := m.firstVarint(10); ok && v >= 100 && v <= 1_000_000 {
		out.TotalMessages = v
	}
	if v, ok := m.firstVarint(12); ok && v >= 1 && v <= 1_000_000 {
		out.RemainingMessages = v
		out.HasMessages = true
	}
}

// ExtractUsageFromUserStatus 从 GetUserStatus 响应体提取用量。
func ExtractUsageFromUserStatus(data []byte) UsageFields {
	var out UsageFields

	top, err := parseMessage(data)
	if err != nil {
		return out
	}

	// 顶层 plan 快照（T2）
	if p2 := firstBytes(top.Bytes, 2); len(p2) > 0 {
		if sub, err := parseMessage(p2); err == nil {
			applyPlanMessage(&sub, &out)
		}
	}
	if name := top.firstString(2); isPlanName(name) {
		out.PlanName = name
	}

	// 展开外层用户对象（优先大块 field 1）
	body := data
	if inner := firstBytes(top.Bytes, 1); len(inner) > 100 {
		body = inner
	}
	msg, err := parseMessage(body)
	if err != nil {
		return out
	}
	for _, f := range msg.Strings {
		for _, s := range f {
			if len(s) > 0 && strings_containsAt(s) {
				out.Email = s
				break
			}
		}
		if out.Email != "" {
			break
		}
	}
	if t, ok := msg.firstVarint(10); ok && t < 1000 {
		out.TeamsTier = t
	}

	// 用量信封（field 13）
	if env := firstBytes(msg.Bytes, 13); len(env) > 0 {
		if em, err := parseMessage(env); err == nil {
			if p := firstBytes(em.Bytes, 1); len(p) > 0 {
				if pm, err := parseMessage(p); err == nil {
					applyPlanMessage(&pm, &out)
				}
			}
			if name := em.firstString(2); isPlanName(name) {
				out.PlanName = name
			}
			applyUsageVarints(&em, &out)
			return out
		}
	}
	applyUsageVarints(&msg, &out)
	return out
}

func strings_containsAt(s string) bool {
	for _, r := range s {
		if r == '@' {
			return true
		}
	}
	return false
}
