// S5 压力系数动态副本测试：raceCopies() 压力分段 + 活跃请求计数成对。
package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

// pressureTransport 实现 contract.RaceTracker：固定健康节点数；其余无操作。
type pressureTransport struct {
	healthy int
}

func (t *pressureTransport) Client(_ contract.Tier, _ bool) (*http.Client, string) {
	return nil, ""
}

func (t *pressureTransport) Mark(string, int, error) {}

func (t *pressureTransport) HealthyNodeCount() int { return t.healthy }

func (t *pressureTransport) RaceStarted([]string) {}

func (t *pressureTransport) RaceFinished([]string) {}

// TestRaceCopiesPressureSegments 压力系数分段（用假 activeRequests 计数）：
//   - pressure < 0.5            → 上限（全速竞速）
//   - 0.5 ≤ pressure < 1.0      → 2（温和竞速）
//   - pressure ≥ 1.0            → 1（退化单发）
//   - 除数 0 / 无 tracker 统计   → 按高压力（=1）
func TestRaceCopiesPressureSegments(t *testing.T) {
	orig := activeRequests.Load()
	defer activeRequests.Store(orig)

	cases := []struct {
		name    string
		active  int64
		healthy int
		upper   int
		want    int
	}{
		{"低压力 0.1 → 上限", 1, 10, 4, 4},
		{"低压力 0.4 → 上限", 4, 10, 3, 3},
		{"中压力 0.7 → 2", 7, 10, 4, 2},
		{"高压力 1.5 → 1", 15, 10, 4, 1},
		{"高压力 3.0 → 1", 30, 10, 4, 1},
		{"除数 0 → 高压力 1", 0, 0, 4, 1},
		{"无 tracker → 高压力 1", 5, -1, 4, 1},
		{"上限 1 = 关闭竞速", 1, 10, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			activeRequests.Store(tc.active)
			v := New(Config{RaceCopies: tc.upper})
			if tc.healthy >= 0 {
				v = New(Config{RaceCopies: tc.upper, Transport: &pressureTransport{healthy: tc.healthy}})
			}
			if got := v.raceCopies(); got != tc.want {
				t.Fatalf("raceCopies()=%d, want %d", got, tc.want)
			}
		})
	}
}

// TestRaceCopiesCustomThresholds 自定义压力阈值生效。
func TestRaceCopiesCustomThresholds(t *testing.T) {
	orig := activeRequests.Load()
	defer activeRequests.Store(orig)
	// p = 2/10 = 0.2；low=0.3 → 仍全速（上限 4）。
	activeRequests.Store(2)
	v := New(Config{
		Transport:        &pressureTransport{healthy: 10},
		RaceCopies:       4,
		RacePressureLow:  0.3,
		RacePressureHigh: 1.0,
	})
	if got := v.raceCopies(); got != 4 {
		t.Fatalf("raceCopies()=%d, want 4 (custom low threshold)", got)
	}
	// p = 0.2；low=0.1 → 已超 low → 温和竞速 2。
	v2 := New(Config{
		Transport:        &pressureTransport{healthy: 10},
		RaceCopies:       4,
		RacePressureLow:  0.1,
		RacePressureHigh: 0.5,
	})
	if got := v2.raceCopies(); got != 2 {
		t.Fatalf("raceCopies()=%d, want 2 (custom thresholds)", got)
	}
}

// TestActiveRequestsCounting Chat/ChatStream 入口计数成对：调用结束后回 0
// （非流式成功 / 流式成功返回流 / 失败路径均不泄漏）。
func TestActiveRequestsCounting(t *testing.T) {
	orig := activeRequests.Load()
	defer activeRequests.Store(orig)
	activeRequests.Store(0)

	ok := &delayRT{delay: 0, body: `{"id":"x","object":"chat.completion","choices":[]}`}
	v := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: ok}},
		addrs:   []string{"a1"},
	}, 2)

	// 非流式成功。
	if _, err := v.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", "")); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := ActiveRequests(); got != 0 {
		t.Fatalf("ActiveRequests=%d after Chat, want 0", got)
	}

	// 流式成功返回流。
	st, err := v.ChatStream(context.Background(), msgWith(`{"model":"m-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	st.Close()
	if got := ActiveRequests(); got != 0 {
		t.Fatalf("ActiveRequests=%d after ChatStream, want 0", got)
	}

	// 失败路径（全部 503，重试耗尽返回）。
	v2 := newRaceVendor(&racerTransport{
		clients: []*http.Client{{Transport: &failRT{status: http.StatusServiceUnavailable}}},
		addrs:   []string{"a1"},
	}, 2)
	reply, _ := v2.Chat(context.Background(), msgWith(`{"model":"m-free","messages":[{"role":"user","content":"hi"}]}`, "m-free", "public", ""))
	if reply == nil {
		t.Fatal("want non-nil reply (503 with body)")
	}
	if got := ActiveRequests(); got != 0 {
		t.Fatalf("ActiveRequests=%d after failed Chat, want 0", got)
	}
}
