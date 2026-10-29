package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sy665/sycode/internal/filesystem"
)

// GrepToolName 是工具名。
const GrepToolName = "grep"

const (
	// defaultGrepLimit 是默认命中的行数上限。
	defaultGrepLimit = 100
	// maxGrepLimit 是上限。
	maxGrepLimit = 500
	// maxGrepFileBytes 单文件上限：再大的文件基本是日志或产物，逐行扫没有意义。
	maxGrepFileBytes = 4 << 20
	// grepLineBytes 是单行上限。压缩过的 JS 一行能有几 MB，
	// 不设限会让一次 grep 把内存吃光，而且那种行也没法读。
	grepLineBytes = 256 << 10
)

// GrepTool 按正则搜索文件内容。
type GrepTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
}

// NewGrepTool 注入依赖。
func NewGrepTool(guard *filesystem.Guard, perm PermissionRequester) *GrepTool {
	return &GrepTool{guard: guard, perm: perm}
}

func (t *GrepTool) Name() string { return GrepToolName }

func (t *GrepTool) Description() string {
	return "用 Go 正则（RE2 语法）搜索工作目录内文件的内容，返回「路径:行号: 该行内容」。" +
		"path 指定搜索起点（默认工作目录，可以是单个文件）；include 用 glob 过滤文件名，" +
		"例如 \"**/*.go\"。二进制文件、超 4MiB 的文件、被 .gitignore 忽略的路径都会跳过。只读。"
}

func (t *GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"pattern": map[string]any{
			"type":        "string",
			"description": "Go 正则表达式，例如 func\\s+\\w+\\(",
		},
		"path": map[string]any{
			"type":        "string",
			"description": "搜索起点：目录或单个文件，默认工作目录",
		},
		"include": map[string]any{
			"type":        "string",
			"description": "只搜匹配该 glob 的文件，例如 **/*.go；留空表示所有文本文件",
		},
		"limit": map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("最多返回多少条命中行，默认 %d、上限 %d", defaultGrepLimit, maxGrepLimit),
		},
	}
}

func (t *GrepTool) ValidateInput(_ context.Context, in Input) error {
	pattern, err := requireString(in, "pattern")
	if err != nil {
		return err
	}
	if _, err := regexp.Compile(pattern); err != nil {
		// 直接把编译器的英文原因带上：模型据此能自己改对。
		return fmt.Errorf("参数错误：正则表达式 %q 无法编译：%v", pattern, err)
	}
	if _, err := optionalString(in, "path"); err != nil {
		return err
	}
	include, err := optionalString(in, "include")
	if err != nil {
		return err
	}
	if include != "" {
		if _, err := compileGlob(include); err != nil {
			return err
		}
	}
	limit, err := optionalInt(in, "limit", defaultGrepLimit)
	if err != nil {
		return err
	}
	if limit < 1 {
		return fmt.Errorf("参数错误：字段 \"limit\" 必须为正数，实际是 %d", limit)
	}
	if limit > maxGrepLimit {
		return fmt.Errorf("参数错误：字段 \"limit\" 最大 %d，实际是 %d。请把搜索范围缩小", maxGrepLimit, limit)
	}
	return nil
}

// Execute 遍历并匹配。
func (t *GrepTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	pattern, _ := requireString(in, "pattern")
	base, _ := optionalString(in, "path")
	include, _ := optionalString(in, "include")
	limit, _ := optionalInt(in, "limit", defaultGrepLimit)

	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：正则表达式 %q 无法编译：%v", pattern, err)
	}

	if base == "" {
		base = "."
	}
	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   GrepToolName,
		path:   base,
		op:     filesystem.OpRead,
		detail: "在目录里搜索文件内容",
	}); err != nil {
		return Result{}, err
	}

	root, err := t.guard.Resolve(base)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：%w", err)
	}

	state := &grepState{regex: re, limit: limit, root: root}
	if err := t.search(ctx, root, include, state); err != nil {
		return Result{}, err
	}

	if len(state.hits) == 0 {
		return Result{
			Content: fmt.Sprintf("没有找到匹配 %q 的内容（检查了 %d 个文件）。可以放宽正则，或用 %s 先确认文件范围",
				pattern, state.files, GlobToolName),
			Display: &Display{Kind: DisplayList, Title: "无匹配"},
		}, nil
	}

	content := strings.Join(state.hits, "\n")
	if state.truncated {
		content += fmt.Sprintf("\n（只返回前 %d 条；检查了 %d 个文件。请缩小范围或提高 limit，上限 %d）",
			len(state.hits), state.files, maxGrepLimit)
	}

	return Result{
		Content: content,
		Display: &Display{
			Kind:  DisplayList,
			Title: fmt.Sprintf("匹配 %q：%d 条（检查 %d 个文件）", pattern, len(state.hits), state.files),
			Lines: state.hits,
		},
	}, nil
}

