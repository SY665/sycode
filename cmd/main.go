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
	"github.com/sy665/sycode/internal/config"
	"github.com/sy665/sycode/internal/logging"
	"github.com/sy665/sycode/internal/providers"
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
		Version:      version,
		Commit:       commit,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		ResolveModel: resolveModel,
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

// resolveModel 按 docs/CODE-DESIGN.md §1 的顺序确定要用的模型：
// 注册表 → 用户配置 → 合并 → 密钥校验。
//
// 为什么是一个"按需调用"的函数，而不是在 run() 里提前执行完再塞进 Options：
// 版本与帮助不需要密钥。提前解析会让新用户敲的第一条 `sycode --version`
// 就撞上"缺少密钥"，恰好卡在他还没机会配置的时候。
//
// 错误原样返回：config/providers 的文案已是中文且带修改指引（CODING-STANDARDS §13），
// 这里再包一层只会得到"错误：无法确定模型：缺少密钥：…"这样的叠词。
func resolveModel() (string, error) {
	registry, err := providers.Load()
	if err != nil {
		// 内嵌注册表损坏属于打包错误：用户改不了，但也不能静默退回默认值。
		return "", fmt.Errorf("内置模型注册表不可用（属打包问题，请反馈）：%w", err)
	}

	path, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}

	// 两个空串表示"命令行没有指定"，此时优先级为 配置文件 active_* > 注册表 defaults。
	resolved, err := cfg.Resolve(registry, "", "")
	if err != nil {
		return "", err
	}

	// 密钥只在这里校验"有没有"，取到即丢弃：P2.4 接入 llm 时才交给客户端，
	// 且任何情况下都不写进日志或终端。
	if _, err := config.ResolveKey(resolved); err != nil {
		return "", err
	}

	// 只记供应商、模型与端点；密钥永不记录。
	slog.Debug("模型已就绪",
		"provider", resolved.ProviderID,
		"model", resolved.ModelID,
		"base_url", resolved.BaseURL,
	)
	return resolved.ModelID, nil
}
