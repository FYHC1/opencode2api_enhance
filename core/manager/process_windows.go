//go:build windows

package manager

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// applyNoWindow Windows：CREATE_NO_WINDOW（0x08000000）。
func applyNoWindow(cmd *exec.Cmd, enabled bool) {
	if enabled {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	}
}

// killProcess Windows：taskkill /PID <pid> /F（与 Rust 一致）。
func killProcess(pid int) error {
	taskkill := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F")
	applyNoWindow(taskkill, true)
	return taskkill.Run()
}

// pidAliveWindows Windows：tasklist 过滤该 PID；无匹配输出 "No tasks"。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), "No tasks")
}

func intToToken(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
