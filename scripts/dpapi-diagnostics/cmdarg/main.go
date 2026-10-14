// 复现 core/manager/open_url.go 在 Windows 上的命令行构造：
// exec.Command("cmd", "/c", "start", "", target)
// 这里把 start 换成 echo（同样的 cmd 解析层），观察含 & 的 URL 是否被 cmd 拆成多条命令。
package main

import (
	"fmt"
	"os/exec"
)

const cmdExe = `C:\Windows\System32\cmd.exe`

func main() {
	target := "http://127.0.0.1:58054/?gateway=http%3A%2F%2F127.0.0.1%3A40000%2Fv1&plugin=qw2api&models=qw2api%2Fqwork-auto%2Cqw2api%2Fqwork-advanced"

	fmt.Println("--- 用法1：core 现在的写法（arg 列表，Go 自行转义） ---")
	c1 := exec.Command(cmdExe, "/c", "echo", "start", "", target)
	out, err := c1.CombinedOutput()
	fmt.Printf("cmd 实际收到并解析后输出：\n%s\nerr=%v\n", out, err)

	fmt.Println("--- 用法2：显式给整个 URL 加引号 ---")
	c2 := exec.Command(cmdExe, "/c", "echo", "start", "", `"`+target+`"`)
	out2, err2 := c2.CombinedOutput()
	fmt.Printf("cmd 实际收到并解析后输出：\n%s\nerr=%v\n", out2, err2)
}
