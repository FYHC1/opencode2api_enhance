// 数据清理（Rust commands.rs data_clean 移植）。
// 级别：1=清理运行数据（runtime/）；2=+清空实例记录；3=+删除配置（先备份 .bak）。
package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DataClean 执行三级数据清理。
// 先停后清：停网关 → 停全部实例 → 等 300ms → 清理数据目录。
func (m *Manager) DataClean(runner Runner, gw *Gateway, level int) error {
	if level < 1 || level > 3 {
		return fmt.Errorf("无效的清理级别: %d", level)
	}
	if runner == nil {
		runner = &realRunner{}
	}
	if gw != nil {
		gw.stop(runner)
	}
	m.StopAllInstances(runner)
	time.Sleep(300 * time.Millisecond)

	if err := m.cleanDataAt(level); err != nil {
		return err
	}

	// 清空内存态实例（前端刷新即见空）
	if level >= 2 {
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := writeFileMkdir(m.Paths().Instances, []byte("[]")); err != nil {
			return err
		}
	}
	return nil
}

// cleanDataAt 纯文件系统清理（按级别）。
func (m *Manager) cleanDataAt(level int) error {
	p := m.Paths()

	// 1) 删除 runtime 目录（运行数据）
	if err := os.RemoveAll(p.RuntimeDir); err != nil {
		return fmt.Errorf("删除运行数据失败: %w", err)
	}

	// 2) 清空实例记录
	if level >= 2 {
		if err := writeFileMkdir(p.Instances, []byte("[]")); err != nil {
			return fmt.Errorf("清空实例记录失败: %w", err)
		}
	}

	// 3) 删除配置（备份 .bak）
	if level == 3 {
		if _, err := os.Stat(p.Config); err == nil {
			_ = copyFile(p.Config, p.Config+".bak")
			if err := os.Remove(p.Config); err != nil {
				return fmt.Errorf("删除配置失败: %w", err)
			}
		}
	}
	return nil
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(dst); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(dst, data, 0o644)
}
