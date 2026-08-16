// 多 key 池测试：调度策略、冷却/禁用状态机、请求链路换 key。
package custom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

func TestKeyPoolRoundRobinRotation(t *testing.T) {
	p := newKeyPool([]string{"k1", "k2", "k3"}, StrategyRoundRobin)
	seen := map[string]int{}
	tried := map[int]bool{}
	for i := 0; i < 6; i++ {
		k, _, ok := p.tryAcquire(map[int]bool{})
		if !ok {
			t.Fatal("acquire failed")
		}
		seen[k]++
	}
	if len(seen) != 3 {
		t.Fatalf("round_robin must spread across keys: %v", seen)
	}
	for k, n := range seen {
		if n != 2 {
			t.Fatalf("key %s used %d times, want 2", k, n)
		}
	}
	_ = tried
}

func TestKeyPoolFailoverSticky(t *testing.T) {
	p := newKeyPool([]string{"k1", "k2"}, StrategyFailover)
	for i := 0; i < 5; i++ {
		k, _, ok := p.tryAcquire(map[int]bool{})
		if !ok || k != "k1" {
			t.Fatalf("failover must stick to first available key, got %q", k)
		}
	}
	// 主 key 冷却后降级到 k2。
	p.cool(0, time.Minute)
	k, _, _ := p.tryAcquire(map[int]bool{})
	if k != "k2" {
		t.Fatalf("after cooling key0, want k2, got %q", k)
	}
	// 冷却到期回池 → 回到 k1（粘主）。
	p.nowFn = func() time.Time { return time.Now().Add(2 * time.Minute) }
	k, _, _ = p.tryAcquire(map[int]bool{})
	if k != "k1" {
		t.Fatalf("after cooldown expiry, want k1, got %q", k)
	}
}

func TestKeyPoolDisableAndStatus(t *testing.T) {
	p := newKeyPool([]string{"k1", "k2", "k3"}, StrategyRoundRobin)
	p.disable(1)
	p.cool(2, time.Hour)
	st := p.status()
	if st.Total != 3 || st.Available != 1 || st.Cooling != 1 || st.Disabled != 1 {
		t.Fatalf("status = %+v", st)
	}
	// 同请求内已试过的 key 不再选。
	if _, idx, ok := p.tryAcquire(map[int]bool{0: true}); ok || idx != -1 {
		t.Fatal("only key left is tried; expect no candidate")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("30"); d != 30*time.Second {
		t.Fatalf("seconds form = %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty = %v", d)
	}
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Fatalf("garbage = %v", d)
	}
}

// multiKeyServer 按 key 返回不同行为的假上游：keysBad 中的 key 恒返回 given 状态。
type multiKeyServer struct {
	usedKeys []string
	failKey  string
	failAll  bool
	failSts  int
	models   int
}

