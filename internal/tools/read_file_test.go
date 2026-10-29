package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sy665/sycode/internal/filesystem"
)

// fakePerm 记录询问次数并按预设回答，用于测"要不要问用户"。
type fakePerm struct {
	decision Decision
	err      error
	asked    int
	last     PermissionRequest
}

func (f *fakePerm) RequestPermission(_ context.Context, req PermissionRequest) (Decision, error) {
	f.asked++
	f.last = req
	return f.decision, f.err
}

// newReadTool 造一个工作目录内的读取工具。
func newReadTool(t *testing.T, dir string, perm PermissionRequester) *ReadFileTool {
	t.Helper()
	return NewReadFileTool(filesystem.NewGuard(dir, nil), perm)
}

// writeFile 在 dir 下写文件（自动建父目录）。
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	return path
}

// mustExecute 执行工具并要求成功；参数用 Tool 接口，读/找/搜三类工具共用。
func mustExecute(t *testing.T, tool Tool, in Input) Result {
	t.Helper()
	result, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	return result
}

func wantError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望包含 %q 的错误，实际没有错误", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("期望包含 %q，实际：%v", substr, err)
	}
}

func TestReadFileWholeFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	result := mustExecute(t, newReadTool(t, dir, nil), Input{"path": "main.go"})

	if !strings.Contains(result.Content, "func main()") {
		t.Errorf("内容不符：%q", result.Content)
	}
	// 模型那份不带行号：它要拿原文去精确替换。
	if strings.Contains(result.Content, "│") {
		t.Errorf("给模型的内容不该带行号：%q", result.Content)
	}
	// 给人看的那份带行号。
	if result.Display == nil || len(result.Display.Lines) != 3 {
		t.Fatalf("Display 行数不符：%+v", result.Display)
	}
	if !strings.Contains(result.Display.Lines[2], "│ func main()") {
		t.Errorf("Display 应带行号：%q", result.Display.Lines[2])
	}
}

func TestReadFileOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "1\n2\n3\n4\n5\n")

	result := mustExecute(t, newReadTool(t, dir, nil), Input{"path": "a.txt", "offset": float64(2), "limit": float64(2)})

	if result.Content != "2\n3\n（已读到 2-3 行，共 5 行；继续读请用 offset=4）" {
		t.Errorf("分段读取结果不符：%q", result.Content)
	}
}

func TestReadFileLastChunkHasNoTrailer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "1\n2\n3\n")

	result := mustExecute(t, newReadTool(t, dir, nil), Input{"path": "a.txt", "offset": float64(2), "limit": float64(10)})

	// 读到文件尾就不该再提示"继续读"。
	if strings.Contains(result.Content, "继续读") {
		t.Errorf("读完最后一段不该提示继续：%q", result.Content)
	}
	if result.Content != "2\n3" {
		t.Errorf("内容不符：%q", result.Content)
	}
}

func TestReadFileOffsetBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "1\n2\n")

	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "a.txt", "offset": float64(10)})
	wantError(t, err, "超出文件行数")
}

func TestReadFileEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")

	result := mustExecute(t, newReadTool(t, dir, nil), Input{"path": "empty.txt"})
	if result.Content != "" {
		t.Errorf("空文件应返回空内容，实际 %q", result.Content)
	}
}

func TestReadFileMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "nope.go"})
	wantError(t, err, "不存在")
}

func TestReadFileDirectoryIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}

	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "sub"})
	wantError(t, err, "是目录")
}

func TestReadFileBinaryIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(path, []byte("PK\x03\x04\x00\x00binary"), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "a.bin"})
	wantError(t, err, "二进制")
}

func TestReadFileHugeFileSuggestsGrep(t *testing.T) {
	if testing.Short() {
		t.Skip("会创建一个稀疏大文件")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建文件失败：%v", err)
	}
	// 用 Seek 造一个超过上限的稀疏文件，不实际写满磁盘。
	if err := file.Truncate(maxReadBytes + 1); err != nil {
		file.Close()
		t.Skipf("当前文件系统不支持 Truncate：%v", err)
	}
	file.Close()

	_, err = newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "huge.log"})
	wantError(t, err, "grep")
}