// grepState 汇总一次搜索的进度与结果。
type grepState struct {
	regex     *regexp.Regexp
	limit     int
	root      string
	hits      []string
	files     int
	truncated bool
	cancelled bool
}

// search 遍历起点（目录或单文件）。单文件走同一条读取路径，避免两套逻辑。
func (t *GrepTool) search(ctx context.Context, root, include string, state *grepState) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("执行失败：路径 %q 不存在或不可访问", root)
	}
	if !info.IsDir() {
		// 单文件起点：把 rel 的基准挪到父目录，否则结果里会显示 "." 而不是文件名。
		state.root = filepath.Dir(root)
		return t.searchFile(ctx, root, state)
	}

	includeRe, err := compileOptionalGlob(include)
	if err != nil {
		return err
	}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			state.cancelled = true
			return fs.SkipAll
		}
		if err != nil {
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
		if entry.IsDir() {
			return nil
		}
		if includeRe != nil {
			rel, err := filepath.Rel(root, path)
			if err != nil || !includeRe.MatchString(filepath.ToSlash(rel)) {
				return nil
			}
		}
		if err := t.searchFile(ctx, path, state); err != nil {
			return err
		}
		if state.cancelled || state.truncated {
			return fs.SkipAll
		}
		return nil
	})
	_ = walkErr

	if state.cancelled {
		return fmt.Errorf("超时：搜索被取消。请缩小路径范围后重试")
	}
	return nil
}

// searchFile 逐行匹配单个文件；二进制与超大文件静默跳过（它们是噪声，不是错误）。
func (t *GrepTool) searchFile(ctx context.Context, path string, state *grepState) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxGrepFileBytes {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil // 读不了就跳过：一个权限不足的文件不该让整次搜索失败
	}
	defer file.Close()

	head := make([]byte, binaryProbeBytes)
	n, _ := io.ReadFull(file, head)
	if isBinary(head[:n]) {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil
	}

	state.files++
	rel := path
	if relative, err := filepath.Rel(state.root, path); err == nil {
		rel = filepath.ToSlash(relative)
	}

	reader := bufio.NewReaderSize(file, 64<<10)
	for lineNo := 1; ; lineNo++ {
		if ctx.Err() != nil {
			state.cancelled = true
			return nil
		}

		line, got, readErr := readLimitedLine(reader)
		if got && state.regex.MatchString(line) {
			state.hits = append(state.hits, fmt.Sprintf("%s:%d: %s", rel, lineNo, line))
			if len(state.hits) >= state.limit {
				state.truncated = true
				return nil
			}
		}

		switch {
		case readErr == nil:
			// 继续读下一行
		case errors.Is(readErr, errLineTooLong):
			// 该行已按前缀匹配过；跳过剩下的部分，接着读下一行
		default:
			// io.EOF 与其它读取错误都在这里收尾：已经拿到的命中照常返回
			return nil
		}
	}
}

// errLineTooLong 表示单行超过 grepLineBytes，只按前缀匹配。
var errLineTooLong = errors.New("行超长")

// readLimitedLine 读一行，但限制长度。
//
// 压缩过的前端产物一行能有几 MB；不设限的话一次 grep 就足够把内存吃光，
// 而且那种行本来也读不出意义。超长时把已读前缀交回（仍可匹配）并丢弃剩余部分，
// 否则下一次读会接着读到同一行的尾巴，行号就全错了。
func readLimitedLine(reader *bufio.Reader) (line string, got bool, err error) {
	var builder strings.Builder
	got = false
	for {
		chunk, isPrefix, readErr := reader.ReadLine()
		if len(chunk) > 0 || !isPrefix {
			got = true
		}
		builder.Write(chunk)

		if builder.Len() >= grepLineBytes {
			for isPrefix && readErr == nil {
				_, isPrefix, readErr = reader.ReadLine()
			}
			return builder.String(), got, errLineTooLong
		}
		if readErr != nil {
			return builder.String(), got, readErr
		}
		if !isPrefix {
			return builder.String(), got, nil
		}
	}
}

// compileOptionalGlob 编译可选的 include 过滤；空串返回 nil（表示不过滤）。
func compileOptionalGlob(include string) (*regexp.Regexp, error) {
	if strings.TrimSpace(include) == "" {
		return nil, nil
	}
	return compileGlob(include)
}
