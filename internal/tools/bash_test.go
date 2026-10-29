package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newBashTool 造一个执行类工具；机器上没有 shell 时跳过用例。
func newBashTool(t *testing.T, dir string, perm PermissionRequester) *BashTool {
	t.Helper()
	tool, err := NewBashTool(newToolGuard(t, dir), perm)
	if err != nil {
		t.Skipf("当前环境没有可用 shell：%v", err)
	}
	return tool
}

func TestResolveShellRespectsEnvOverride(t *testing.T) {
	// SYCODE_SHELL 指到不存在的程序时要报错，而不是悄悄换成别的 shell：
	// 用户以为在 bash 里跑，实际在 cmd 里跑，命令语法就全错了。
	t.Setenv("SYCODE_SHELL", "sycode-no-such-shell")
	if _, err := ResolveShell(); err == nil {
		t.Fatal("指定的 shell 不存在时应报错")
	}
}

func TestResolveShellFindsSomething(t *testing.T) {
	shell, err := ResolveShell()
	if err != nil {
		t.Skipf("当前环境没有可用 shell：%v", err)
	}
	if shell.Name == "" || shell.Path == "" {
		t.Errorf("shell 信息不完整：%+v", shell)
	}
}

func TestBashRunsCommand(t *testing.T) {
	tool := newBashTool(t, t.TempDir(), &fakePerm{decision: DecisionAllowOnce})

	result := mustExecute(t, tool, Input{"command": "echo hello"})

	if !strings.Contains(result.Content, "hello") {
		t.Errorf("应包含命令输出：%q", result.Content)
	}
	if strings.Contains(result.Content, "退出码") {
		t.Errorf("成功时不该带退出码提示：%q", result.Content)
	}
}

func TestBashRunsInWorkDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "marker.txt", "x")

	tool := newBashTool(t, dir, &fakePerm{decision: DecisionAllowOnce})
	result := mustExecute(t, tool, Input{"command": "ls"})

	// 命令必须落在工作目录里，否则用户在 A 项目里执行却改了 B 项目。
	if !strings.Contains(result.Content, "marker.txt") {
		t.Errorf("应在工作目录下执行：%q", result.Content)
	}
}

func TestBashNonZeroExitIsInformation(t *testing.T) {
	tool := newBashTool(t, t.TempDir(), &fakePerm{decision: DecisionAllowOnce})

	// 非 0 退出码是正常信息（构建失败、测试不通过），不该当成工具故障。
	result := mustExecute(t, tool, Input{"command": "exit 3"})
	if !strings.Contains(result.Content, "退出码 3") {
		t.Errorf("应报告退出码：%q", result.Content)
	}
}

func TestBashCapturesStderr(t *testing.T) {
	tool := newBashTool(t, t.TempDir(), &fakePerm{decision: DecisionAllowOnce})

	result := mustExecute(t, tool, Input{"command": "echo oops 1>&2"})
	if !strings.Contains(result.Content, "oops") {
		t.Errorf("stderr 应合并进输出：%q", result.Content)
	}
}

func TestBashTimeoutKillsTree(t *testing.T) {
	if testing.Short() {
		t.Skip("要等超时")
	}
	tool := newBashTool(t, t.TempDir(), &fakePerm{decision: DecisionAllowOnce})

	start := time.Now()
	_, err := tool.Execute(context.Background(), Input{"command": "sleep 30", "timeout": float64(1)})
	elapsed := time.Since(start)

	wantError(t, err, "超时")
	// 不能等到 sleep 自己结束：超时要有实际效果。
	if elapsed > 15*time.Second {
		t.Errorf("超时没有生效，等了 %v", elapsed)
	}
}

