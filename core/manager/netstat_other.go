//go:build !windows

package manager

import "os/exec"

// netstatRunner 非 Windows 桩：返回 nil（P5 用 lsof / procfs 替换）。
func netstatRunner() *exec.Cmd {
	return nil
}
