package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sy665/sycode/internal/filesystem"
)

// EditFileToolName 是工具名。
const EditFileToolName = "edit_file"

// maxMatchReport 是最多回报几处候选行号：再多也没人看，位置信息足够定位即可。
const maxMatchReport = 5

// EditFileTool 精确替换文件里的片段。
//
// 只做"唯一匹配"的替换：模型拿着不唯一的 old_string 去改，改错地方的代价远大于让它重写一次。
type EditFileTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
}

// NewEditFileTool 注入依赖。
func NewEditFileTool(guard *filesystem.Guard, perm PermissionRequester) *EditFileTool {
	return &EditFileTool{guard: guard, perm: perm}
}

func (t *EditFileTool) Name() string { return EditFileToolName }

func (t *EditFileTool) Description() string {
	return "把文件里的一段内容替换成另一段：old_string 必须在文件中恰好出现一次。" +
		"改多处请多次调用；想一次写整个文件请用 write_file。" +
		"old_string 要带上足够的上下文（含缩进）以保证唯一；new_string 为空串表示删除该段。" +
		"写入是原子的。"
}

func (t *EditFileTool) InputSchema() map[string]any {
	return map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "要修改的文件路径",
		},
		"old_string": map[string]any{
			"type":        "string",
			"description": "要被替换掉的原文（含缩进，必须在文件中唯一出现）",
		},
		"new_string": map[string]any{
			"type":        "string",
			"description": "替换后的内容；空串表示删除 old_string",
		},
	}
}

func (t *EditFileTool) ValidateInput(_ context.Context, in Input) error {
	if _, err := requireString(in, "path"); err != nil {
		return err
	}
	oldText, err := requireText(in, "old_string")
	if err != nil {
		return err
	}
	// 空串在任何位置都"匹配"，替换只会随机毁掉文件。
	if oldText == "" {
		return fmt.Errorf("参数错误：字段 %q 不能为空。要整文件重写请用 %s", "old_string", WriteFileToolName)
	}
	_, err = requireText(in, "new_string")
	return err
}

// Execute 读出原文 → 确认唯一 → 替换 → 原子写回。
func (t *EditFileTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	path, _ := requireString(in, "path")
	oldText, _ := requireString(in, "old_string")
	newText, _ := requireText(in, "new_string")

	// 先问写权限：被拒绝时一个字节都不该动，包括别去读它。
	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   EditFileToolName,
		path:   path,
		op:     filesystem.OpWrite,
		detail: "修改文件内容（" + summarizeChange(oldText, newText) + "）",
	}); err != nil {
		return Result{}, err
	}

	resolved, err := t.guard.Resolve(path)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：%w", err)
	}

	current, existed, err := readExisting(resolved)
	if err != nil {
		return Result{}, err
	}
	if !existed {
		return Result{}, fmt.Errorf("执行失败：文件 %q 不存在。要新建请用 %s", path, WriteFileToolName)
	}

	matches := locateMatches(current, oldText)
	switch {
	case len(matches) == 0:
		return Result{}, fmt.Errorf("执行失败：在 %q 里没有找到 old_string 的内容。"+
			"可能是缩进或空白不一致，或这段代码已经被改过；建议先用 %s 确认原文", path, GrepToolName)
	case len(matches) > 1:
		return Result{}, fmt.Errorf("参数错误：old_string 在 %q 里出现了 %d 次（第 %s 行）。"+
			"请把上下文一并带上让它唯一，或分多次修改",
			path, len(matches), joinLines(matches))
	}

	updated := applyReplace(current, oldText, newText)
	if updated == current {
		return Result{
			Content: fmt.Sprintf("文件 %q 的内容没有变化，未写入。", path),
			Display: &Display{Kind: DisplayDiff, Title: path + "（无变化）", Lines: []string{"（内容没有变化）"}},
		}, nil
	}

	if err := writeAndVerify(resolved, updated, path); err != nil {
		return Result{}, err
	}

	diff := buildDiff(current, updated)
	return Result{
		Content: fmt.Sprintf("已修改 %q（%s；第 %d 行附近）。", path, diffSummary(diff), matches[0]),
		Display: &Display{
			Kind:  DisplayDiff,
			Title: fmt.Sprintf("%s（%s）", path, diffSummary(diff)),
			Lines: formatDiff(diff),
		},
	}, nil
}

// locateMatches 返回 old 在 content 中每次出现的起始行号（从 1 开始）。
//
// 只需要"有几处、在第几行"：给模型的行号是为了让它补上下文，不是为了让程序去改动。
func locateMatches(content, old string) []int {
	if old == "" {
		return nil
	}

	lines := make([]int, 0, 2)
	offset := 0
	for len(lines) < maxMatchReport {
		index := strings.Index(content[offset:], old)
		if index < 0 {
			break
		}
		position := offset + index
		lines = append(lines, 1+strings.Count(content[:position], "\n"))
		offset = position + len(old)
	}

	// 超过上报上限时再多探一次，让调用方知道"不止这些"。
	if len(lines) == maxMatchReport {
		if strings.Contains(content[offset:], old) {
			lines = append(lines, -1)
		}
	}
	return lines
}

// applyReplace 替换第一处（调用方已确认唯一）。
func applyReplace(content, old, new string) string {
	return strings.Replace(content, old, new, 1)
}

// summarizeChange 给权限弹窗写一句人话。
func summarizeChange(oldText, newText string) string {
	return fmt.Sprintf("%s 行 → %s 行", itoa(len(splitLines(oldText))), itoa(len(splitLines(newText))))
}

// joinLines 把行号拼成"3、8、12"；遇到 -1（表示还有更多）就换成省略号。
func joinLines(lines []int) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		if line < 0 {
			parts = append(parts, "…")
			continue
		}
		parts = append(parts, itoa(line))
	}
	return strings.Join(parts, "、")
}
