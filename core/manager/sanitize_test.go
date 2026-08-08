package manager

import (
	"strings"
	"testing"
)

func TestSanitizeInstanceName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"美国 G1 | 直连、ChatGPT | 3x", "美国 G1 - 直连、ChatGPT - 3x"},
		{"node/with:bad*chars?", "node-with-bad-chars-"},
		{"  leading and trailing  ", "leading and trailing"},
		{"...dots...", "dots"},
		{"", "node"},
		{"||||", "----"},
		{"\x01\x02control", "--control"},
	}
	for _, c := range cases {
		got := sanitizeInstanceName(c.in)
		if got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
		// 结果绝不能含 Windows 非法字符
		for _, bad := range `/\:*?"<>|` {
			if strings.ContainsRune(got, bad) {
				t.Errorf("sanitize(%q) still contains %q: %q", c.in, bad, got)
			}
		}
	}

	// 长度截断 40
	long := strings.Repeat("a", 50) + "|" + strings.Repeat("b", 50)
	got := sanitizeInstanceName(long)
	if len([]rune(got)) > 40 {
		t.Errorf("sanitize long = %d runes, want <= 40", len([]rune(got)))
	}
}

func TestRuntimeDirOfSanitized(t *testing.T) {
	p := Paths{RuntimeDir: "C:\\rt"}
	got := p.RuntimeDirOf("美国 G1 | 直连")
	want := "C:\\rt\\美国 G1 - 直连"
	if got != want {
		t.Errorf("RuntimeDirOf = %q, want %q", got, want)
	}
}
