//go:build windows

package manager

import "os/exec"

// netstatRunner Windows 真实现：netstat -ano -tcp。
func netstatRunner() *exec.Cmd {
	return exec.Command("netstat", "-ano", "-tcp")
}
