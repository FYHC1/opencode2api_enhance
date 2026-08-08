// 实例生命周期（Rust instance.rs start_instance_inner 移植）。
// 短锁模式：锁内快照 + 置 Starting；锁外 spawn/等待；锁内写回 Running/Error。
package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// singboxWait sing-box 监听端口就绪等待。
const singboxWait = 10 * time.Second

// openCodeWait opencode2api 监听端口就绪等待。
const openCodeWait = 15 * time.Second

// StartInstance 启动实例（短锁模式）。
func (m *Manager) StartInstance(runner Runner, name string) error {
	if runner == nil {
		runner = &realRunner{}
	}
	// 阶段1：锁内快照 + 置 Starting
	m.mu.Lock()
	inst, err := m.markStartingLocked(name)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	// 阶段2：锁外执行（可长时间阻塞）
	runErr := m.startInstanceLockFree(runner, &inst)
	// 阶段3：锁内写回结果
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for i := range list {
		if list[i].Name != name {
			continue
		}
		list[i].PID, list[i].SingboxPID = inst.PID, inst.SingboxPID
		if runErr != nil {
			list[i].Status = StatusError(runErr.Error())
		} else {
			list[i].Status = StatusRunning()
		}
		_ = m.save(list)
		return runErr
	}
	return runErr
}

// markStartingLocked 标记并校验可启动（锁内）。
func (m *Manager) markStartingLocked(name string) (Instance, error) {
	list := m.load()
	for i := range list {
		if list[i].Name != name {
			continue
		}
		switch list[i].Status.State {
		case "Running", "Starting", "Stopping":
			return Instance{}, fmt.Errorf("实例 %s 状态冲突（%s）", name, list[i].Status.State)
		}
		list[i].Status = StatusStarting()
		_ = m.save(list)
		return list[i], nil
	}
	return Instance{}, errors.New("实例不存在: " + name)
}

// startInstanceLockFree 锁外执行：sing-box → 等口 → opencode2api → 等口。
func (m *Manager) startInstanceLockFree(runner Runner, inst *Instance) error {
	sf := m.currentSeams()
	if sf.ResolveNode == nil || sf.BuildSingbox == nil || sf.BuildOpenCfg == nil {
		return errors.New("未装配实例接缝（clash/singbox/opencode 生成器）")
	}
	node, ok := sf.ResolveNode(inst.Node)
	if !ok {
		return fmt.Errorf("节点未找到: %s", inst.Node)
	}
	dir := m.paths.RuntimeDirOf(inst.Name)
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return err
	}
	sbCfg, err := sf.BuildSingbox(node, inst.SingboxPort)
	if err != nil {
		return fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "singbox.json"), sbCfg, 0o644); err != nil {
		return err
	}
	sbPID, err := runner.Start(ExecSpec{
		Bin:      m.binPath("sing-box"),
		Args:     []string{"run", "-c", filepath.Join(dir, "singbox.json")},
		Dir:      dir,
		LogOut:   filepath.Join(dir, "logs", "singbox.out.log"),
		LogErr:   filepath.Join(dir, "logs", "singbox.err.log"),
		NoWindow: true,
	})
	if err != nil {
		return fmt.Errorf("启动 sing-box 失败: %w", err)
	}
	inst.SingboxPID = &sbPID
	if err := waitForPort(inst.SingboxPort, singboxWait); err != nil {
		_ = runner.Kill(sbPID)
		return errors.New("sing-box 端口未就绪: " + err.Error())
	}
	ocCfg, err := sf.BuildOpenCfg(inst.SingboxPort)
	if err != nil {
		_ = runner.Kill(sbPID)
		return fmt.Errorf("生成 opencode2api 配置失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode2api.json"), ocCfg, 0o644); err != nil {
		_ = runner.Kill(sbPID)
		return err
	}
	ocPID, err := runner.Start(ExecSpec{
		Bin:      m.binPath("opencode2api"),
		Args:     []string{"-port", itoa(inst.Port), "-config", filepath.Join(dir, "opencode2api.json"), "-password", inst.Password},
		Dir:      dir, // Go core 把 stats.json 写在 cwd
		LogOut:   filepath.Join(dir, "logs", "opencode2api.out.log"),
		LogErr:   filepath.Join(dir, "logs", "opencode2api.err.log"),
		NoWindow: true,
	})
	if err != nil {
		_ = runner.Kill(sbPID)
		return fmt.Errorf("启动 opencode2api 失败: %w", err)
	}
	inst.PID = &ocPID
	if err := waitForPort(inst.Port, openCodeWait); err != nil {
		_ = runner.Kill(ocPID)
		_ = runner.Kill(sbPID)
		return errors.New("opencode2api 端口未就绪: " + err.Error())
	}
	return nil
}

// StopInstance 停止实例：先杀 opencode 再杀 sing-box（同步，锁内）。
func (m *Manager) StopInstance(runner Runner, name string) error {
	if runner == nil {
		runner = &realRunner{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for i := range list {
		if list[i].Name != name {
			continue
		}
		switch list[i].Status.State {
		case "Starting", "Stopping":
			return fmt.Errorf("实例 %s 状态冲突（%s）", name, list[i].Status.State)
		}
		ocPID, sbPID := pidVal(list[i].PID), pidVal(list[i].SingboxPID)
		if ocPID > 0 {
			_ = runner.Kill(ocPID)
		}
		if sbPID > 0 {
			_ = runner.Kill(sbPID)
		}
		list[i].PID, list[i].SingboxPID = nil, nil
		list[i].Status = StatusStopped()
		return m.save(list)
	}
	return errors.New("实例不存在: " + name)
}

// RemoveInstanceAlive 删除实例：best-effort 先停止再移除记录。
func (m *Manager) RemoveInstanceAlive(runner Runner, name string) error {
	if runner == nil {
		runner = &realRunner{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	for i := range list {
		if list[i].Name != name {
			continue
		}
		ocPID, sbPID := pidVal(list[i].PID), pidVal(list[i].SingboxPID)
		if ocPID > 0 {
			_ = runner.Kill(ocPID)
		}
		if sbPID > 0 {
			_ = runner.Kill(sbPID)
		}
		list = append(list[:i], list[i+1:]...)
		return m.save(list)
	}
	return nil
}

// ReconcileStates 校正状态：Running/Starting 但 pid 已不存在 → Stopped。
func (m *Manager) ReconcileStates(runner Runner) []Instance {
	_ = runner
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	changed := false
	for i := range list {
		st := list[i].Status.State
		if st != "Running" && st != "Starting" {
			continue
		}
		if pid := pidVal(list[i].PID); pid > 0 && !pidAlive(pid) {
			list[i].Status = StatusStopped()
			list[i].PID, list[i].SingboxPID = nil, nil
			changed = true
		}
	}
	if changed {
		_ = m.save(list)
	}
	return list
}

// RefreshStates 返回指定实例的最新状态（输入顺序；先 reconcile）。
func (m *Manager) RefreshStates(runner Runner, names []string) []Instance {
	_ = m.ReconcileStates(runner)
	byName := map[string]Instance{}
	for _, inst := range m.ListInstances() {
		byName[inst.Name] = inst
	}
	out := make([]Instance, 0, len(names))
	for _, n := range names {
		if inst, ok := byName[n]; ok {
			out = append(out, inst)
		}
	}
	return out
}

// pidVal 解指针 pid（nil → 0）。
func pidVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}