func newMultiKeyUpstream(t *testing.T, mk *multiKeyServer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		mk.usedKeys = append(mk.usedKeys, key)
		if r.URL.Path == "/models" {
			if mk.failAll || (mk.failKey != "" && key == "Bearer "+mk.failKey) {
				if mk.failSts == 429 {
					w.Header().Set("Retry-After", "30")
				}
				w.WriteHeader(mk.failSts)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}}})
			return
		}
		// /chat/completions
		if mk.failAll || (mk.failKey != "" && key == "Bearer "+mk.failKey) {
			if mk.failSts == 429 {
				w.Header().Set("Retry-After", "30")
			}
			w.WriteHeader(mk.failSts)
			_, _ = w.Write([]byte(`{"error":{"message":"limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
}

func chatWithKeys(t *testing.T, v *Vendor) *contract.Reply {
	t.Helper()
	msg := &contract.Message{Model: "src1/m1", Extra: map[string]any{
		keyRawBody: []byte(`{"model":"src1/m1","messages":[{"role":"user","content":"q"}]}`),
	}}
	reply, err := v.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return reply
}

func TestChatMultiKey429FallsToNextKey(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{failKey: "k1", failSts: 429}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	v, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKeys: []string{"k1", "k2"}, Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	reply := chatWithKeys(t, v)
	if reply.Status != http.StatusOK {
		t.Fatalf("status = %d", reply.Status)
	}
	// 请求先试 k1（429）再落到 k2。
	if len(mk.usedKeys) < 2 || mk.usedKeys[len(mk.usedKeys)-1] != "Bearer k2" {
		t.Fatalf("used keys = %v", mk.usedKeys)
	}
	// k1 已冷却：后续请求直接 k2。
	before := len(mk.usedKeys)
	chatWithKeys(t, v)
	after := mk.usedKeys[before:]
	for _, k := range after {
		if k == "Bearer k1" {
			t.Fatalf("cooling key must be skipped: %v", after)
		}
	}
	st := v.PoolStatus()
	if st.Cooling != 1 || st.Available != 1 {
		t.Fatalf("pool status = %+v", st)
	}
}

func TestChatMultiKey401DisablesKey(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{failKey: "k1", failSts: 401}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	v, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKeys: []string{"k1", "k2"}, Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if reply := chatWithKeys(t, v); reply.Status != http.StatusOK {
		t.Fatalf("status = %d", reply.Status)
	}
	st := v.PoolStatus()
	if st.Disabled != 1 || st.Available != 1 {
		t.Fatalf("pool status = %+v", st)
	}
	// 多次请求不再触碰禁用 key。
	before := len(mk.usedKeys)
	for i := 0; i < 3; i++ {
		chatWithKeys(t, v)
	}
	for _, k := range mk.usedKeys[before:] {
		if k == "Bearer k1" {
			t.Fatal("disabled key must never be used again")
		}
	}
}

func TestChatAllKeysExhaustedReturnsLastError(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{failKey: "k1", failSts: 429}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	// 两个 key 都恒 429：无 key 成功 → 返回最后一次（429），供外层厂商级切换。
	v, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKeys: []string{"k1", "k1x"}, Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	// 让两个 key 都 429：改造 fake —— failKey 只能一个，这里直接把第二个 key 也置为 429 行为。
	mk2 := &multiKeyServer{failAll: true, failSts: 429}
	srv2 := newMultiKeyUpstream(t, mk2)
	defer srv2.Close()
	v2, _ := New(Config{ID: "src1", BaseURL: srv2.URL, APIKeys: []string{"k1", "k2"}, Protocol: ProtoOpenAI})
	_ = v
	msg := &contract.Message{Model: "src1/m1", Extra: map[string]any{
		keyRawBody: []byte(`{"model":"src1/m1","messages":[{"role":"user","content":"q"}]}`),
	}}
	reply, err := v2.Chat(context.Background(), msg)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (last)", reply.Status)
	}
	if v2.PoolStatus().Available != 0 {
		t.Fatalf("all keys should be cooling: %+v", v2.PoolStatus())
	}
}

func TestChatFailoverStrategySticksToPrimary(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	v, err := New(Config{
		ID: "src1", BaseURL: srv.URL,
		APIKeys: []string{"k1", "k2"}, KeyStrategy: StrategyFailover,
		Protocol: ProtoOpenAI,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		chatWithKeys(t, v)
	}
	for _, k := range mk.usedKeys {
		if k != "Bearer k1" {
			t.Fatalf("failover must always use primary key, saw %v", mk.usedKeys)
		}
	}
}

func TestChatStreamMultiKeyFailover(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{failKey: "k1", failSts: 429}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	v, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKeys: []string{"k1", "k2"}, Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	msg := &contract.Message{Model: "src1/m1", Stream: true, Extra: map[string]any{
		keyRawBody: []byte(`{"model":"src1/m1","messages":[{"role":"user","content":"q"}],"stream":true}`),
	}}
	st, err := v.ChatStream(context.Background(), msg)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer st.Close()
	if st.Status != http.StatusOK {
		t.Fatalf("stream status = %d", st.Status)
	}
	if mk.usedKeys[len(mk.usedKeys)-1] != "Bearer k2" {
		t.Fatalf("stream must fall to k2, used = %v", mk.usedKeys)
	}
}

func TestSingleKeyCompatViaAPIKeyField(t *testing.T) {
	t.Setenv("OPCODE2API_DATA_DIR", t.TempDir())
	mk := &multiKeyServer{}
	srv := newMultiKeyUpstream(t, mk)
	defer srv.Close()

	// 老配置只有 api_key 单 key：行为不变。
	v, err := New(Config{ID: "src1", BaseURL: srv.URL, APIKey: "legacy", Protocol: ProtoOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	chatWithKeys(t, v)
	if mk.usedKeys[0] != "Bearer legacy" {
		t.Fatalf("legacy single key = %v", mk.usedKeys)
	}
	if st := v.PoolStatus(); st.Total != 1 || st.Available != 1 {
		t.Fatalf("single-key pool = %+v", st)
	}
}
