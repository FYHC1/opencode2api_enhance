//go:build !windows

package manager

import (
	"os/exec"
	"strconv"
	"strings"
)

// listAppProcesses 非 Windows：经 ps 枚举本应用进程（opencode2api / sing-box）。
func listAppProcesses() ([]procLine, error) {
	out, err := exec.Command("ps", "-eo", "pid=,comm=,args=").Output()
	if err != nil {
		return nil, err
	}
	var procs []procLine
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		comm := fields[1]
		if comm != "opencode2api" && comm != "sing-box" && !strings.HasSuffix(comm, "opencode2api") && !strings.HasSuffix(comm, "sing-box") {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		procs = append(procs, procLine{PID: pid, Name: comm, Cmd: strings.Join(fields[2:], " ")})
	}
	return procs, nil
}
