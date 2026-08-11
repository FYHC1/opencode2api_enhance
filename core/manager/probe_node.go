// 单节点探测序列（Rust 语义）+ 探针进程拉起。
package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// probeNode 单节点完整探测。
func (c *ScanController) probeNode(opts ScanOptions, node ClashNode, pair portPair, workerDir string) ProbeResult {
	base := ProbeResult{Node: node.Name, NodeType: node.NodeType, Server: node.Server, Port: node.Port}
	budget := time.Duration(opts.TimeoutSec) * time.Second
	deadline := time.Now().Add(budget)
	password := c.m.effectiveDefaultPassword()

	sbCfg, err := buildSingboxConfig(node, pair.socks)
	if err != nil {
		base.Category = "config"
		base.Message = err.Error()
		return base
	}
	if err := os.MkdirAll(filepath.Join(workerDir, "logs"), 0o755); err != nil {
		base.Category = "config"
		base.Message = err.Error()
		return base
	}
	sbPID, err := c.m.spawnProbeSingbox(c.runner, workerDir, sbCfg, pair.socks)
	if err != nil {
		base.Category = "config"
		base.Message = err.Error()
		return base
	}
	waitTimeout := 8 * time.Second
	if rem := time.Until(deadline); rem < waitTimeout {
		waitTimeout = rem
	}
	if err := waitForPort(pair.socks, waitTimeout); err != nil {
		_ = c.runner.Kill(sbPID)
		base.Category = singboxFailCategory(filepath.Join(workerDir, "logs", "singbox.err.log"))
		base.Message = err.Error()
		return base
	}
	time.Sleep(400 * time.Millisecond)

	ocCfg, err := c.m.buildOpenCodeCfg(pair.socks)
	if err != nil {
		_ = c.runner.Kill(sbPID)
		base.Category = "config"
		base.Message = err.Error()
		return base
	}
	ocPID, err := c.m.spawnProbeOpen(c.runner, workerDir, ocCfg, pair.api, password)
	if err != nil {
		_ = c.runner.Kill(sbPID)
		base.Category = "config"
		base.Message = err.Error()
		return base
	}
	// 探针进程清理（防泄漏）：spawn 成功后，无论后续探测成败、任何返回路径，
	// 退出前必杀 sing-box + opencode2api 探针进程——否则每次扫描每个节点
	// 都会残留一对进程（任务管理器可见数十个 opencode2api/sing-box 堆积）。
	defer func() {
		_ = c.runner.Kill(ocPID)
		_ = c.runner.Kill(sbPID)
	}()
	apiWait := time.Until(deadline)
	if apiWait > 20*time.Second {
		apiWait = 20 * time.Second
	}
	if apiWait < 2*time.Second {
		apiWait = 2 * time.Second
	}
	if err := waitForPort(pair.api, apiWait); err != nil {
		_ = c.runner.Kill(ocPID)
		_ = c.runner.Kill(sbPID)
		base.Category = "upstream"
		base.Message = err.Error()
		return base
	}

	remaining := time.Until(deadline)
	if remaining < 2*time.Second {
		remaining = 2 * time.Second
	}
	if remaining > 12*time.Second {
		remaining = 12 * time.Second
	}
	status, body, modelCount, httpErr := freeCompletion(pair.api, password, remaining)
	base.StatusCode = status
	if modelCount >= 0 {
		base.ModelCount = &modelCount
	}
	if probeCompletionSuccess(status, body) {
		base.OK = true
		base.Category = "ok"
		if modelCount >= 0 {
			base.Message = "可用，models=" + itoa(uint16(modelCount))
		} else {
			base.Message = "可用（免费模型最小请求成功）"
		}
		return base
	}
	if httpErr != nil {
		msg := httpErr.Error()
		if strings.Contains(msg, "timed out") || strings.Contains(msg, "超时") {
			base.Category = "timeout"
		} else {
			base.Category = "other"
		}
		base.Message = "请求失败: " + msg
		return base
	}
	switch {
	case status >= 200 && status < 300:
		base.Category = "invalid_response"
	case status == 502 || status == 503 || status == 504:
		base.Category = "upstream"
	default:
		base.Category = "other"
	}
	base.Message = fmt.Sprintf("HTTP %d，%s", status, truncateProbe(string(body), 160))
	return base
}

// spawnProbeSingbox 起 sing-box 探针进程（写配置 + spawn）。
func (m *Manager) spawnProbeSingbox(runner Runner, dir string, cfg []byte, socks uint16) (int, error) {
	if err := os.WriteFile(filepath.Join(dir, "singbox.json"), cfg, 0o644); err != nil {
		return 0, err
	}
	return runner.Start(ExecSpec{
		Bin:      m.binPath("sing-box"),
		Args:     []string{"run", "-c", filepath.Join(dir, "singbox.json")},
		Dir:      dir,
		LogOut:   filepath.Join(dir, "logs", "singbox.out.log"),
		LogErr:   filepath.Join(dir, "logs", "singbox.err.log"),
		NoWindow: true,
	})
}

// spawnProbeOpen 起 opencode2api 探针进程（cwd=workerDir，stats 落盘于内）。
func (m *Manager) spawnProbeOpen(runner Runner, dir string, ocCfg []byte, apiPort uint16, password string) (int, error) {
	if err := os.WriteFile(filepath.Join(dir, "opencode2api.json"), ocCfg, 0o644); err != nil {
		return 0, err
	}
	return runner.Start(ExecSpec{
		Bin:      m.binPath("opencode2api"),
		Args:     []string{"-port", itoa(apiPort), "-config", filepath.Join(dir, "opencode2api.json"), "-password", password},
		Dir:      dir,
		LogOut:   filepath.Join(dir, "logs", "opencode2api.out.log"),
		LogErr:   filepath.Join(dir, "logs", "opencode2api.err.log"),
		NoWindow: true,
	})
}

// singboxFailCategory 分类 sing-box 启动失败（err.log tail 含 tls/cert/handshake → tls）。
func singboxFailCategory(errLog string) string {
	data, err := readFileTail(errLog, 2000)
	if err != nil {
		return "socks"
	}
	low := strings.ToLower(string(data))
	if strings.Contains(low, "tls") || strings.Contains(low, "certificate") || strings.Contains(low, "handshake") {
		return "tls"
	}
	return "socks"
}

// truncateProbe 截断正文（错误展示）。
func truncateProbe(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
