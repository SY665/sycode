// SyCode 的进程入口。
//
// 本文件只做进程级动作：初始化日志、装配命令树、决定退出码。
// 业务逻辑一律不写在这里，顺序见 docs/CODE-DESIGN.md §1。
package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/cli/cmd"
	"github.com/sy665/sycode/internal/config"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/logging"
	"github.com/sy665/sycode/internal/providers"
	"github.com/sy665/sycode/internal/tools"
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
		Version:  version,
		Commit:   commit,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		BuildEnv: buildEnv,
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

// buildEnv 按 docs/CODE-DESIGN.md §1 的顺序准备运行环境：
// 注册表 → 用户配置 → 合并 → 密钥校验 → 客户端 → 系统提示 → 回合引擎。
//
// 为什么是一个"按需调用"的函数，而不是在 run() 里提前执行完再塞进 Options：
// 版本与帮助不需要密钥。提前解析会让新用户敲的第一条 `sycode --version`
// 就撞上"缺少密钥"，恰好卡在他还没机会配置的时候。
//
// 错误原样返回：config/providers 的文案已是中文且带修改指引（CODING-STANDARDS §13），
// 这里再包一层只会得到"错误：无法准备运行环境：缺少密钥：…"这样的叠词。
func buildEnv() (cmd.Env, error) {
	registry, err := providers.Load()
	if err != nil {
		// 内嵌注册表损坏属于打包错误：用户改不了，但也不能静默退回默认值。
		return cmd.Env{}, fmt.Errorf("内置模型注册表不可用（属打包问题，请反馈）：%w", err)
	}

	path, err := config.DefaultPath()
	if err != nil {
		return cmd.Env{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return cmd.Env{}, err
	}

	// 两个空串表示"命令行没有指定"，此时优先级为 配置文件 active_* > 注册表 defaults。
	resolved, err := cfg.Resolve(registry, "", "")
	if err != nil {
		return cmd.Env{}, err
	}

	// 密钥在这里从"校验有没有"变成"真的用上"：交给客户端，且任何情况下都不写日志。
	apiKey, err := config.ResolveKey(resolved)
	if err != nil {
		return cmd.Env{}, err
	}

	client, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: resolved.BaseURL,
		APIKey:  apiKey,
		Model:   resolved.ModelID,
	})
	if err != nil {
		return cmd.Env{}, err
	}

	// 只记供应商、模型与端点；密钥永不记录。
	slog.Debug("模型已就绪",
		"provider", resolved.ProviderID,
		"model", resolved.ModelID,
		"base_url", resolved.BaseURL,
	)

	return cmd.Env{
		Model: resolved.ModelID,
		Core:  agentcore.NewDirect(client, llm.Build(promptEnv(resolved.ModelID))),
	}, nil
}

// promptEnv 收集系统提示要用的环境事实。
//
// 工作目录取不到时留空：提示里少一行环境，比整个程序起不来强。
func promptEnv(model string) llm.Env {
	workDir, err := os.Getwd()
	if err != nil {
		slog.Debug("取工作目录失败，系统提示将不含工作目录", "err", err)
		workDir = ""
	}
	return llm.Env{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		WorkDir: workDir,
		Shell:   detectShell(),
		Model:   model,
		Today:   time.Now(),
	}
}

// detectShell 取命令执行工具会用的解释器名，写进系统提示（ADR-006 要求）。
//
// 与真正执行命令用的是同一个来源（tools.ResolveShell）：两处结论一旦分叉，
// 模型就会按一种语法写命令、而实际在另一种 shell 里跑。
func detectShell() string {
	shell, err := tools.ResolveShell()
	if err != nil {
		// 拿不到就省略这一行：提示里少一行，比写一个假名字强。
		slog.Debug("未能确定 shell，系统提示将省略该行", "err", err)
		return ""
	}
	return shell.Name
}
