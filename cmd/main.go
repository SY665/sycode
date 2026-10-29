// SyCode 的进程入口。
//
// 本文件只做进程级动作：初始化日志、装配命令树、决定退出码。
// 业务逻辑一律不写在这里，顺序见 docs/CODE-DESIGN.md §1。
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/sy665/sycode/internal/cli/cmd"
	"github.com/sy665/sycode/internal/logging"
)

// version 与 commit 由构建注入：
// go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD)"
//
// 注意：必须是 var 而不是 const —— 链接器的 -X 只能改写字符串变量，
// 对 const 会静默失效（实测仍输出默认值），构建看起来成功但版本号是错的。
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run())
}

// run 承担全部组装并返回退出码：0 成功，1 一般错误，2 用法错误。
//
// 启动顺序见 docs/CODE-DESIGN.md §1：日志必须最先就绪，后续步骤才有地方报错。
func run() int {
	cleanup, logPath, err := logging.Init()
	if err != nil {
		// 日志没起来时只能用 stderr；这条错误本身必须可读（CODING-STANDARDS §13）。
		fmt.Fprintf(os.Stderr, "错误：初始化日志失败：%v\n", err)
		return 1
	}
	defer cleanup()

	slog.Debug("日志已初始化", "path", logPath)

	root := cmd.NewRootCommand(cmd.Options{
		Version: version,
		Commit:  commit,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})

	err = root.Execute()
	if err == nil {
		return 0
	}

	code := cmd.ExitCode(err)
	// 用法错误已经带"参数错误"语义，再套一层"错误："会显得重复。
	if code == 2 {
		fmt.Fprintf(os.Stderr, "参数错误：%v\n用法见 sycode --help\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
	}
	slog.Debug("命令执行失败", "err", err, "exit_code", code)
	return code
}
