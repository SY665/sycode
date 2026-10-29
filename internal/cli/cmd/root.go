// Package cmd 装配命令行界面：参数解析、子命令、退出码。
//
// 本包不直接调用 provider SDK，也不持有会话状态——那是 appstate 的事。
// 依赖方向见 docs/CODING-STANDARDS.md §2.2。
package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Options 注入进程级依赖。输出目标可替换，便于测试断言终端文案。
type Options struct {
	Version string
	Commit  string
	Stdout  io.Writer
	Stderr  io.Writer
}

// NewRootCommand 构建 sycode 的根命令。
//
// 汉化策略：cobra 没有 i18n，内置错误（未知参数、缺少取值）在 SetFlagErrorFunc
// 与 translateFlagError 里统一转成中文；映射不到的原样保留，避免把真实错误吞掉。
func NewRootCommand(opts Options) *cobra.Command {
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
			return runREPL(opts)
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

	// cobra 自动加的 -h/-v 说明是英文（help for sycode / version for sycode）。
	// 先让 cobra 把默认 flag 建出来，再逐条覆盖；两个 flag set 都查一遍，
	// 因为不同 cobra 版本挂载位置不同。
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	localizeDefaultFlags(root.Flags().Lookup)
	localizeDefaultFlags(root.PersistentFlags().Lookup)

	return root
}

// runREPL 进入交互式界面。P1 单元 1.4 才实现，当前明确报"尚未实现"而不是静默退出。
func runREPL(_ Options) error {
	return errNotImplemented("交互式界面", "docs/ROADMAP.md P1.4")
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
