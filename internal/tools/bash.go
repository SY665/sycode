package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sy665/sycode/internal/filesystem"
)

// BashToolName 是工具名。
const BashToolName = "bash"

const (
	// defaultTimeout 是命令的默认超时。构建、测试通常在这个量级内结束。
	defaultTimeout = 120 * time.Second
	// maxTimeout 是允许用户指定的上限：再长就该改成后台任务，而不是阻塞对话。
	maxTimeout = 600 * time.Second
	// maxCapturedBytes 是内存里最多留多少输出。超出的部分直接丢弃：
	// 进程可能吐出几个 GB，而其中能用的信息只在尾部。
	maxCapturedBytes = 4 << 20
	// maxErrorTailBytes 是超时/失败时附带多少输出尾部。
	maxErrorTailBytes = 2 << 10
)

// Shell 是选定的命令解释器。
type Shell struct {
	// Name 是给人看与给模型看短名：bash / powershell / cmd。
	Name string
	// Path 是可执行文件绝对路径。
	Path string
}

// ResolveShell 按 ADR-006 的顺序选 shell：SYCODE_SHELL → bash → PowerShell → cmd。
//
// 系统提示里那行 "Current shell:" 与真正执行用的是同一个来源：两处结论一旦分叉，
// 模型就会按 bash 语法写命令、而实际在 cmd 里跑。
func ResolveShell() (Shell, error) {
	if custom := strings.TrimSpace(os.Getenv("SYCODE_SHELL")); custom != "" {
		path, err := exec.LookPath(custom)
		if err != nil {
			return Shell{}, fmt.Errorf("执行失败：SYCODE_SHELL=%q 找不到可执行文件：%w", custom, err)
		}
		return Shell{Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path}, nil
	}

	candidates := []struct{ name, exe string }{
		{"bash", "bash"},
		{"bash", "bash.exe"},
		{"powershell", "powershell.exe"},
		{"pwsh", "pwsh"},
		{"cmd", "cmd.exe"},
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate.exe); err == nil {
			return Shell{Name: candidate.name, Path: path}, nil
		}
	}
	return Shell{}, errors.New("执行失败：找不到可用的 shell（试过 bash / PowerShell / cmd）")
}

// command 按 shell 种类拼出执行一条命令的 argv。
func (s Shell) command(ctx context.Context, command string) *exec.Cmd {
	switch s.Name {
	case "bash":
		return exec.CommandContext(ctx, s.Path, "-c", command)
	case "powershell", "pwsh":
		// -NoProfile：别让用户的 profile 改掉输出编码或塞一堆欢迎语。
		return exec.CommandContext(ctx, s.Path, "-NoProfile", "-NonInteractive", "-Command", command)
	default:
		return exec.CommandContext(ctx, s.Path, "/c", command)
	}
}

// BashTool 在工作目录里执行一条 shell 命令。
type BashTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
	shell Shell
}

// NewBashTool 注入依赖。失败条件是"这台机器上找不到任何 shell"。
func NewBashTool(guard *filesystem.Guard, perm PermissionRequester) (*BashTool, error) {
	shell, err := ResolveShell()
	if err != nil {
		return nil, err
	}
	return &BashTool{guard: guard, perm: perm, shell: shell}, nil
}

func (t *BashTool) Name() string { return BashToolName }

// ShellName 暴露选定的 shell 名，供系统提示显示（ADR-006 要求）。
func (t *BashTool) ShellName() string { return t.shell.Name }

func (t *BashTool) Description() string {
	return "在当前 shell 里执行一条命令，返回合并后的 stdout+stderr 与退出码。" +
		"命令在工作目录下执行；退出码非 0 不是错误，是你需要读的信息。" +
		"默认超时 " + strconv.Itoa(int(defaultTimeout.Seconds())) + " 秒（可用 timeout 指定，上限 " +
		strconv.Itoa(int(maxTimeout.Seconds())) + " 秒），超时会终止整棵进程树。" +
		"输出超过 32KiB 会落盘并给出路径。危险命令（rm -rf、git reset --hard 等）会先征求用户同意。"
}

func (t *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"command": map[string]any{
			"type":        "string",
			"description": "要执行的命令，按当前 shell 的语法书写",
		},
		"timeout": map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("超时秒数，默认 %d、上限 %d", int(defaultTimeout.Seconds()), int(maxTimeout.Seconds())),
		},
	}
}

func (t *BashTool) ValidateInput(_ context.Context, in Input) error {
	if _, err := requireString(in, "command"); err != nil {
		return err
	}
	timeout, err := optionalInt(in, "timeout", int(defaultTimeout.Seconds()))
	if err != nil {
		return err
	}
	if timeout < 1 {
		return fmt.Errorf("参数错误：字段 \"timeout\" 至少 1 秒，实际是 %d", timeout)
	}
	if timeout > int(maxTimeout.Seconds()) {
		return fmt.Errorf("参数错误：字段 \"timeout\" 最大 %d 秒，实际是 %d。更长的任务请拆开或后台执行",
			int(maxTimeout.Seconds()), timeout)
	}
	return nil
}

