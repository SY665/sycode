package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// newTestRoot 构造一个把输出收集到缓冲区、且不依赖真实终端的根命令。
func newTestRoot(t *testing.T, args ...string) (*bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	root := NewRootCommand(Options{
		Version: "0.1.0",
		Commit:  "abc1234",
		Stdout:  stdout,
		Stderr:  stderr,
	})
	root.SetArgs(args)
	return stdout, stderr, root.Execute()
}

func TestRootCommand_VersionFlag(t *testing.T) {
	stdout, _, err := newTestRoot(t, "--version")
	if err != nil {
		t.Fatalf("--version 不应报错：%v", err)
	}
	want := "sycode 0.1.0 (build abc1234)\n"
	if got := stdout.String(); got != want {
		t.Fatalf("版本输出不符：期望 %q，实际 %q", want, got)
	}
}

func TestRootCommand_VersionSubcommandWithoutCommit(t *testing.T) {
	stdout := &bytes.Buffer{}
	root := NewRootCommand(Options{Version: "0.1.0", Stdout: stdout, Stderr: &bytes.Buffer{}})
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version 子命令不应报错：%v", err)
	}
	// 没有提交号时不应出现空括号。
	if got, want := stdout.String(), "sycode 0.1.0\n"; got != want {
		t.Fatalf("版本输出不符：期望 %q，实际 %q", want, got)
	}
}

func TestRootCommand_UnknownCommandIsUsageError(t *testing.T) {
	_, _, err := newTestRoot(t, "bogu")
	if err == nil {
		t.Fatal("未知命令应返回错误")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("退出码不符：期望 2，实际 %d", code)
	}
	if !strings.Contains(err.Error(), "未知命令") {
		t.Errorf("错误信息应为中文并指明未知命令，实际：%v", err)
	}
}

func TestRootCommand_UnknownFlagIsChinese(t *testing.T) {
	_, _, err := newTestRoot(t, "--cwd2", "/tmp")
	if err == nil {
		t.Fatal("未知参数应返回错误")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("退出码不符：期望 2，实际 %d", code)
	}
	if !strings.Contains(err.Error(), "未知参数") {
		t.Errorf("错误信息应汉化为“未知参数”，实际：%v", err)
	}
}

func TestRunCmd_RequiresExactlyOneArg(t *testing.T) {
	_, _, err := newTestRoot(t, "run")
	if err == nil {
		t.Fatal("缺少提示词时应返回错误")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("缺参属于用法错误，期望退出码 2，实际 %d", code)
	}
}

func TestRunCmd_NotImplementedHasExitCodeOne(t *testing.T) {
	_, _, err := newTestRoot(t, "run", "列出当前目录")
	if err == nil {
		t.Fatal("未实现的命令应返回错误")
	}
	// 参数正确但功能未做：必须是 1 而不是 2，脚本才能区分"敲错"与"没做"。
	if code := ExitCode(err); code != 1 {
		t.Errorf("未实现属于一般错误，期望退出码 1，实际 %d", code)
	}
	if !strings.Contains(err.Error(), "尚未实现") {
		t.Errorf("错误信息应说明尚未实现，实际：%v", err)
	}
}

func TestRootCommand_NoArgsStartsREPL(t *testing.T) {
	// 用 stub 替换真实界面：否则测试会把终端交给 Bubble Tea，在交互终端里挂住。
	sentinel := errors.New("已进入界面")
	called := false
	root := NewRootCommand(Options{
		Version: "0.1.0",
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		StartREPL: func() error {
			called = true
			return sentinel
		},
	})
	root.SetArgs(nil)

	err := root.Execute()
	if !called {
		t.Fatal("无参数执行应进入交互界面")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("应原样返回界面错误，实际 %v", err)
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("界面错误按一般错误处理，期望退出码 1，实际 %d", code)
	}
}

func TestHelp_IsFullyLocalized(t *testing.T) {
	stdout := &bytes.Buffer{}
	root := NewRootCommand(Options{Version: "0.1.0", Stdout: stdout, Stderr: &bytes.Buffer{}})
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help 不应报错：%v", err)
	}

	got := stdout.String()
	// 帮助页出现英文标签或 cobra 自带 completion 命令，说明汉化被改坏了。
	for _, banned := range []string{"Available Commands", "Flags:", "help for sycode", "version for", "completion", "Usage:"} {
		if strings.Contains(got, banned) {
			t.Errorf("帮助页残留英文内容 %q：\n%s", banned, got)
		}
	}
	for _, want := range []string{"用法：", "可用命令：", "显示帮助", "显示版本"} {
		if !strings.Contains(got, want) {
			t.Errorf("帮助页缺少中文内容 %q：\n%s", want, got)
		}
	}
}

func TestDefaultFlagsAreLocalized(t *testing.T) {
	root := NewRootCommand(Options{Version: "0.1.0", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})

	for name, want := range map[string]string{"help": "显示帮助", "version": "显示版本"} {
		f := root.Flags().Lookup(name)
		if f == nil {
			f = root.PersistentFlags().Lookup(name)
		}
		if f == nil {
			t.Fatalf("默认参数 %q 不存在", name)
		}
		if f.Usage != want {
			t.Errorf("参数 %q 的说明不符：期望 %q，实际 %q", name, want, f.Usage)
		}
	}
}
