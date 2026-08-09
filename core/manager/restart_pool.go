// 一键重启实例池（Rust commands.rs restart_pool 移植）。
// 顺序：停网关 → 全停实例（含残留）→ 等 300ms → 强清池端口+网关端口 → 等 200ms →
// 并行启动全部池成员 → 同步网关（自动拉起）。
package manager

import "time"

// RestartPool 一键重启池。gw 为 nil 时跳过网关步骤。
func (m *Manager) RestartPool(runner Runner, gw *Gateway) RestartPoolResult {
	res := RestartPoolResult{Stopped: 0, Started: 0, FreedPorts: []uint16{}}
	if runner == nil {
		runner = &realRunner{}
	}

	// 1) 停统一网关
	if gw != nil {
		gw.stop(runner)
	}

	// 2) 全停实例（含残留 pid 的僵尸进程）；stopped 计数仅统计池成员（对齐 Rust pool_names.len()）
	m.StopAllInstances(runner)
	time.Sleep(300 * time.Millisecond)

	// 3) 收集池成员端口 + 网关端口，强清占用
	poolNames := []string{}
	memberPorts := []uint16{}
	for _, inst := range m.ListInstances() {
		if inst.JoinGateway {
			poolNames = append(poolNames, inst.Name)
			memberPorts = append(memberPorts, inst.SingboxPort)
		}
	}
	res.Stopped = len(poolNames)
	allPorts := append(append([]uint16(nil), memberPorts...), managerGatewayPort())
	for _, p := range allPorts {
		if !isPortFree(p) {
			if freed := m.ForceFreePort(runner, p); len(freed) > 0 {
				res.FreedPorts = append(res.FreedPorts, p)
			}
		}
	}
	time.Sleep(200 * time.Millisecond)

	// 4) 并行启动全部池成员
	if len(poolNames) > 0 {
		results := m.BatchStart(runner, poolNames)
		for _, err := range results {
			if err == nil {
				res.Started++
			}
		}
	}

	// 5) 同步网关
	if gw != nil {
		if err := gw.sync(runner); err == nil {
			res.GatewayRunning = gw.isRunning(runner)
		} else {
			res.Error = err.Error()
		}
	}
	return res
}
