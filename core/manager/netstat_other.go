//go:build !windows

package manager

import (
	"os/exec"
	"strconv"
)

// netstatRunner 非 Windows：保留签名桩（端口查询走 lsof，见 listPortPids）。
func netstatRunner() *exec.Cmd {
	return nil
}

// listPortPids 非 Windows：lsof -t -iTCP:<port> -sTCP:LISTEN 输出占用端口的 PID（每行一个）。
// 无监听或 lsof 不可用时返回 nil。
func listPortPids(port uint16) []int {
	out, err := exec.Command("lsof", "-t", "-iTCP:"+strconv.Itoa(int(port)), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	return parseLsofPIDOutput(string(out))
}
