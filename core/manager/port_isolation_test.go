package manager

import (
	"fmt"
	"os"
	"testing"
)

// TestPortIsolationE2E 验证便携端口段隔离：实例 51000+ / sing-box +2000（既定规则）。
func TestPortIsolationE2E(t *testing.T) {
	// 模拟便携版环境变量（壳层 run() 注入）
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "51000")
	os.Setenv("OPCODE2API_PROBE_API_PORT", "52000")
	os.Setenv("OPCODE2API_PROBE_SOCKS_PORT", "52100")
	defer os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	defer os.Unsetenv("OPCODE2API_PROBE_API_PORT")
	defer os.Unsetenv("OPCODE2API_PROBE_SOCKS_PORT")

	dir := os.Getenv("OPCODE2API_DATA_DIR")
	if dir == "" {
		dir = "C:\\Users\\ASUS\\AppData\\Roaming\\oc2api-clean-test"
	}
	m := New(dir)
	m.SetSeams(&SeamFuncs{
		ListNodes: func() []ClashNode {
			return []ClashNode{{Name: "A", NodeType: "vmess", Server: "1.2.3.4", Port: 443, UUID: "u1"}}
		},
		BuildSingbox: BuildSingboxConfigFor,
		BuildOpenCfg: m.BuildOpenCodeCfgFor,
	})

	// 批量添加（走环境变量 base port）
	res := m.httpBatchAdd([]BatchAddHTTPItem{{Node: "A"}}, 0, true, "n")
	if res.AddedCount != 1 {
		for _, e := range res.Errors {
			t.Logf("add error: node=%q err=%q", e.Node, e.Error)
		}
		t.Fatalf("added=%d want 1", res.AddedCount)
	}
	inst, _ := m.FindInstance(res.Added[0].Name)
	fmt.Printf("instance port=%d singbox=%d\n", inst.Port, inst.SingboxPort)
	if inst.Port < 51000 || inst.Port >= 52000 {
		t.Errorf("instance port %d not in 51000 segment", inst.Port)
	}
	if inst.SingboxPort != inst.Port+singboxPortOffset {
		t.Errorf("singbox %d != port+%d (%d)", inst.SingboxPort, singboxPortOffset, inst.Port+singboxPortOffset)
	}
	if inst.SingboxPort < 51000+singboxPortOffset || inst.SingboxPort >= 52000+singboxPortOffset {
		t.Errorf("singbox %d not in +%d segment (would collide with other env)", inst.SingboxPort, singboxPortOffset)
	}

	// 端口建议应落在实例段附近（非正式版 18100 段）
	suggest, err := m.PortSuggest()
	if err != nil {
		t.Fatalf("PortSuggest: %v", err)
	}
	fmt.Printf("port suggest=%d\n", suggest)
	if suggest < 51000+100 || suggest >= 51000+100+2000 {
		t.Errorf("suggest %d not in env instance segment", suggest)
	}

	// 探针端口
	if got := m.probeAPIPort(); got != 52000 {
		t.Errorf("probe api=%d want 52000", got)
	}
	if got := m.probeSocksPort(); got != 52100 {
		t.Errorf("probe socks=%d want 52100", got)
	}

	_ = m.RemoveInstanceAlive(m.Run(), inst.Name)
}
