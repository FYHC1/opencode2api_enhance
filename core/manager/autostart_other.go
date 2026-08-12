//go:build !windows

package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// autostartFilePath 非 Windows 自启文件路径：
//   - Linux：~/.config/autostart/opencode2api-<env>.desktop
//   - macOS：~/Library/LaunchAgents/opencode2api-<env>.plist
//
// 环境隔离：文件名跟随数据目录文件夹名。
func autostartFilePath(dataDir string) string {
	name := autostartRunName(dataDir)
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "opencode2api-"+name+".plist")
	}
	return filepath.Join(home, ".config", "autostart", "opencode2api-"+name+".desktop")
}

// platformAutostartStatus 检查自启文件是否存在。
func platformAutostartStatus(dataDir string) (bool, error) {
	_, err := os.Stat(autostartFilePath(dataDir))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// platformSetAutostart 写/删自启文件（幂等）。
func platformSetAutostart(dataDir string, enabled bool) error {
	path := autostartFilePath(dataDir)
	if !enabled {
		_ = os.Remove(path)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}
	var content string
	if runtime.GOOS == "darwin" {
		content = launchAgentContent(exe, autostartRunName(dataDir))
	} else {
		content = desktopAutostartContent(exe, autostartRunName(dataDir))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
