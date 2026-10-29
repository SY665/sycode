// Package cmd 装配命令行界面：参数解析、子命令、退出码。
//
// 本包不直接调用 provider SDK，也不持有会话状态——那是 appstate 的事。
// 依赖方向见 docs/CODING-STANDARDS.md §2.2。
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/cli/repl"
)

// Env 是进入界面前必须准备好的运行依赖。
type Env struct {
	// Model 只用于标题展示。
	Model string
	// Core 是界面用的回合引擎；为空时界面只回显（测试可省）。
	Core agentcore.AgentCore
	// Permission 是权限询问通路：工具侧往里放请求，界面侧取出来弹窗。
	// 两侧拿的是同一个对象，因此它既不属于 Core 也不属于界面，由这里一并转交。
	Permission *agentcore.PermissionPrompt
	// History 是恢复出来的历史对话，界面启动时先把它画出来。
	// 上下文由 Core 自己恢复（Core.Restore），这里只管"屏幕上有东西"。
	History []agentcore.Turn
	// RestoredNote 是标题下那行说明，例如「已恢复 20261029-201835-0b30（3 轮）」，空串表示新会话。
	RestoredNote string
}

// Options 注入进程级依赖。输出目标可替换，便于测试断言终端文案。
type Options struct {
	Version string
	Commit  string
	Stdout  io.Writer
	Stderr  io.Writer

	// BuildEnv 在进入界面之前准备好模型与回合引擎，失败则不进入界面。
	//
	// 之所以交给调用方延迟执行、而不是在 run() 里算好传进来：版本、帮助不需要密钥，
	// 提前解析会让 `sycode --version` 在没有 key 的机器上直接失败。
	// 参数是 `--new`：由调用方决定恢复上次会话还是新建。
	BuildEnv func(newSession bool) (Env, error)

	// StartREPL 允许测试替换交互入口，参数是已解析的模型名。为空时走真实界面。
	// 不这样做的话，"无参数启动"的测试会真的把终端交给 Bubble Tea，在交互式终端里跑 go test 时会挂住。
	StartREPL func(model string) error
}

// NewRootCommand 构建 sycode 的根命令。
//
// 汉化策略：cobra 没有 i18n，内置错误（未知参数、缺少取值）在 SetFlagErrorFunc
// 与 translateFlagError 里统一转成中文；映射不到的原样保留，避免把真实错误吞掉。
func NewRootCommand(opts Options) *cobra.Command {
	// --new 只是个开关：值存在这里，进 runREPL 时再传下去，cmd 树里其它方法不必都知道它。
	newSession := false

	root := &cobra.Command{
		Use:   "sycode",
		Short: "终端里的 AI 编程代理",
		Long: "SyCode 在终端里读写代码、执行命令、搜索文件，\n" +
			"把一句需求推进到可运行的改动。",
		// 错误由 run() 统一打印，cobra 不要重复输出，也不要在出错时刷一屏用法。
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			// 根命令不接受位置参数：多出来的只可能是拼错的子命令。
			if len(args) > 0 {
				return usageErrorf("未知命令 %q。可用命令见 sycode --help", args[0])
			}
			return runREPL(opts, newSession)
		},
	}

	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)

	// cobra 默认会挂一个英文 completion 命令；我们用不上，且它污染帮助页。
	root.CompletionOptions.DisableDefaultCmd = true
	root.InitDefaultHelpCmd()
	for _, sub := range root.Commands() {
		if sub.Name() == "help" {
			sub.Short = "查看帮助"
		}
	}
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(usageTemplate)

	root.Version = versionString(opts)
	root.SetVersionTemplate("sycode {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageErrorf("%s", translateFlagError(err))
	})

	root.AddCommand(newVersionCmd(opts), newRunCmd())

	// 默认恢复本工作目录的上一次会话；--new 明确要求从头开始。
	root.Flags().BoolVar(&newSession, "new", false, "不恢复上次会话，新建一个")

	// cobra 自动加的 -h/-v 说明是英文（help for sycode / version for sycode）。
	// 先让 cobra 把默认 flag 建出来，再逐条覆盖；两个 flag set 都查一遍，
	// 因为不同 cobra 版本挂载位置不同。
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	localizeDefaultFlags(root.Flags().Lookup)
	localizeDefaultFlags(root.PersistentFlags().Lookup)

	return root
}

