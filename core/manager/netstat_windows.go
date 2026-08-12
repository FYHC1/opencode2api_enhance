//go:build windows

package manager

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// netstatRunner Windows 真实现：netstat -ano -tcp。
func netstatRunner() *exec.Cmd {
	return exec.Command("netstat", "-ano", "-tcp")
}

// netstatLineRe 匹配 netstat -ano -tcp 行：
//
//	TCP    127.0.0.1:18100    0.0.0.0:0    LISTENING    1234
//
// 捕获本地端口、状态、PID。
var netstatLineRe = regexp.MustCompile(`^TCP\s+127\.0\.0\.1:(\d{2,5})\s+\S+:\d+\s+(LISTENING|ESTABLISHED|TIME_WAIT)\s+(\d+)$`)

// listPortPids Windows：解析 netstat 输出中占用给定端口的 PID 集合。
func listPortPids(port uint16) []int {
	seen := map[int]bool{}
	var pids []int
	for _, line := range runNetstat() {
		m := netstatLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if uint16(p) != port {
			continue
		}
		pid, _ := strconv.Atoi(m[3])
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}