// Execute 问权限 → 执行 → 截断/落盘输出。
func (t *BashTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	command, _ := requireString(in, "command")
	timeoutSeconds, _ := optionalInt(in, "timeout", int(defaultTimeout.Seconds()))

	detail := "运行命令：" + oneLine(command)
	if isDangerous(command) {
		// 危险命令也要能过，但必须在弹窗里被明确标出来，否则用户只会看到一行命令就点了允许。
		detail = "⚠ 危险命令：" + oneLine(command)
	}
	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   BashToolName,
		path:   ".",
		op:     filesystem.OpExec,
		detail: detail,
	}); err != nil {
		return Result{}, err
	}

	output, exitCode, timedOut := t.run(ctx, command, time.Duration(timeoutSeconds)*time.Second)
	content, artifact := truncateOutput(output)

	switch {
	case timedOut:
		return Result{Content: content, Artifact: artifact}, fmt.Errorf(
			"超时：命令在 %d 秒内没有结束，已终止整棵进程树。以下是已捕获的输出（可能不完整）：\n%s",
			timeoutSeconds, tailString(content, maxErrorTailBytes))
	case exitCode != 0:
		// 非 0 退出码是正常信息：构建失败、测试不通过都会这样，不该当成工具故障。
		return Result{
			Content:  fmt.Sprintf("%s\n（退出码 %d）", content, exitCode),
			Artifact: artifact,
		}, nil
	}
	return Result{Content: content, Artifact: artifact}, nil
}

// run 执行命令，返回合并输出、退出码与是否超时。
func (t *BashTool) run(ctx context.Context, command string, timeout time.Duration) (string, int, bool) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := t.shell.command(runCtx, command)
	if dir := t.guard.WorkDir(); dir != "" {
		cmd.Dir = dir
	}
	capture := &limitedBuffer{limit: maxCapturedBytes}
	cmd.Stdout = capture
	cmd.Stderr = capture
	// 子进程可能继承输出管道且不肯退出（Windows 上 taskkill 偶尔只杀掉 shell）。
	// 没有 WaitDelay 时，Wait 会一直等管道关闭，超时形同虚设——实测被拖到命令自己结束。
	cmd.WaitDelay = 3 * time.Second
	prepareCommand(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("执行失败：无法启动 %s（%s）：%v", t.shell.Name, t.shell.Path, err), -1, false
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case waitErr := <-done:
		return capture.String(), exitCodeOf(waitErr), false
	case <-runCtx.Done():
		// 只杀 shell 不够：go build 之类的子进程会变成孤儿继续跑。
		killTree(cmd)
		// 有 WaitDelay 兜底，这里不会无限等；再加一层保险，避免用户取消后界面卡住。
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return capture.String(), -1, true
	}
}

// exitCodeOf 从 Wait 的错误里取退出码；正常结束返回 0。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// dangerousPatterns 是"不看内容就该多问一句"的命令模式。
//
// 这里只做提示增强，不改变权限判定：能不能执行由 Guard 的三态决定，
// 危险模式的用处是让弹窗把风险写在脸上。
var dangerousPatterns = []*regexp.Regexp{
	// rm 带 -r 或 -f（含组合写法 -rf / -fr / -Rf）
	regexp.MustCompile(`\brm\s+-[a-zA-Z]*[rRfF]`),
	// 丢弃工作区改动
	regexp.MustCompile(`\bgit\s+reset\s+--hard`),
	regexp.MustCompile(`\bgit\s+clean\s+-[a-zA-Z]*[dfx]`),
	// 下载即执行
	regexp.MustCompile(`\b(curl|wget)\b[^|]*\|\s*(ba)?sh`),
	// 格式化/覆写磁盘
	regexp.MustCompile(`\bmkfs(\.\w+)?\b`),
	regexp.MustCompile(`\bdd\s+[^|]*\bof=/dev/`),
	regexp.MustCompile(`>\s*/dev/(sd|nvme|disk)`),
	// 提权与 fork 炸弹
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
}

// isDangerous 判断命令是否命中危险模式。
func isDangerous(command string) bool {
	for _, pattern := range dangerousPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// truncateOutput 按 32KiB 截断，超限部分落盘并返回路径。
//
// 落盘位置是 filesystem 的 artifact 目录（Guard 已把它设为"可读不可写"的例外），
// 因此模型可以回头再读。这里不自己拼路径：一旦两处拼接不一致，
// 就会出现"写进去了但 Guard 不允许读"的死角。
func truncateOutput(output string) (string, string) {
	inline, clipped := clip(output)
	if !clipped {
		return inline, ""
	}

	dir, err := filesystem.BashOutputDir()
	if err != nil {
		return inline + "\n（输出超过 32KiB，但无法确定落盘目录，已截断）", ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return inline + "\n（输出超过 32KiB，但创建落盘目录失败，已截断）", ""
	}

	name := time.Now().Format("20060102-150405") + "-" + shortRandom() + ".log"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		return inline + "\n（输出超过 32KiB，但写入落盘文件失败，已截断）", ""
	}

	return inline + fmt.Sprintf("\n（输出共 %d 字节，已截断；完整内容见 %s，可用 %s 读取）",
		len(output), path, ReadFileToolName), path
}

// shortRandom 用 base64 编一小段随机字节做文件名后缀，避免同一秒内多次调用冲突。
func shortRandom() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// oneLine 把命令压成一行并截断，供弹窗显示。
func oneLine(command string) string {
	collapsed := strings.Join(strings.Fields(command), " ")
	const limit = 120
	if len(collapsed) <= limit {
		return collapsed
	}
	return collapsed[:limit] + "…"
}

// tailString 取字符串尾部 n 字节，按 rune 收尾。
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "…" + s[cut:]
}

// limitedBuffer 是带容量上限的并发安全缓冲。
//
// 命令的 stdout/stderr 由两个 goroutine 同时写，必须加锁；
// 超过上限的部分直接丢弃并记数——目的是让人看到"被截了"，而不是把几个 GB 读进内存。
type limitedBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	limit   int
	dropped int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.dropped += len(p)
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.dropped += len(p) - room
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	text := b.buf.String()
	if b.dropped > 0 {
		text += fmt.Sprintf("\n（输出过多，已丢弃 %d 字节）", b.dropped)
	}
	return text
}
