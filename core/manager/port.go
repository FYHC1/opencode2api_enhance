// 端口工具（Rust commands.rs port_suggest / port_check / force_free_port 移植）。
package manager

import (
	"errors"
	"time"
)

// randSeed 与 Rust LCG 一致的种子（时间）。
func randSeed() uint64 {
	return uint64(time.Now().UnixNano() & 0x7fffffff)
}

// lcgNext 线性同余迭代（Rust 同参）。
func lcgNext(seed *uint64) uint64 {
	*seed = *seed*6364136223846793005 + 1442695040888963407
	return *seed
}

// isPortUsedByInstance 端口是否已被实例表占用。
func (m *Manager) isPortUsedByInstance(port uint16) bool {
	for _, inst := range m.ListInstances() {
		if inst.Port == port || inst.SingboxPort == port {
			return true
		}
	}
	return false
}

// PortSuggest 建议一个可用端口（在环境实例段 [base+100, base+2100) 内 LCG 取 2000 次尝试）。
// 与批量添加/订阅导入同段，端口和建议永远落在当前环境槽内（跨环境零重叠）。
func (m *Manager) PortSuggest() (uint16, error) {
	seed := randSeed()
	base := m.instanceBasePort()
	for i := 0; i < 2000; i++ {
		candidate := base + 100 + uint16(lcgNext(&seed)%2000)
		if !m.isPortUsedByInstance(candidate) && isPortFree(candidate) {
			return candidate, nil
		}
	}
	return 0, errors.New("未找到可用端口")
}

// PortCheckResult 端口检查结果（前端契约）。
type PortCheckResult struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// PortCheck 检查端口是否可被新实例使用。
func (m *Manager) PortCheck(port uint16) PortCheckResult {
	if port < 1024 {
		return PortCheckResult{Available: false, Reason: "端口需 >= 1024"}
	}
	if m.isPortUsedByInstance(port) {
		return PortCheckResult{Available: false, Reason: "已被实例占用"}
	}
	if isPortFree(port) {
		return PortCheckResult{Available: true, Reason: "端口可用"}
	}
	return PortCheckResult{Available: false, Reason: "端口已被本机程序监听"}
}

// ForceFreePort 强制释放端口：netstat 找到 PID → taskkill。
func (m *Manager) ForceFreePort(runner Runner, port uint16) []int {
	if runner == nil {
		runner = &realRunner{}
	}
	var freed []int
	for _, pid := range pidsOnPort(port) {
		if runner.Kill(pid) == nil {
			freed = append(freed, pid)
		}
	}
	return freed
}

// RestartPoolResult 一键重启池结果（前端契约）。
type RestartPoolResult struct {
	Stopped        int      `json:"stopped"`
	Started        int      `json:"started"`
	FreedPorts     []uint16 `json:"freed_ports"`
	GatewayRunning bool     `json:"gateway_running"`
	Error          string   `json:"error,omitempty"`
}