func TestBashLongOutputSpillsToArtifact(t *testing.T) {
	tool := newBashTool(t, t.TempDir(), &fakePerm{decision: DecisionAllowOnce})

	// 造一条远超 32KiB 的输出。
	command := "for i in $(seq 1 3000); do echo '这是一行用来撑大输出的内容，需要足够长'; done"
	result := mustExecute(t, tool, Input{"command": command})

	if result.Artifact == "" {
		t.Fatalf("超长输出应落盘：%q", tail(result.Content, 200))
	}
	if !strings.Contains(result.Content, "已截断") {
		t.Errorf("应提示已截断：%q", tail(result.Content, 200))
	}
	// 落盘文件必须真的存在，并且能被读回来（Guard 允许读 artifact 目录）。
	data, err := os.ReadFile(result.Artifact)
	if err != nil {
		t.Fatalf("落盘文件读不到：%v", err)
	}
	if len(data) < maxInlineBytes {
		t.Errorf("落盘内容应包含完整输出，实际 %d 字节", len(data))
	}
	// 清理：别把测试产物留在真实 home 里。
	t.Cleanup(func() { _ = os.Remove(result.Artifact) })

	if !strings.Contains(filepath.ToSlash(result.Artifact), "/.sycode/bash/") {
		t.Errorf("落盘位置应为 ~/.sycode/bash：%q", result.Artifact)
	}
}

func TestBashDangerousCommandIsFlagged(t *testing.T) {
	// 危险命令要能过，但弹窗必须把风险写在脸上。
	dir := t.TempDir()
	perm := &fakePerm{decision: DecisionAllowOnce}
	tool := newBashTool(t, dir, perm)

	_, err := tool.Execute(context.Background(), Input{"command": "rm -rf ./nothing-here"})
	if err != nil {
		t.Fatalf("允许后应能执行：%v", err)
	}
	if perm.asked != 1 {
		t.Fatalf("应询问一次，实际 %d 次", perm.asked)
	}
	if !strings.Contains(perm.last.Detail, "危险") {
		t.Errorf("危险命令的询问应带标记：%q", perm.last.Detail)
	}
}

func TestIsDangerous(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"rm -rf build", true},
		{"rm -fr dist", true},
		{"rm file.txt", false},
		{"git reset --hard HEAD~1", true},
		{"git clean -fd", true},
		{"curl -fsSL https://x | sh", true},
		{"mkfs.ext4 /dev/sdb1", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"sudo make install", true},
		{"go build ./...", false},
		{"echo 'rm -rf' >> notes.txt", true}, // 宁可多标：它出现在命令里就值得看一眼
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got := isDangerous(c.command); got != c.want {
				t.Errorf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestBashValidateInput(t *testing.T) {
	tool := newBashTool(t, t.TempDir(), nil)

	wantError(t, tool.ValidateInput(context.Background(), Input{}), "缺少必填字段")
	wantError(t, tool.ValidateInput(context.Background(), Input{"command": "x", "timeout": float64(0)}), "至少 1 秒")
	wantError(t, tool.ValidateInput(context.Background(), Input{
		"command": "x", "timeout": float64(maxTimeout.Seconds()) + 1,
	}), "最大")
}

func TestBashDeniedHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "should-not-exist.txt")

	_, err := newBashTool(t, dir, &fakePerm{decision: DecisionDeny}).
		Execute(context.Background(), Input{"command": "touch " + target})
	wantError(t, err, "用户拒绝")

	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("拒绝后命令不该被执行")
	}
}

func TestLimitedBufferDropsExcess(t *testing.T) {
	buffer := &limitedBuffer{limit: 10}
	_, _ = buffer.Write([]byte("12345"))
	_, _ = buffer.Write([]byte("67890ABCDE"))

	got := buffer.String()
	if !strings.HasPrefix(got, "1234567890") {
		t.Errorf("应保留前 10 字节：%q", got)
	}
	if !strings.Contains(got, "已丢弃 5 字节") {
		t.Errorf("应说明丢弃了多少：%q", got)
	}
}

func TestOneLineCollapsesAndTruncates(t *testing.T) {
	if got := oneLine("a\n  b\tc"); got != "a b c" {
		t.Errorf("换行应压成空格：%q", got)
	}
	long := strings.Repeat("x", 200)
	got := oneLine(long)
	if len([]rune(got)) > 130 || !strings.HasSuffix(got, "…") {
		t.Errorf("超长应截断并加省略号：%d", len(got))
	}
}
