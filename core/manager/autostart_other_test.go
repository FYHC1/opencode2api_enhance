package manager

import (
	"strings"
	"testing"
)

// TestAutostartRunName：数据目录派生自启名（异常路径回退默认）。
func TestAutostartRunName(t *testing.T) {
	cases := []struct{ dataDir, want string }{
		{`C:\data\opencode2api-manager`, "opencode2api-manager"},
		{`/home/u/.opencode2api-dev`, ".opencode2api-dev"},
		{"", "opencode2api-manager"},
		{"/", "opencode2api-manager"},
		{".", "opencode2api-manager"},
	}
	for _, c := range cases {
		if got := autostartRunName(c.dataDir); got != c.want {
			t.Fatalf("autostartRunName(%q)=%q, want %q", c.dataDir, got, c.want)
		}
	}
}

// TestDesktopAutostartContent：Linux .desktop 内容关键字段。
func TestDesktopAutostartContent(t *testing.T) {
	c := desktopAutostartContent("/opt/opencode2api", "mgr")
	for _, s := range []string{
		"[Desktop Entry]",
		"Type=Application",
		`Exec="/opt/opencode2api"`,
		"X-GNOME-Autostart-enabled=true",
		"Name=opencode2api mgr",
	} {
		if !strings.Contains(c, s) {
			t.Fatalf(".desktop 缺少 %q:\n%s", s, c)
		}
	}
}

// TestLaunchAgentContent：macOS plist 内容关键字段。
func TestLaunchAgentContent(t *testing.T) {
	c := launchAgentContent("/Applications/opencode2api", "mgr")
	for _, s := range []string{
		`<plist version="1.0">`,
		"<key>Label</key>",
		"opencode2api-mgr",
		"<key>RunAtLoad</key>",
		"<string>/Applications/opencode2api</string>",
	} {
		if !strings.Contains(c, s) {
			t.Fatalf("plist 缺少 %q:\n%s", s, c)
		}
	}
}
