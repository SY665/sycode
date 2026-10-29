// SyCode 的进程入口。
//
// 本文件只做进程级动作：初始化日志、装配依赖、决定退出码。
// 业务逻辑一律不写在这里，顺序见 docs/CODE-DESIGN.md §1。
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/sy665/sycode/internal/logging"
)

// version 由构建注入：go build -ldflags "-X main.version=0.1.0"。
//
// 注意：必须是 var 而不是 const —— 链接器的 -X 只能改写字符串变量，
// 对 const 会静默失效（实测仍输出默认值），构建看起来成功但版本号是错的。
var version = "dev"

func main() {
	os.Exit(run())
}

// run 承担全部组装并返回退出码：0 成功，1 一般错误，2 用法错误。
//
// 启动顺序见 docs/CODE-DESIGN.md §1：日志必须最先就绪，后续步骤才有地方报错。
// P1 余下部分会把这里换成 cobra 命令树（见 docs/ROADMAP.md）。
func run() int {
	cleanup, logPath, err := logging.Init()
	if err != nil {
		// 日志没起来时只能用 stderr；这条错误本身必须可读（CODING-STANDARDS §13）。
		fmt.Fprintf(os.Stderr, "错误：初始化日志失败：%v\n", err)
		return 1
	}
	defer cleanup()

	slog.Debug("日志已初始化", "path", logPath)
	fmt.Fprintf(os.Stderr, "sycode %s：骨架已就绪，命令树见 P1\n", version)
	return 0
}
