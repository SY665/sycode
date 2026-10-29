package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sy665/sycode/internal/filesystem"
)

// newToolGuard 造一个带 .gitignore 的 Guard，供遍历类工具用。
func newToolGuard(t *testing.T, dir string) *filesystem.Guard {
	t.Helper()
	git, err := filesystem.NewGitAwareness(dir)
	if err != nil {
		t.Fatalf("构造 GitAwareness 失败：%v", err)
	}
	return filesystem.NewGuard(dir, git)
}

// tree 按 "相对路径: 内容" 建一棵测试树。
func tree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
		why     string
	}{
		{"*.go", "main.go", true, "段内通配"},
		{"*.go", "internal/main.go", false, "* 不跨目录"},
		{"**/*.go", "main.go", true, "**/ 允许零层目录"},
		{"**/*.go", "internal/llm/client.go", true, "** 跨任意层"},
		{"internal/**/*.go", "internal/a/b/c.go", true, "中层 **"},
		{"internal/**", "internal/a/b.go", true, "尾部 **"},
		{"a/**", "a", true, "** 也能匹配空层"},
		{"**", "any/deep/path.txt", true, "全匹配"},
		{"main.?o", "main.go", true, "? 单字符"},
		{"main.?o", "main.goo", false, "? 只一个字符"},
		{"[mn]ain.go", "main.go", true, "字符集"},
		{"[!mn]ain.go", "main.go", false, "取反字符集"},
		{"src/*.{go}", "src/a.go", false, "花括号不展开（已知限制）"},
		{"docs/**/x.md", "docs/x.md", true, "** 零层"},
		{"internal/llm/*.go", "internal\\llm\\client.go", true, "Windows 分隔符也要命中"},
		{"**/*_test.go", "internal/tools/read_file_test.go", true, "常见用法"},
	}

	for _, c := range cases {
		t.Run(c.pattern+"→"+c.path, func(t *testing.T) {
			if got := matchPattern(c.pattern, c.path); got != c.want {
				t.Errorf("期望 %v（%s），实际 %v", c.want, c.why, got)
			}
		})
	}
}

func TestCompileGlobRejectsBadPatterns(t *testing.T) {
	cases := []struct{ pattern, want string }{
		{"", "模式为空"},
		{"a[b", "没有闭合"},
		{"a[]", "是空的"},
	}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			_, err := compileGlob(c.pattern)
			wantError(t, err, c.want)
		})
	}
}

func TestGlobFindsFiles(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"main.go":                 "package main\n",
		"internal/llm/client.go":  "package llm\n",
		"internal/llm/client.txt": "note\n",
		"internal/tools/tool.go":  "package tools\n",
		"docs/readme.md":          "# hi\n",
	})

	tool := NewGlobTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": "**/*.go"})

	lines := strings.Split(result.Content, "\n")
	if len(lines) != 3 {
		t.Fatalf("应找到 3 个 .go 文件，实际 %d：%v", len(lines), lines)
	}
	// 输出必须稳定（字典序），否则模型两次调用看到的顺序不同。
	want := []string{"internal/llm/client.go", "internal/tools/tool.go", "main.go"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("第 %d 行应为 %q，实际 %q", i, w, lines[i])
		}
	}
}

func TestGlobHonoursLimit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files["f"+string(rune('a'+i))+".go"] = "package x\n"
	}
	tree(t, dir, files)

	tool := NewGlobTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": "*.go", "limit": float64(3)})

	lines := strings.Split(strings.Split(result.Content, "\n（只列出")[0], "\n")
	if len(lines) != 3 {
		t.Fatalf("应只返回 3 条，实际 %d", len(lines))
	}
	if !strings.Contains(result.Content, "只列出前 3 条") {
		t.Errorf("截断要提示，否则模型以为只有这些：%q", result.Content)
	}
}

func TestGlobSkipsIgnoredAndVCS(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		".gitignore":     "dist/\n*.log\n",
		"main.go":        "package main\n",
		"app.log":        "noise\n",
		"dist/bundle.js": "noise\n",
		".git/config":    "[core]\n",
	})

	tool := NewGlobTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": "**/*"})

	for _, banned := range []string{"app.log", "bundle.js", ".git/config"} {
		if strings.Contains(result.Content, banned) {
			t.Errorf("不该出现 %q：%q", banned, result.Content)
		}
	}
	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("应包含 main.go：%q", result.Content)
	}
}

func TestGlobSubdirectory(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"a/x.go": "package a\n",
		"b/y.go": "package b\n",
	})

	tool := NewGlobTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": "*.go", "path": "b"})

	if result.Content != "y.go" {
		t.Errorf("应以 path 为起点且相对它输出，实际 %q", result.Content)
	}
}

