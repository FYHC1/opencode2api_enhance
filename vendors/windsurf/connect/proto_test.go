package connect

import (
	"bytes"
	"strings"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 300, 65535, 1 << 32, 1<<63 - 1} {
		enc := encodeVarint(v)
		got, n, err := decodeVarint(enc, 0)
		if err != nil || n != len(enc) || got != v {
			t.Fatalf("varint %d: got %d n=%d err=%v enc=%x", v, got, n, err, enc)
		}
	}
}

func TestEncodeFields(t *testing.T) {
	// string field 3 "hi" → key=0x1A len=2
	enc := EncodeString(3, "hi")
	want := []byte{0x1A, 0x02, 'h', 'i'}
	if !bytes.Equal(enc, want) {
		t.Fatalf("EncodeString = %x, want %x", enc, want)
	}
	encV := EncodeVarintField(8, 300)
	wantV := []byte{0x40, 0xAC, 0x02}
	if !bytes.Equal(encV, wantV) {
		t.Fatalf("EncodeVarintField = %x, want %x", encV, wantV)
	}
	encD := EncodeDoubleField(5, 1.0)
	if len(encD) != 1+8 {
		t.Fatalf("double len = %d", len(encD))
	}
	if encD[0] != 0x29 { // field 5 wire1
		t.Fatalf("double key = %02x", encD[0])
	}
}

func TestParseRoundTrip(t *testing.T) {
	msg := append(EncodeString(1, "hello"), EncodeVarintField(2, 42)...)
	raw, err := parseMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if raw.FirstString(1) != "hello" {
		t.Fatalf("field1 = %q", raw.FirstString(1))
	}
	if v, _ := raw.FirstVarint(2); v != 42 {
		t.Fatalf("field2 = %d", v)
	}
}

func TestExtractUsageFromUserStatus(t *testing.T) {
	// 构造 GetUserStatus：field13 用量信封 {14:99,15:88,17:reset} 内嵌于 >100B 的 field1 用户对象
	envelope := concat(
		EncodeVarintField(14, 99),
		EncodeVarintField(15, 88),
		EncodeVarintField(17, 1_700_000_000),
		EncodeVarintField(18, 1_800_000_000),
	)
	userObj := EncodeVarintField(10, 2) // teams tier
	userObj = append(userObj, EncodeString(7, "user1@example.com")...)
	userObj = append(userObj, EncodeString(8, strings.Repeat("x", 120))...) // 撑大 >100B
	userObj = append(userObj, EncodeMessage(13, envelope)...)
	body := EncodeMessage(1, userObj)
	top := append(body, EncodeString(2, "Free")...)

	u := ExtractUsageFromUserStatus(top)
	if !u.HasDailyPct || u.DailyRemainingPct != 99 {
		t.Fatalf("daily = %d (%v), want 99", u.DailyRemainingPct, u.HasDailyPct)
	}
	if !u.HasWeeklyPct || u.WeeklyRemainingPct != 88 {
		t.Fatalf("weekly = %d (%v), want 88", u.WeeklyRemainingPct, u.HasWeeklyPct)
	}
	if u.DailyResetUnix != 1_700_000_000 || u.WeeklyResetUnix != 1_800_000_000 {
		t.Fatalf("reset = %d/%d", u.DailyResetUnix, u.WeeklyResetUnix)
	}
	if u.PlanName != "Free" {
		t.Fatalf("plan = %q, want Free", u.PlanName)
	}
	if u.TeamsTier != 2 {
		t.Fatalf("tier = %d, want 2", u.TeamsTier)
	}
	if u.Email != "user1@example.com" {
		t.Fatalf("email = %q, want user1@example.com", u.Email)
	}
}

func fake() string { return string(bytes.Repeat([]byte("x"), 120)) }

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// EncodeMessage 是 EncodeMessageField 的简写。
func EncodeMessage(field uint32, msg []byte) []byte {
	return EncodeMessageField(field, msg)
}

// EnvelopeVarint 是一个便捷封装（仅测试用）。
func EnvelopeVarint(field uint32, v uint64) []byte { return EncodeVarintField(field, v) }
