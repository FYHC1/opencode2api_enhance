//go:build windows

package manager

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// autostartRunKey Windows 注册表 Run 键。
const autostartRunKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// platformAutostartStatus 查询自启注册表项是否存在。
// 幂等：reg query 找不到键时返回非零，视为未启用（不报错）。
func platformAutostartStatus(dataDir string) (bool, error) {
	name := autostartRunName(dataDir)
	out, err := exec.Command("reg", "query", autostartRunKey, "/v", name).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), name), nil
}

// platformSetAutostart 写/删自启注册表项（幂等）。
func platformSetAutostart(dataDir string, enabled bool) error {
	name := autostartRunName(dataDir)
	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取可执行文件路径失败: %w", err)
		}
		val := fmt.Sprintf(`"%s"`, exe)
		if out, err := exec.Command("reg", "add", autostartRunKey, "/v", name, "/t", "REG_SZ", "/d", val, "/f").CombinedOutput(); err != nil {
			return fmt.Errorf("写入注册表失败: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// 值不存在时删除失败也可接受
	_, _ = exec.Command("reg", "delete", autostartRunKey, "/v", name, "/f").CombinedOutput()
	return nil
}
