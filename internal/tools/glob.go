package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sy665/sycode/internal/filesystem"
)

// GlobToolName 是工具名。
const GlobToolName = "glob"

const (
	// defaultGlobLimit 是默认返回条数：够看结构，又不会把整棵树的路径灌进上下文。
	defaultGlobLimit = 200
	// maxGlobLimit 是上限；再多说明该缩小 pattern 而不是加数量。
	maxGlobLimit = 2000
	// maxGlobVisited 是遍历条目上限，防止在超大目录上转很久。
	maxGlobVisited = 50000
)

// vcsDirs 是遍历时直接跳过的目录：里面没有源码，而且可能有凭证。
var vcsDirs = map[string]bool{".git": true, ".svn": true, ".hg": true}

// GlobTool 按模式列文件路径。
type GlobTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
}

// NewGlobTool 注入依赖。
func NewGlobTool(guard *filesystem.Guard, perm PermissionRequester) *GlobTool {
	return &GlobTool{guard: guard, perm: perm}
}

func (t *GlobTool) Name() string { return GlobToolName }

func (t *GlobTool) Description() string {
	return "按 glob 模式列出工作目录内的文件路径（只列文件，不含目录内容）。" +
		"模式用 / 分隔，支持 *、?、**（任意层目录）、[abc]；例如 \"**/*.go\"、" +
		"\"internal/**/*_test.go\"。默认以工作目录为起点，可用 path 指定子目录。" +
		"被 .gitignore 忽略的路径不会出现。只读，不修改任何文件。"
}

func (t *GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"pattern": map[string]any{
			"type":        "string",
			"description": "glob 模式，例如 **/*.go；相对 path（默认工作目录）",
		},
		"path": map[string]any{
			"type":        "string",
			"description": "起始目录，默认工作目录",
		},
		"limit": map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("最多返回多少条，默认 %d、上限 %d", defaultGlobLimit, maxGlobLimit),
		},
	}
}

func (t *GlobTool) ValidateInput(_ context.Context, in Input) error {
	if _, err := requireString(in, "pattern"); err != nil {
		return err
	}
	pattern, _ := requireString(in, "pattern")
	if _, err := compileGlob(pattern); err != nil {
		return err
	}
	if _, err := optionalString(in, "path"); err != nil {
		return err
	}
	limit, err := optionalInt(in, "limit", defaultGlobLimit)
	if err != nil {
		return err
	}
	if limit < 1 {
		return fmt.Errorf("参数错误：字段 \"limit\" 必须为正数，实际是 %d", limit)
	}
	if limit > maxGlobLimit {
		return fmt.Errorf("参数错误：字段 \"limit\" 最大 %d，实际是 %d。请缩小 pattern", maxGlobLimit, limit)
	}
	return nil
}

// Execute 列出匹配的文件。
//
// 起点只问一次权限：如果起点在工作目录外，用户已经为"在这个目录里找"点过头，
// 再为每个子文件弹一次窗只会把人逼疯。
func (t *GlobTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	pattern, _ := requireString(in, "pattern")
	base, _ := optionalString(in, "path")
	limit, _ := optionalInt(in, "limit", defaultGlobLimit)

	if base == "" {
		base = "."
	}
	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   GlobToolName,
		path:   base,
		op:     filesystem.OpRead,
		detail: "在目录里按模式查找文件",
	}); err != nil {
		return Result{}, err
	}

	root, err := t.guard.Resolve(base)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：%w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return Result{}, fmt.Errorf("执行失败：目录 %q 不存在或不可访问", base)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("参数错误：%q 不是目录。要读单个文件请用 %s", base, ReadFileToolName)
	}

	matches, visited, truncated := t.walk(root, pattern, limit)

	if len(matches) == 0 {
		return Result{
			Content: fmt.Sprintf("没有匹配 %q 的文件（共检查 %d 项）。可以放宽模式，或换一个起始目录", pattern, visited),
			Display: &Display{Kind: DisplayList, Title: "无匹配"},
		}, nil
	}

	content := strings.Join(matches, "\n")
	notes := make([]string, 0, 2)
	if truncated {
		notes = append(notes, fmt.Sprintf("（只列出前 %d 条；共检查 %d 项。要更多请缩小 pattern 或提高 limit，上限 %d）",
			len(matches), visited, maxGlobLimit))
	}
	if visited >= maxGlobVisited {
		notes = append(notes, fmt.Sprintf("（遍历在 %d 项处停止；请缩小起始目录）", maxGlobVisited))
	}
	if len(notes) > 0 {
		content += "\n" + strings.Join(notes, "\n")
	}

	return Result{
		Content: content,
		Display: &Display{Kind: DisplayList, Title: fmt.Sprintf("匹配 %q：%d 个文件", pattern, len(matches)), Lines: matches},
	}, nil
}

// walk 按字典序遍历并收集匹配项。
//
// 用 WalkDir 而不是自己递归：它自带"按名排序"的保证，输出稳定，模型两次调用结果一致。
// 遇到被忽略的目录直接 SkipDir —— 那里面通常是产物，看它既费时间又误导。
func (t *GlobTool) walk(root, pattern string, limit int) (matches []string, visited int, truncated bool) {
	matcher, err := compileGlob(pattern)
	if err != nil {
		return nil, 0, false
	}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// 单个目录读不了不该让整次查找失败：跳过它继续。
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path != root && vcsDirs[entry.Name()] && entry.IsDir() {
			return fs.SkipDir
		}
		if path != root && t.guard != nil && t.guard.IsIgnored(path) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		visited++
		if visited >= maxGlobVisited {
			return fs.SkipAll
		}
		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if !matcher.MatchString(filepath.ToSlash(rel)) {
			return nil
		}

		if len(matches) >= limit {
			truncated = true
			return fs.SkipAll
		}
		matches = append(matches, filepath.ToSlash(rel))
		return nil
	})

	// WalkDir 被 SkipAll 中断时返回 nil，其它错误也不该让"找到了什么"白丢。
	_ = walkErr
	return matches, visited, truncated
}
