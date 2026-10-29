package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/sy665/sycode/internal/filesystem"
)

// ReadFileToolName 是工具名，模型按它发起调用。
const ReadFileToolName = "read_file"

const (
	// defaultReadLimit 是一次读取的默认行数。
	// 2000 行足够看完整文件的大半，又不会一次吃掉上下文窗口。
	defaultReadLimit = 2000
	// maxReadLimit 是 limit 的上限：超过这个数说明该用 grep 定位或分段读。
	maxReadLimit = 5000
	// maxReadBytes 是"整文件读入内存"的上限。超过就拒绝并建议先用 grep 定位：
	// 一次性把 100MB 日志读进内存，比报错更难排查。
	maxReadBytes = 16 << 20
	// binaryProbeBytes 是二进制探测的窗口大小。
	binaryProbeBytes = 8 << 10
)

// ReadFileTool 读取文本文件；分段、截断、二进制探测都在这里。
type ReadFileTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
}

// NewReadFileTool 注入依赖。perm 可为 nil（没有界面时按"不允许"处理）。
func NewReadFileTool(guard *filesystem.Guard, perm PermissionRequester) *ReadFileTool {
	return &ReadFileTool{guard: guard, perm: perm}
}

func (t *ReadFileTool) Name() string { return ReadFileToolName }

// Description 决定模型用不用它、怎么用：写清用途与边界，比写一堆同义词有用。
func (t *ReadFileTool) Description() string {
	return "读取工作目录内文本文件的内容。默认从头读 " + strconv.Itoa(defaultReadLimit) + " 行，" +
		"用 offset/limit 分段读大文件（offset 从 1 开始）。" +
		"二进制文件会被拒绝；被 .gitignore 忽略的路径不可读。只读，不修改文件。"
}

func (t *ReadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "文件路径，相对工作目录或绝对路径",
		},
		"offset": map[string]any{
			"type":        "integer",
			"description": "从第几行开始读（从 1 开始），默认 1",
		},
		"limit": map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("最多读多少行，默认 %d、上限 %d", defaultReadLimit, maxReadLimit),
		},
	}
}

// ValidateInput 只校验参数形态；权限与文件是否存在留给 Execute（那两条要给不同的错误）。
func (t *ReadFileTool) ValidateInput(_ context.Context, in Input) error {
	if _, err := requireString(in, "path"); err != nil {
		return err
	}
	offset, err := optionalInt(in, "offset", 1)
	if err != nil {
		return err
	}
	if offset < 1 {
		return fmt.Errorf("参数错误：字段 \"offset\" 从 1 开始，实际是 %d", offset)
	}
	limit, err := optionalInt(in, "limit", defaultReadLimit)
	if err != nil {
		return err
	}
	if limit < 1 {
		return fmt.Errorf("参数错误：字段 \"limit\" 必须为正数，实际是 %d", limit)
	}
	if limit > maxReadLimit {
		return fmt.Errorf("参数错误：字段 \"limit\" 最大 %d，实际是 %d。读更多请用 offset 分段", maxReadLimit, limit)
	}
	return nil
}

// Execute 先问权限再读。顺序不能反：先读再问等于已经把内容拿进内存了。
func (t *ReadFileTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	path, _ := requireString(in, "path")
	offset, _ := optionalInt(in, "offset", 1)
	limit, _ := optionalInt(in, "limit", defaultReadLimit)

	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   ReadFileToolName,
		path:   path,
		op:     filesystem.OpRead,
		detail: "读取文件内容",
	}); err != nil {
		return Result{}, err
	}

	// 读之前把路径解析成绝对路径：模型给的相对路径是相对工作目录的，
	// 而进程的 cwd 未必就是工作目录（测试里就不是）。
	resolved, err := t.guard.Resolve(path)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：%w", err)
	}

	content, total, read, truncated, err := readFileRange(resolved, offset, limit)
	if err != nil {
		return Result{}, err
	}

	body, clipped := clip(content)
	var notes []string
	if truncated {
		notes = append(notes, fmt.Sprintf("（已读到 %d-%d 行，共 %d 行；继续读请用 offset=%d）",
			offset, offset+read-1, total, offset+read))
	}
	if clipped {
		notes = append(notes, fmt.Sprintf("（内容超过 %d 字节已截断；请用 offset 分段读取或改用 grep 定位）", maxInlineBytes))
	}
	if len(notes) > 0 {
		body += "\n" + strings.Join(notes, "\n")
	}

	return Result{
		Content: body,
		Display: &Display{
			Kind:  DisplayList,
			Title: fmt.Sprintf("%s（第 %d-%d 行，共 %d 行）", path, offset, offset+read-1, total),
			Lines: numberedLines(content, offset),
		},
	}, nil
}

// readFileRange 读取指定行区间。
//
// 返回总行数是为了让上层能说"共 N 行"——模型据此决定要不要继续读。
// 超大文件直接拒绝而不是硬读：那种文件靠分段读也没意义，应该先 grep 定位。
func readFileRange(path string, offset, limit int) (content string, total, read int, truncated bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", 0, 0, false, fmt.Errorf("执行失败：文件 %q 不存在。请确认路径，或先列目录找一下", path)
		}
		return "", 0, 0, false, fmt.Errorf("执行失败：无法读取 %q：%w", path, err)
	}
	if info.IsDir() {
		return "", 0, 0, false, fmt.Errorf("执行失败：%q 是目录，不是文件。列目录请用 glob", path)
	}
	if info.Size() > maxReadBytes {
		return "", 0, 0, false, fmt.Errorf("执行失败：文件 %q 有 %s，超过 %s 上限。请先用 grep 定位要看的片段",
			path, humanBytes(info.Size()), humanBytes(maxReadBytes))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("执行失败：读取 %q 失败：%w", path, err)
	}
	if isBinary(data) {
		return "", 0, 0, false, fmt.Errorf("不支持：文件 %q 看起来是二进制（前 %s 内出现 NUL 字节），无法按文本读取",
			path, humanBytes(binaryProbeBytes))
	}

	lines := splitLines(string(data))
	total = len(lines)
	if offset > total && total > 0 {
		return "", total, 0, false, fmt.Errorf("参数错误：offset=%d 超出文件行数（共 %d 行）", offset, total)
	}
	if offset > total {
		// 空文件：offset=1 也是合法请求，返回空内容。
		return "", 0, 0, false, nil
	}

	start := offset - 1
	end := start + limit
	if end > total {
		end = total
	}
	read = end - start
	truncated = end < total
	return strings.Join(lines[start:end], "\n"), total, read, truncated, nil
}

// isBinary 用 NUL 字节判定：文本文件里不会出现 0x00，二进制里几乎必然有。
func isBinary(data []byte) bool {
	probe := data
	if len(probe) > binaryProbeBytes {
		probe = probe[:binaryProbeBytes]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// splitLines 按 \n 切行并去掉行尾 \r，保留空行：行号必须与编辑器一致。
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// numberedLines 给人看的带行号版本。模型那份不带行号：
// 它要拿原文去 edit_file 精确替换，带行号只会诱发把 " 12→" 也写进 old_string。
func numberedLines(content string, offset int) []string {
	lines := splitLines(content)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		out = append(out, fmt.Sprintf("%6d│ %s", offset+i, line))
	}
	return out
}

// humanBytes 把字节数换成便于阅读的形式（只用于提示，不追求精确）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}