func TestGlobNoMatch(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"main.go": "package main\n"})

	tool := NewGlobTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": "**/*.rs"})

	if !strings.Contains(result.Content, "没有匹配") {
		t.Errorf("无匹配要说清楚，而不是空字符串：%q", result.Content)
	}
	// 无匹配时也要提示下一步。
	if !strings.Contains(result.Content, "放宽") {
		t.Errorf("应给出可执行的建议：%q", result.Content)
	}
}

func TestGlobRejectsFileAsBase(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"main.go": "package main\n"})

	_, err := NewGlobTool(newToolGuard(t, dir), nil).Execute(context.Background(), Input{"pattern": "*.go", "path": "main.go"})
	wantError(t, err, "不是目录")
}

func TestGlobRequiresPermissionForOutsideBase(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	tree(t, outside, map[string]string{"x.go": "package x\n"})

	// 没有确认通道：起点在工作目录外时必须拒绝。
	_, err := NewGlobTool(newToolGuard(t, dir), nil).Execute(context.Background(), Input{"pattern": "*.go", "path": outside})
	wantError(t, err, "没有确认通道")

	// 有通道且允许：能列出结果，且不会为每个文件再问一次。
	perm := &fakePerm{decision: DecisionAllowOnce}
	result := mustExecute(t, NewGlobTool(newToolGuard(t, dir), perm), Input{"pattern": "*.go", "path": outside})
	if perm.asked != 1 {
		t.Errorf("起点只该问一次，实际 %d 次", perm.asked)
	}
	if !strings.Contains(result.Content, "x.go") {
		t.Errorf("应列出文件：%q", result.Content)
	}
}

func TestGrepFindsMatches(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"main.go":        "package main\n\nfunc main() {}\n",
		"internal/a.go":  "package internal\n\nfunc helper() {}\n",
		"docs/readme.md": "func 出现在文档里\n",
	})

	tool := NewGrepTool(newToolGuard(t, dir), nil)
	result := mustExecute(t, tool, Input{"pattern": `func \w+\(`, "include": "**/*.go"})

	if !strings.Contains(result.Content, "main.go:3: func main() {}") {
		t.Errorf("命中格式应为「路径:行号: 内容」：%q", result.Content)
	}
	if !strings.Contains(result.Content, "internal/a.go:3: func helper() {}") {
		t.Errorf("应包含子目录命中：%q", result.Content)
	}
	// include 过滤掉 markdown。
	if strings.Contains(result.Content, "readme.md") {
		t.Errorf("include 未生效：%q", result.Content)
	}
}

func TestGrepWithoutIncludeSearchesAll(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"a.txt": "needle\n",
		"b.go":  "needle\n",
	})

	result := mustExecute(t, NewGrepTool(newToolGuard(t, dir), nil), Input{"pattern": "needle"})
	if !strings.Contains(result.Content, "a.txt:1") || !strings.Contains(result.Content, "b.go:1") {
		t.Errorf("不带 include 应搜所有文本文件：%q", result.Content)
	}
}

func TestGrepSkipsBinaryAndIgnored(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		".gitignore":  "vendor/\n",
		"ok.txt":      "needle\n",
		"vendor/x.go": "needle\n",
	})
	// 二进制文件里也放 needle，但前面有 NUL。
	binaryPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binaryPath, []byte("needle\x00\x00"), 0o600); err != nil {
		t.Fatalf("写二进制文件失败：%v", err)
	}

	result := mustExecute(t, NewGrepTool(newToolGuard(t, dir), nil), Input{"pattern": "needle"})

	if strings.Contains(result.Content, "blob.bin") {
		t.Errorf("二进制文件应跳过：%q", result.Content)
	}
	if strings.Contains(result.Content, "vendor") {
		t.Errorf("被忽略的目录应跳过：%q", result.Content)
	}
	if !strings.Contains(result.Content, "ok.txt:1") {
		t.Errorf("正常文件应命中：%q", result.Content)
	}
}

func TestGrepSingleFile(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"a.go": "package a\n// needle\n"})

	result := mustExecute(t, NewGrepTool(newToolGuard(t, dir), nil), Input{"pattern": "needle", "path": "a.go"})
	// 单文件起点也要给出文件名，而不是 "."。
	if !strings.Contains(result.Content, "a.go:2: // needle") {
		t.Errorf("单文件结果不符：%q", result.Content)
	}
}

func TestGrepLimitAndTrailer(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "hit")
	}
	tree(t, dir, map[string]string{"a.txt": strings.Join(lines, "\n") + "\n"})

	result := mustExecute(t, NewGrepTool(newToolGuard(t, dir), nil), Input{"pattern": "hit", "limit": float64(5)})
	if !strings.Contains(result.Content, "只返回前 5 条") {
		t.Errorf("截断要有提示：%q", result.Content)
	}
}

func TestGrepNoMatch(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"a.txt": "hello\n"})

	result := mustExecute(t, NewGrepTool(newToolGuard(t, dir), nil), Input{"pattern": "缺"})
	if !strings.Contains(result.Content, "没有找到") {
		t.Errorf("无命中要说清楚：%q", result.Content)
	}
	if !strings.Contains(result.Content, GlobToolName) {
		t.Errorf("应建议先用 glob 确认范围：%q", result.Content)
	}
}

func TestGrepInvalidRegex(t *testing.T) {
	tool := NewGrepTool(newToolGuard(t, t.TempDir()), nil)
	// 错误要带编译器的原因，模型才能改对。
	wantError(t, tool.ValidateInput(context.Background(), Input{"pattern": "a("}), "无法编译")
}

func TestGrepValidation(t *testing.T) {
	tool := NewGrepTool(newToolGuard(t, t.TempDir()), nil)
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"缺 pattern", Input{}, "缺少必填字段"},
		{"limit 超上限", Input{"pattern": "x", "limit": float64(maxGrepLimit + 1)}, "最大"},
		{"include 非法", Input{"pattern": "x", "include": "["}, "没有闭合"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantError(t, tool.ValidateInput(context.Background(), c.in), c.want)
		})
	}
}

func TestGrepCancelledContext(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"a.txt": "needle\n"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewGrepTool(newToolGuard(t, dir), nil).Execute(ctx, Input{"pattern": "needle"})
	wantError(t, err, "超时")
}