func TestReadFileClipLongContent(t *testing.T) {
	dir := t.TempDir()
	// 单行超长：clip 会按字节截断并提示。
	writeFile(t, dir, "long.txt", strings.Repeat("汉", maxInlineBytes))

	result := mustExecute(t, newReadTool(t, dir, nil), Input{"path": "long.txt"})
	if !strings.Contains(result.Content, "已截断") {
		t.Errorf("超长内容应提示截断：%q", tail(result.Content, 120))
	}
	if len(result.Content) > maxInlineBytes+200 {
		t.Errorf("截断后仍然过长：%d", len(result.Content))
	}
}

func TestReadFilePermissionDenied(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x")

	// 系统路径必被拒绝（不依赖工作目录内外）。
	blocked := filesystem.NewGuard(dir, nil)
	_ = blocked
	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": "~/.ssh/id_rsa"})
	wantError(t, err, "权限不足")
}

func TestReadFilePendingAsksUser(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := writeFile(t, outside, "outside.txt", "邻居目录的内容")

	// 工作目录外 → Pending：必须问用户。
	perm := &fakePerm{decision: DecisionAllowOnce}
	result := mustExecute(t, newReadTool(t, dir, perm), Input{"path": target})

	if perm.asked != 1 {
		t.Fatalf("应询问一次，实际 %d 次", perm.asked)
	}
	if perm.last.Op != string(filesystem.OpRead) || perm.last.Path != target {
		t.Errorf("询问内容不符：%+v", perm.last)
	}
	if perm.last.Tool != ReadFileToolName {
		t.Errorf("询问应带工具名：%q", perm.last.Tool)
	}
	if !strings.Contains(result.Content, "邻居目录") {
		t.Errorf("允许后应读到内容：%q", result.Content)
	}
}

func TestReadFilePendingDeniedByUser(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := writeFile(t, outside, "outside.txt", "不该读到")

	_, err := newReadTool(t, dir, &fakePerm{decision: DecisionDeny}).Execute(context.Background(), Input{"path": target})
	wantError(t, err, "用户拒绝")
	// 拒绝的文案要像"决定"，不能像"故障"，否则模型会换个说法再试。
	if strings.Contains(err.Error(), "执行失败") {
		t.Errorf("用户拒绝不该说成执行失败：%v", err)
	}
}

func TestReadFilePendingWithoutRequester(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := writeFile(t, outside, "outside.txt", "x")

	// 没有确认通道时必须拒绝并说清原因，而不是默认放行。
	_, err := newReadTool(t, dir, nil).Execute(context.Background(), Input{"path": target})
	wantError(t, err, "没有确认通道")
}

func TestReadFilePermissionRequesterError(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := writeFile(t, outside, "outside.txt", "x")

	_, err := newReadTool(t, dir, &fakePerm{err: errors.New("界面崩了")}).Execute(context.Background(), Input{"path": target})
	wantError(t, err, "等待权限确认时出错")
}

func TestReadFileValidateInput(t *testing.T) {
	tool := newReadTool(t, t.TempDir(), nil)

	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"缺 path", Input{}, "缺少必填字段"},
		{"path 类型错", Input{"path": 42.0}, "必须是字符串"},
		{"path 空串", Input{"path": ""}, "不能为空串"},
		{"offset 为 0", Input{"path": "a", "offset": float64(0)}, "从 1 开始"},
		{"offset 非整数", Input{"path": "a", "offset": 1.5}, "必须是整数"},
		{"limit 为 0", Input{"path": "a", "limit": float64(0)}, "必须为正数"},
		{"limit 超上限", Input{"path": "a", "limit": float64(maxReadLimit + 1)}, "最大"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantError(t, tool.ValidateInput(context.Background(), c.in), c.want)
		})
	}
}

func TestReadFileCancelledContext(t *testing.T) {
	// 上下文已取消：权限询问应立刻失败，而不是傻等。
	dir := t.TempDir()
	outside := t.TempDir()
	target := writeFile(t, outside, "outside.txt", "x")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	perm := &cancelAwarePerm{}
	_, err := newReadTool(t, dir, perm).Execute(ctx, Input{"path": target})
	wantError(t, err, "等待权限确认时出错")
}

// cancelAwarePerm 模拟"界面按 ctx 取消时立刻返回错误"。
type cancelAwarePerm struct{}

func (cancelAwarePerm) RequestPermission(ctx context.Context, _ PermissionRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return DecisionDeny, err
	}
	return DecisionAllowOnce, nil
}

// tail 取字符串末尾 n 个字符，便于断言时看到截断提示。
func tail(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
