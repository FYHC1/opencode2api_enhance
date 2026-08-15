package manager

// H6 订阅导入同批端口记账偏移单测：真实 sing-box 偏移是 singboxPortOffset（2000），
// 记账必须用同一口径。构造「同批导入内后节点的 API 端口 == 先节点 sing-box 端口」
// 场景——旧代码把 sing-box 记账成 +10000 会静默放行（两实例同时启动必有一方 bind
// 失败），修正后该端口被占用自动 +1 跳过。
// 构造方式（最小节点数）：预置 1999 个实例占住 base+1..base+1999（API）与
// base+2001..base+3999（sing-box），同批导入 2 个节点——n1 取 base，其 sing-box 为
// base+2000；n2 选端从 base 起跳过已占端口，若 base+2000 未记账则恰好落在
// n1 的 sing-box 端口上（旧代码必现撞车，修正后跳至 base+4000）。
// 全程 fake + 临时目录 + httptest，不触网、不 spawn 真实进程。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestImportSubscriptionInBatchPortAccounting(t *testing.T) {
	// 找一段全部空闲的 6000 连续端口（实例段 + sing-box 段 + 修正后 n2 的 sing-box），
	// 避开本机真实占用
	base := uint16(40000)
scan:
	for base < 60000 {
		for p := base; p <= base+6000; p++ {
			if !isPortFree(p) {
				base += 4002
				continue scan
			}
		}
		break
	}
	if base >= 60000 {
		t.Skip("无 4000 连续空闲端口段")
	}
	oldBase := os.Getenv("OPCODE2API_INSTANCE_BASE_PORT")
	defer os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", oldBase)
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", strconv.Itoa(int(base)))

	dataDir := t.TempDir()
	m := New(dataDir)
	// 预置实例：占住 base+1..base+1999（API）与 base+2001..base+3999（sing-box），
	// 让同批导入的选端循环必须走到 base+2000 才可能停
	pre := make([]Instance, 0, 1999)
	for i := 1; i <= 1999; i++ {
		pre = append(pre, Instance{
			Name:        fmt.Sprintf("pre%d", i),
			Port:        base + uint16(i),
			Node:        fmt.Sprintf("pre%d", i),
			IP:          "1.2.3.4:443",
			SingboxPort: base + uint16(i) + singboxPortOffset,
			Status:      StatusStopped(),
		})
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(pre, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.paths.Instances, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// 同一批次导入 2 个节点
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("vless://uuid-1@srv1.example.com:443?security=tls#n1\nvless://uuid-2@srv2.example.com:8443?security=tls#n2\n"))
	}))
	defer srv.Close()

	n, err := m.importSubscription(srv.URL, true)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported = %d, want 2", n)
	}

	insts := m.ListInstances()
	if len(insts) != 2001 {
		t.Fatalf("instances = %d, want 2001", len(insts))
	}
	// 全部 API / sing-box 端口两两唯一（含「节点 API 撞先节点 sing-box」场景）
	used := map[uint16]string{}
	for _, inst := range insts {
		if prev, dup := used[inst.Port]; dup {
			t.Fatalf("port %d reused by %s and %s", inst.Port, prev, inst.Name)
		}
		used[inst.Port] = inst.Name
		if prev, dup := used[inst.SingboxPort]; dup {
			t.Fatalf("singbox port %d reused by %s and %s", inst.SingboxPort, prev, inst.Name)
		}
		used[inst.SingboxPort] = inst.Name
	}
	// 明确断言：n1 的 sing-box 端口（base+2000）未被任何同批实例作为 API 端口占用
	n1sb := uint16(0)
	for _, inst := range insts {
		if inst.Name == "n1" {
			n1sb = inst.SingboxPort
			break
		}
	}
	if n1sb != base+singboxPortOffset {
		t.Fatalf("n1 singbox = %d, want %d", n1sb, base+singboxPortOffset)
	}
	for _, inst := range insts {
		if inst.Port == n1sb {
			t.Fatalf("instance %s api port %d collides n1's singbox port %d", inst.Name, inst.Port, n1sb)
		}
	}
}

// 防呆：分组合并路径（saveSubscriptionCacheGrouped 的 group=="" 分支）也走原子落盘，
// 不因新锁引入漏写/错写。
func TestImportSubscriptionGroupedNoLeak(t *testing.T) {
	m := New(t.TempDir())
	body := "trojan://pw@a.example.com:443#GA\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	if _, err := m.importSubscription(srv.URL, false); err != nil {
		t.Fatalf("import: %v", err)
	}
	cache := m.loadSubscriptionCache()
	if len(cache) != 1 || cache[0].Name != "GA" {
		t.Fatalf("cache = %+v", cache)
	}
	if !strings.Contains(cache[0].Group, "订阅") {
		t.Fatalf("group = %q, want 订阅*", cache[0].Group)
	}
	insts := m.ListInstances()
	if len(insts) != 1 || insts[0].Port == uint16(0) {
		t.Fatalf("instances = %+v", insts)
	}
	if insts[0].SingboxPort != insts[0].Port+singboxPortOffset {
		t.Fatalf("singbox = %d, port = %d, want +%d", insts[0].SingboxPort, insts[0].Port, singboxPortOffset)
	}
}