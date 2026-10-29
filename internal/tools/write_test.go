package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newWriteTool / newEditTool 造工作目录内的写类工具。
func newWriteTool(t *testing.T, dir string, perm PermissionRequester) *WriteFileTool {
	t.Helper()
	return NewWriteFileTool(newToolGuard(t, dir), perm)
}

func newEditTool(t *testing.T, dir string, perm PermissionRequester) *EditFileTool {
	t.Helper()
	return NewEditFileTool(newToolGuard(t, dir), perm)
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	return string(data)
}

// noTempFiles 断言目录里没有残留的临时文件。
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录失败：%v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sycode-") {
			t.Errorf("残留了临时文件 %q", entry.Name())
		}
	}
}

func TestWriteFileCreatesFileAndParents(t *testing.T) {
	dir := t.TempDir()
	tool := newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce})

	result := mustExecute(t, tool, Input{"path": "a/b/c.go", "content": "package c\n"})

	got := readBack(t, filepath.Join(dir, "a", "b", "c.go"))
	if got != "package c\n" {
		t.Errorf("内容不符：%q", got)
	}
	if !strings.Contains(result.Content, "新建") {
		t.Errorf("新建文件应说明是新建：%q", result.Content)
	}
	noTempFiles(t, filepath.Join(dir, "a", "b"))
}

func TestWriteFileOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "旧内容\n")

	mustExecute(t, newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "content": "新内容\n"})

	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "新内容\n" {
		t.Errorf("应被覆盖：%q", got)
	}
}

func TestWriteFileUnchangedSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "一样\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 失败：%v", err)
	}

	result := mustExecute(t, newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "content": "一样\n"})

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 失败：%v", err)
	}
	// 不碰文件：改 mtime 会让 watch/构建白跑一遍。
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("内容未变化时不该重写文件")
	}
	if !strings.Contains(result.Content, "没有变化") {
		t.Errorf("应说明没有变化：%q", result.Content)
	}
}

func TestWriteFileDeniedHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "new.txt")

	// 工作目录外 + 无确认通道 → 拒绝，且文件不能被创建。
	_, err := newWriteTool(t, dir, nil).Execute(context.Background(), Input{"path": target, "content": "x"})
	wantError(t, err, "没有确认通道")

	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("被拒绝时不能在磁盘上留下任何东西")
	}
}

func TestWriteFileUserDenyHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "new.txt")

	_, err := newWriteTool(t, dir, &fakePerm{decision: DecisionDeny}).
		Execute(context.Background(), Input{"path": target, "content": "x"})
	wantError(t, err, "用户拒绝")

	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("用户拒绝后仍创建了文件")
	}
}

func TestWriteFileAsksForWriteInsideWorkdir(t *testing.T) {
	// 工作目录内的写操作是 Pending：必须问，不能默默改。
	dir := t.TempDir()
	perm := &fakePerm{decision: DecisionAllowOnce}

	mustExecute(t, newWriteTool(t, dir, perm), Input{"path": "a.txt", "content": "x"})

	if perm.asked != 1 {
		t.Fatalf("写操作应询问一次，实际 %d 次", perm.asked)
	}
	if perm.last.Op != "write" {
		t.Errorf("询问的操作应为 write，实际 %q", perm.last.Op)
	}
}

func TestWriteFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}

	_, err := newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}).
		Execute(context.Background(), Input{"path": "sub", "content": "x"})
	wantError(t, err, "是目录")
}

func TestWriteFileRejectsBinaryTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(path, []byte("PK\x00\x00"), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	_, err := newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}).
		Execute(context.Background(), Input{"path": "a.bin", "content": "text"})
	wantError(t, err, "二进制")
}

func TestWriteFileAllowsEmptyContent(t *testing.T) {
	// 清空文件是合法操作。
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "有内容\n")

	mustExecute(t, newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "content": ""})

	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "" {
		t.Errorf("应被清空，实际 %q", got)
	}
}

func TestWriteFileValidateInput(t *testing.T) {
	tool := newWriteTool(t, t.TempDir(), nil)
	wantError(t, tool.ValidateInput(context.Background(), Input{"content": "x"}), "缺少必填字段")
	wantError(t, tool.ValidateInput(context.Background(), Input{"path": "a"}), "缺少必填字段")
	wantError(t, tool.ValidateInput(context.Background(), Input{"path": "a", "content": 1.0}), "必须是字符串")
}

func TestWriteFileShowsDiff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "第一行\n第二行\n")

	result := mustExecute(t, newWriteTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "content": "第一行\n改过的第二行\n"})

	if result.Display == nil || result.Display.Kind != DisplayDiff {
		t.Fatalf("改动应产生 diff 展示：%+v", result.Display)
	}
	joined := strings.Join(result.Display.Lines, "\n")
	if !strings.Contains(joined, "- 第二行") || !strings.Contains(joined, "+ 改过的第二行") {
		t.Errorf("diff 应包含删除与新增：\n%s", joined)
	}
}

func TestEditFileReplacesUniqueMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n\nfunc old() {}\n\nfunc keep() {}\n")

	result := mustExecute(t, newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.go", "old_string": "func old() {}", "new_string": "func renamed() {}"})

	got := readBack(t, filepath.Join(dir, "a.go"))
	if !strings.Contains(got, "func renamed() {}") || strings.Contains(got, "func old() {}") {
		t.Errorf("替换未生效：%q", got)
	}
	// 其余内容必须原样保留。
	if !strings.Contains(got, "func keep() {}") || !strings.HasPrefix(got, "package a\n") {
		t.Errorf("替换破坏了其它内容：%q", got)
	}
	if !strings.Contains(result.Content, "已修改") {
		t.Errorf("应报告修改结果：%q", result.Content)
	}
}

