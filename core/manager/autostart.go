// 开机自启（Rust commands.rs autostart 移植）。
// 桌面壳的窗口由 Go core 的 HTTP 服务承载（http://127.0.0.1:<port>/），
// 页面不是 Tauri webview 本地环境，invoke 不可用 —— 因此自启逻辑必须放 core，
// 前端经 /api/admin/autostart 走 HTTP（与其它管理接口一致）。
//
// 平台实现：
//   - Windows：reg 命令写 HKCU Run 键（autostart_windows.go）
//   - Linux：~/.config/autostart/*.desktop（autostart_other.go）
//   - macOS：~/Library/LaunchAgents/*.plist（autostart_other.go）
//
// 环境隔离：自启键名/文件名跟随数据目录文件夹名（正式版 opencode2api-manager、
// dev -dev、便携 -test），与数据目录/端口段隔离一致 —— 避免不同环境互相污染。
package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
)

const autostartRunNameDefault = "opencode2api-manager"

// autostartRunName 按数据目录派生自启键名：取目录文件夹名；
// 异常（空/根目录）回退默认键名，保证正式版行为不变。
func autostartRunName(dataDir string) string {
	base := filepath.Base(dataDir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return autostartRunNameDefault
	}
	return base
}

// autostartStatus 查询自启是否启用（平台实现：Windows 注册表 / 其他 自启文件）。
func autostartStatus(dataDir string) (bool, error) {
	return platformAutostartStatus(dataDir)
}

// setAutostart 写/删自启（平台实现：Windows 注册表 / 其他 自启文件）。
func setAutostart(dataDir string, enabled bool) error {
	return platformSetAutostart(dataDir, enabled)
}

// desktopAutostartContent 生成 Linux XDG autostart .desktop 内容（可单测）。
func desktopAutostartContent(exe, name string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=opencode2api %s
Exec="%s"
X-GNOME-Autostart-enabled=true
`, name, exe)
}

// launchAgentContent 生成 macOS LaunchAgent plist 内容（可单测）。
func launchAgentContent(exe, name string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>opencode2api-%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`, name, exe)
}

// AutostartGetHandler GET → {enabled}。
func (m *Manager) AutostartGetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		enabled, err := autostartStatus(m.paths.DataDir)
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
		if err := setAutostart(m.paths.DataDir, req.Enabled); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "enabled": req.Enabled})
	}
}
