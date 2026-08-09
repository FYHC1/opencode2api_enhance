// 开机自启（Rust commands.rs autostart 移植）。
// 桌面壳的窗口由 Go core 的 HTTP 服务承载（http://127.0.0.1:<port>/），
// 页面不是 Tauri webview 本地环境，invoke 不可用 —— 因此自启逻辑必须放 core，
// 前端经 /api/admin/autostart 走 HTTP（与其它管理接口一致）。
// Windows 用 reg 命令写 HKCU Run 键；非 Windows 明确报错。
package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	autostartRunKey  = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	autostartRunName = "opencode2api-manager"
)

// autostartStatus 查询自启注册表项是否存在。
func autostartStatus() (bool, error) {
	if runtime.GOOS != "windows" {
		return false, fmt.Errorf("仅 Windows 支持开机自启")
	}
	out, err := exec.Command("reg", "query", autostartRunKey, "/v", autostartRunName).CombinedOutput()
	if err != nil {
		// reg query 找不到键时返回非零；视为未启用（不报错）
		return false, nil
	}
	return strings.Contains(string(out), autostartRunName), nil
}

// setAutostart 写/删自启注册表项。
func setAutostart(enabled bool) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("仅 Windows 支持开机自启")
	}
	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取可执行文件路径失败: %w", err)
		}
		val := fmt.Sprintf(`"%s"`, exe)
		if out, err := exec.Command("reg", "add", autostartRunKey, "/v", autostartRunName, "/t", "REG_SZ", "/d", val, "/f").CombinedOutput(); err != nil {
			return fmt.Errorf("写入注册表失败: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// 幂等：值不存在时删除失败也可接受
	_, _ = exec.Command("reg", "delete", autostartRunKey, "/v", autostartRunName, "/f").CombinedOutput()
	return nil
}

// AutostartGetHandler GET → {enabled}。
func (m *Manager) AutostartGetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		enabled, err := autostartStatus()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"enabled": enabled, "platform": runtime.GOOS})
	}
}

// AutostartSetHandler POST {enabled}。
func (m *Manager) AutostartSetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		if err := setAutostart(req.Enabled); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "enabled": req.Enabled})
	}
}