func TestEditFileDeletion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "keep1\nremove me\nkeep2\n")

	mustExecute(t, newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "old_string": "remove me\n", "new_string": ""})

	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "keep1\nkeep2\n" {
		t.Errorf("删除结果不符：%q", got)
	}
}

func TestEditFileNoMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello\n")

	_, err := newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}).
		Execute(context.Background(), Input{"path": "a.txt", "old_string": "bye", "new_string": "x"})
	wantError(t, err, "没有找到")
	// 提示要指向下一步，而不是只说"失败了"。
	if !strings.Contains(err.Error(), GrepToolName) {
		t.Errorf("应建议用 grep 确认原文：%v", err)
	}
}

func TestEditFileAmbiguousMatchListsLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "dup\ndup\ndup\n")

	_, err := newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}).
		Execute(context.Background(), Input{"path": "a.txt", "old_string": "dup", "new_string": "x"})
	wantError(t, err, "出现")
	// 行号是模型补上下文的关键信息。
	for _, want := range []string{"第 1、2、3 行"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误应包含 %q：%v", want, err)
		}
	}
	// 歧义时文件不能被改。
	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "dup\ndup\ndup\n" {
		t.Errorf("歧义匹配时不该改动文件：%q", got)
	}
}

func TestEditFileDeniedHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	target := writeFile(t, outsideDir, "a.txt", "原样\n")

	_, err := newEditTool(t, dir, &fakePerm{decision: DecisionDeny}).
		Execute(context.Background(), Input{"path": target, "old_string": "原样", "new_string": "改过"})
	wantError(t, err, "用户拒绝")

	if got := readBack(t, target); got != "原样\n" {
		t.Errorf("被拒绝时文件不该变化：%q", got)
	}
}

func TestEditFileMissingFileSuggestsWrite(t *testing.T) {
	dir := t.TempDir()
	_, err := newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}).
		Execute(context.Background(), Input{"path": "nope.txt", "old_string": "a", "new_string": "b"})
	wantError(t, err, WriteFileToolName)
}

func TestEditFileEmptyOldStringIsRejected(t *testing.T) {
	tool := newEditTool(t, t.TempDir(), nil)
	// 空 old_string 在任何位置都匹配，替换只会随机毁文件。
	wantError(t, tool.ValidateInput(context.Background(), Input{"path": "a", "old_string": "", "new_string": "x"}), "不能为空")
}

func TestEditFileUnchangedSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "same\n")
	before, _ := os.Stat(path)

	result := mustExecute(t, newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "old_string": "same", "new_string": "same"})

	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("内容未变化时不该重写文件")
	}
	if !strings.Contains(result.Content, "没有变化") {
		t.Errorf("应说明没有变化：%q", result.Content)
	}
}

func TestEditFileShowsDiff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\nb\nc\n")

	result := mustExecute(t, newEditTool(t, dir, &fakePerm{decision: DecisionAllowOnce}),
		Input{"path": "a.txt", "old_string": "b", "new_string": "B"})

	if result.Display == nil || result.Display.Kind != DisplayDiff {
		t.Fatalf("应产生 diff：%+v", result.Display)
	}
	joined := strings.Join(result.Display.Lines, "\n")
	if !strings.Contains(joined, "- b") || !strings.Contains(joined, "+ B") {
		t.Errorf("diff 内容不符：\n%s", joined)
	}
	// 上下文行有助定位：保留一行前后。
	if !strings.Contains(joined, "  a") && !strings.Contains(joined, "  c") {
		t.Errorf("diff 应保留上下文行：\n%s", joined)
	}
}

func TestBuildDiff(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     string
	}{
		{"相同内容无 diff", "a\n", "a\n", ""},
		{"纯新增", "a\n", "a\nb\n", "  a\n+ b"},
		{"纯删除", "a\nb\n", "a\n", "  a\n- b"},
		{"中间修改", "a\nb\nc\n", "a\nB\nc\n", "  a\n- b\n+ B\n  c"},
		{"全文替换", "x\n", "y\n", "- x\n+ y"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(formatDiff(buildDiff(c.old, c.new)), "\n")
			if c.want == "" {
				if len(buildDiff(c.old, c.new)) != 0 {
					t.Errorf("相同内容不该产生 diff：%q", got)
				}
				return
			}
			if got != c.want {
				t.Errorf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

func TestDiffSummary(t *testing.T) {
	diff := []EditDiffLine{{Kind: '+', Text: "a"}, {Kind: '+', Text: "b"}, {Kind: '-', Text: "c"}}
	if got := diffSummary(diff); got != "+2 / -1" {
		t.Errorf("摘要不符：%q", got)
	}
}

func TestLocateMatchesReportsLimit(t *testing.T) {
	content := strings.Repeat("dup\n", 10)
	lines := locateMatches(content, "dup")

	// 上报上限是 5，第 6 处用 -1 表示"还有更多"。
	if len(lines) != maxMatchReport+1 {
		t.Fatalf("应上报 %d 个元素（含省略标记），实际 %d", maxMatchReport+1, len(lines))
	}
	if lines[len(lines)-1] != -1 {
		t.Errorf("最后一个应为 -1 表示还有更多：%v", lines)
	}
	if joined := joinLines(lines); !strings.Contains(joined, "…") {
		t.Errorf("省略应体现为省略号：%q", joined)
	}
}
