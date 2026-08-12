package manager

import (
	"testing"
)

// TestParseLsofPIDOutput：lsof -t 输出解析（正常/空/含非 PID 行/去重/跳过 0）。
func TestParseLsofPIDOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []int
	}{
		{"empty", "", nil},
		{"whitespace", "\n  \n\t\n", nil},
		{"single", "1234\n", []int{1234}},
		{"multi", "1234\n5678\n", []int{1234, 5678}},
		{"mixed garbage", "COMMAND\n1234\nnotapid\n5678\n", []int{1234, 5678}},
		{"skip zero", "0\n999\n", []int{999}},
		{"dedupe", "1234\n1234\n", []int{1234}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseLsofPIDOutput(c.out)
			if len(got) != len(c.want) {
				t.Fatalf("got=%v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got=%v, want %v", got, c.want)
				}
			}
		})
	}
}