// runREPL 准备好运行环境后进入交互式界面；测试可经 Options.StartREPL 替换界面本身。
//
// 准备失败（缺密钥、配置写错）属于"用户需要去动手"，不是用法错误：
// 原样返回让 main 打一行"错误：…"并退出 1，不进入界面，也不刷用法提示。
func runREPL(opts Options, newSession bool) error {
	env, err := buildEnv(opts, newSession)
	if err != nil {
		return err
	}
	if opts.StartREPL != nil {
		return opts.StartREPL(env.Model)
	}
	return repl.Run(context.Background(), repl.Deps{
		Version:    versionString(opts),
		Model:      env.Model,
		Core:       env.Core,
		Permission: env.Permission,
		History:    env.History,
		Restored:   env.RestoredNote,
	})
}

// buildEnv 调用注入的构造函数；未注入时按"没有模型、没有引擎"处理，界面照常可用。
// 这样只测命令树（帮助、版本、参数错误）时不必构造注册表与配置文件。
func buildEnv(opts Options, newSession bool) (Env, error) {
	if opts.BuildEnv == nil {
		return Env{}, nil
	}
	return opts.BuildEnv(newSession)
}

// newVersionCmd 提供 `sycode version`，与 `--version` 等价。
func newVersionCmd(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "sycode "+versionString(opts))
			return err
		},
	}
}

// newRunCmd 提供 `sycode run "<提示词>"`（headless 单回合）。P4 实装。
func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <提示词>",
		Short: "非交互执行一个回合",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageErrorf("run 需要且只需要一个提示词参数，例如 sycode run \"列出当前目录\"")
			}
			// 参数已就位，缺的是实现——属于"未实现"而非"用法错误"，退出码 1。
			return errNotImplemented("headless 模式", "docs/ROADMAP.md P4")
		},
	}
}

// versionString 拼出版本号；有构建提交号时附带，便于用户反馈问题时定位。
func versionString(opts Options) string {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	if opts.Commit == "" || opts.Commit == "none" {
		return version
	}
	return fmt.Sprintf("%s (build %s)", version, opts.Commit)
}

// exitError 携带退出码，让 main 不必靠字符串猜是用法错误还是一般错误。
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func (e *exitError) ExitCode() int { return e.code }

// usageErrorf 构造退出码 2 的用法错误（参数拼错、缺参、未知子命令）。
func usageErrorf(format string, a ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, a...)}
}

// errNotImplemented 构造退出码 1 的"尚未实现"错误。
//
// 单独成型是为了让"暂未实现"与"用法错误"在退出码上可区分：
// 脚本里能据此判断是用户敲错了，还是这个功能还没做。
// hint 指向实装位置，避免用户以为是自己用错了。
func errNotImplemented(what, hint string) error {
	msg := what + "尚未实现"
	if hint != "" {
		msg += "（见 " + hint + "）"
	}
	return &exitError{code: 1, err: errors.New(msg)}
}

// 帮助与用法模板：cobra 默认是英文，这里整体汉化（CODING-STANDARDS §13.6）。
const helpTemplate = `{{with (or .Long .Short)}}{{.}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

const usageTemplate = `用法：
  {{.UseLine}}{{if .HasAvailableSubCommands}}
  sycode [命令]{{end}}

{{if .HasAvailableSubCommands}}可用命令：{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}

{{end}}{{if .HasAvailableLocalFlags}}参数：
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

{{end}}{{if .HasAvailableSubCommands}}用 "sycode [命令] --help" 查看子命令详情。{{end}}`

// localizeDefaultFlags 覆盖 cobra 默认 flag 的英文说明。
// 传入 Lookup 而非 FlagSet，是为了同时支持 Flags 与 PersistentFlags。
func localizeDefaultFlags(lookup func(string) *pflag.Flag) {
	usages := map[string]string{"help": "显示帮助", "version": "显示版本"}
	for name, usage := range usages {
		if f := lookup(name); f != nil {
			f.Usage = usage
		}
	}
}

// translateFlagError 把 cobra/pflag 的英文内置错误转成中文。
func translateFlagError(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unknown flag:"):
		return "未知参数 " + strings.TrimSpace(strings.TrimPrefix(msg, "unknown flag:"))
	case strings.HasPrefix(msg, "unknown shorthand flag:"):
		return "未知参数 " + strings.TrimSpace(strings.TrimPrefix(msg, "unknown shorthand flag:"))
	case strings.HasPrefix(msg, "flag needs an argument:"):
		return "参数 " + strings.TrimSpace(strings.TrimPrefix(msg, "flag needs an argument:")) + " 缺少取值"
	case strings.HasPrefix(msg, "invalid argument"):
		return "参数取值非法；" + msg
	default:
		return msg
	}
}

// ExitCode 供 main 取退出码；非 exitError 一律按 1 处理。
func ExitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 1
}
