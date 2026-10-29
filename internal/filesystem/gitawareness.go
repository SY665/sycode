// Package filesystem 是唯一的文件访问入口：路径解析、权限三态判定、.gitignore 与敏感路径。
//
// 本包**不执行读写**（ADR-005）：Guard 只回答"能不能"，工具负责"问 → 做"。
// 任何绕过 Guard 直接 os.Open 的代码都算安全缺陷——评审直接打回。
package filesystem

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// gitignoreFile 是规则文件名，与 git 一致。
const gitignoreFile = ".gitignore"

// GitAwareness 汇总仓库内所有 .gitignore 的规则，回答"这个路径被忽略了吗"。
//
// 忽略的路径一律不许碰：那里通常是可重新生成的产物（node_modules、dist、日志），
// 让模型去读它既浪费上下文，也可能读到构建残留而误判源码。
type GitAwareness struct {
	root    string
	matcher gitignore.Matcher
}

// NewGitAwareness 递归载入 root 下的 .gitignore。
//
// 遍历时跳过 .git，且不下沉到已被忽略的目录：那些目录里的 .gitignore 在 git 里本就不生效，
// 进去既慢又没有意义（node_modules 就是典型）。
func NewGitAwareness(root string) (*GitAwareness, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// 必须解析到与 IsIgnored 收到的路径同一形态（后者来自 Guard.Resolve）。
	// 不解析的话，macOS 的 /var → /private/var 会让 Rel 直接落到"根之外"，忽略规则全部失效。
	abs = resolveSymlinks(filepath.Clean(abs))

	var patterns []gitignore.Pattern
	collectPatterns(abs, nil, &patterns)

	// NewMatcher 要求"优先级递增"的顺序，递归遍历天然满足：父目录先于子目录。
	return &GitAwareness{root: abs, matcher: gitignore.NewMatcher(patterns)}, nil
}

// Root 返回规则根目录（已绝对化）。
func (g *GitAwareness) Root() string { return g.root }

// IsIgnored 判断路径是否被忽略。
//
// 根目录自身与根目录外的路径一律返回 false：前者不该被"忽略"这张网罩住，
// 后者不归这套规则管（工作目录外的判定由 Guard 的其它规则负责）。
func (g *GitAwareness) IsIgnored(path string) bool {
	// 根的形态是"解析后"，所以路径也得是。Guard 传进来的已经是（它先 Resolve），
	// 直接调用方（测试、将来的工具）不一定。前缀对不上时补一次解析，
	// 别让调用约定变成隐式要求——那正是 macOS 上忽略规则整体失效的原因。
	if !isUnder(g.root, path) {
		path = resolveSymlinks(path)
	}

	rel, err := filepath.Rel(g.root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	return g.matcher.Match(segments, isDir(path))
}

// collectPatterns 深度优先收集规则：先读本层 .gitignore，再进子目录。
//
// 顺序有语义：子目录规则优先级更高，而 NewMatcher 按"先出现的优先级低"处理，
// 所以必须父目录在前。domain 是该 .gitignore 所在目录相对 root 的路径段，
// 保证 `build/` 这种模式只在本层生效。
func collectPatterns(dir string, domain []string, patterns *[]gitignore.Pattern) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 读不到就当没有规则：一个权限不足的子目录不该让整个程序起不来。
		return
	}

	// 先处理本层的 .gitignore，后面的子目录遍历才会带上它。
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() != gitignoreFile {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		*patterns = append(*patterns, parseIgnoreFile(data, domain)...)
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".git" {
			continue
		}
		child := append(append([]string{}, domain...), entry.Name())

		// 已忽略的目录不再下沉：规则在 git 语义里也不生效，且能避免遍历 node_modules。
		// 这里临时建 matcher 是 O(n²)，但每个目录只建一次、规则数很小，值得。
		if gitignore.NewMatcher(*patterns).Match(child, true) {
			continue
		}
		collectPatterns(filepath.Join(dir, entry.Name()), child, patterns)
	}
}

// parseIgnoreFile 把一个 .gitignore 文件的内容转成规则。
func parseIgnoreFile(data []byte, domain []string) []gitignore.Pattern {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

// isDir 判断是否为目录；路径不存在时按"不是目录"处理（新建文件的常态）。
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
