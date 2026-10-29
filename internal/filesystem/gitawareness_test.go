package filesystem

import (
	"path/filepath"
	"testing"
)

// newRepo 造一个带若干 .gitignore 的目录树。
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(root, name), content)
	}
	return root
}

func TestGitAwarenessWithoutGitignore(t *testing.T) {
	root := newRepo(t, map[string]string{"main.go": "package main\n"})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	if git.IsIgnored(filepath.Join(root, "main.go")) {
		t.Error("没有 .gitignore 时不该忽略任何东西")
	}
}

func TestGitAwarenessPatterns(t *testing.T) {
	root := newRepo(t, map[string]string{
		".gitignore": "*.log\n!keep.log\ndist/\n/build\n",
	})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	cases := []struct {
		path string
		want bool
		why  string
	}{
		{"app.log", true, "通配后缀命中"},
		{"deep/nested/app.log", true, "不带斜杠的模式在任意层级生效"},
		{"keep.log", false, "! 取反后应恢复"},
		{"dist/bundle.js", true, "目录模式"},
		{"build/out.bin", true, "前导斜杠表示只锚定根目录"},
		{"src/main.go", false, "普通源码不受影响"},
		{"distx/a.js", false, "dist/ 不该匹配 distx"},
	}

	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := git.IsIgnored(filepath.Join(root, c.path)); got != c.want {
				t.Errorf("期望 %v（%s），实际 %v", c.want, c.why, got)
			}
		})
	}
}

func TestGitAwarenessNestedDomain(t *testing.T) {
	// 子目录的规则只在本层生效：这是 gitignore 的语义，写错会误伤整个仓库。
	root := newRepo(t, map[string]string{
		"sub/.gitignore":   "secret.txt\n",
		"sub/secret.txt":   "x",
		"other/secret.txt": "x",
		"other/notes.txt":  "x",
	})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	if !git.IsIgnored(filepath.Join(root, "sub", "secret.txt")) {
		t.Error("子目录内的同名文件应被忽略")
	}
	if git.IsIgnored(filepath.Join(root, "other", "secret.txt")) {
		t.Error("子目录规则不该影响兄弟目录")
	}
	if git.IsIgnored(filepath.Join(root, "other", "notes.txt")) {
		t.Error("未命中的文件不该被忽略")
	}
}

func TestGitAwarenessNestedOverridesRoot(t *testing.T) {
	// 子目录规则优先级更高：根目录忽略 *.txt，子目录用 ! 放行。
	root := newRepo(t, map[string]string{
		".gitignore":      "*.txt\n",
		"docs/.gitignore": "!readme.txt\n",
		"a.txt":           "x",
		"docs/readme.txt": "x",
	})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	if !git.IsIgnored(filepath.Join(root, "a.txt")) {
		t.Error("根目录规则应生效")
	}
	if git.IsIgnored(filepath.Join(root, "docs", "readme.txt")) {
		t.Error("子目录的取反规则优先级更高")
	}
}

func TestGitAwarenessSkipsGitDir(t *testing.T) {
	// .git 里的内容不参与"忽略"判断（它由 Guard 的独立规则拒绝），
	// 而且遍历必须跳过它，否则大仓库启动会变慢。
	root := newRepo(t, map[string]string{
		".gitignore":  "*.log\n",
		".git/config": "[core]\n",
		".git/x.log":  "x",
	})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	if git.IsIgnored(filepath.Join(root, ".git", "config")) {
		t.Error(".git 内部不该按 gitignore 判定")
	}
}

func TestGitAwarenessOutsideRoot(t *testing.T) {
	root := newRepo(t, map[string]string{".gitignore": "*\n"})
	outside := t.TempDir()

	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	if git.IsIgnored(filepath.Join(outside, "anything.txt")) {
		t.Error("根目录外不归这套规则管")
	}
	if git.IsIgnored(root) {
		t.Error("根目录自身不该被判为忽略")
	}
}

func TestGitAwarenessIgnoresCommentsAndBlankLines(t *testing.T) {
	root := newRepo(t, map[string]string{
		".gitignore": "# 注释\n\n   \n*.log\n",
	})
	git, err := NewGitAwareness(root)
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	if !git.IsIgnored(filepath.Join(root, "a.log")) {
		t.Error("前置条件：*.log 应命中")
	}
	if git.IsIgnored(filepath.Join(root, "# 注释")) {
		t.Error("注释行不该变成规则")
	}
}

func TestParseIgnoreFileStripsTrailingSpaces(t *testing.T) {
	// git 语义：行尾空格被忽略（除非用反斜杠转义）。
	patterns := parseIgnoreFile([]byte("*.log   \r\n\r\n# c\r\n"), nil)
	if len(patterns) != 1 {
		t.Fatalf("应只解析出 1 条规则，实际 %d 条", len(patterns))
	}
}
