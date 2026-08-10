package manager

import (
	"net"
	"os"
	"testing"
)

// TestAllocatePortsSkipsInstanceSegments 探针端口分配必须避开实例段与 sing-box 段
// （回归：worker 跳号曾占 44201/46201 导致实例启动失败与测试 401）。
func TestAllocatePortsSkipsInstanceSegments(t *testing.T) {
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "44100")
	defer os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	m := New(t.TempDir())
	c := NewScanController(m, nil)

	opts := ScanOptions{APIPort: 44190, SocksPort: 46190, Concurrency: 8}
	pairs, err := c.allocatePorts(opts, 8)
	if err != nil {
		t.Fatalf("allocatePorts: %v", err)
	}
	// 实例段 [44200, 46199]（api）与 sing-box 段 [46200, 48199]（socks）不可出现
	instLo, instHi := uint16(44200), uint16(46200)
	sbLo, sbHi := uint16(46200), uint16(48200)
	for _, p := range pairs {
		if p.api >= instLo && p.api < instHi {
			t.Fatalf("probe api %d fell into instance segment", p.api)
		}
		if p.socks >= sbLo && p.socks < sbHi {
			t.Fatalf("probe socks %d fell into singbox segment", p.socks)
		}
		if p.api >= p.socks+2000 && p.api < p.socks+2100 {
			t.Fatalf("probe api %d collides singbox range of socks %d", p.api, p.socks)
		}
	}
}

// TestBatchAddSkipsPortWithOccupiedSingbox 批量添加：sing-box 端口被本机监听时，
// 该实例端口必须被跳过（回归：只查 port 不查 port+2000 导致启动 bind 失败）。
func TestBatchAddSkipsPortWithOccupiedSingbox(t *testing.T) {
	os.Setenv("OPCODE2API_INSTANCE_BASE_PORT", "44100")
	defer os.Unsetenv("OPCODE2API_INSTANCE_BASE_PORT")
	m := New(t.TempDir())

	// 占用实例端口 44200 的 sing-box 端口 46200
	ln, err := net.Listen("tcp", "127.0.0.1:46200")
	if err != nil {
		t.Skip("46200 not bindable")
	}
	defer ln.Close()

	m.SetSeams(&SeamFuncs{
		ListNodes: func() []ClashNode {
			return []ClashNode{{Name: "A", NodeType: "vmess", Server: "1.2.3.4", Port: 443, UUID: "u1"}}
		},
	})
	res := m.BatchAdd([]ClashNode{{Name: "A", NodeType: "vmess", Server: "1.2.3.4", Port: 443, UUID: "u1"}}, 0, true, "")
	if len(res.Added) != 1 {
		t.Fatalf("added=%+v skipped=%+v", res.Added, res.Skipped)
	}
	inst, _ := m.FindInstance(res.Added[0])
	if inst.Port == 44200 {
		t.Fatalf("picked 44200 whose singbox 46200 is occupied")
	}
	if inst.SingboxPort == 46200 {
		t.Fatalf("singbox 46200 is occupied, should skip")
	}
	// 且 singbox 端口必须空闲
	if !isPortFree(inst.SingboxPort) {
		t.Fatalf("picked singbox port %d not free", inst.SingboxPort)
	}
}