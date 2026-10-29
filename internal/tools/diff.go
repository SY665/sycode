package tools

import "strings"

// maxDiffLines 是 diff 展示的行数上限。
//
// 只是给人看的：一次改 200 行以上，屏幕上已经读不出重点，而正文里的 Content 仍会说清改了什么。
const maxDiffLines = 200

// EditDiffLine 是 diff 的一行。Kind 用统一的 '+', '-', ' ' 前缀。
type EditDiffLine struct {
	Kind byte
	Text string
}

// buildDiff 生成"旧文本 → 新文本"的逐行差异。
//
// 没有引 diff 库，也没有写 LCS：这里只需要"改动前后"这一种场景，
// 用"掐掉公共前后缀 + 中间整体标记"就足够——它不会把两处无关改动对齐得很漂亮，
// 但绝不会骗人（不会把改动说成没改）。真正需要细粒度对齐时再换，换的成本被 Display 挡住了。
func buildDiff(oldText, newText string) []EditDiffLine {
	if oldText == newText {
		return nil
	}

	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// 公共前缀：从上往下比到第一个不同。
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	// 公共后缀：从下往上比，注意别越过前缀。
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	diff := make([]EditDiffLine, 0, 8)
	// 保留一行上下文，让人知道改在哪儿；不在开头时才有意义。
	if prefix > 0 {
		diff = append(diff, EditDiffLine{Kind: ' ', Text: oldLines[prefix-1]})
	}
	for _, line := range oldLines[prefix : len(oldLines)-suffix] {
		diff = append(diff, EditDiffLine{Kind: '-', Text: line})
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		diff = append(diff, EditDiffLine{Kind: '+', Text: line})
	}
	if suffix > 0 {
		diff = append(diff, EditDiffLine{Kind: ' ', Text: oldLines[len(oldLines)-suffix]})
	}
	return diff
}

// formatDiff 把 diff 转成 UI 可直接渲染的行；超过 maxDiffLines 时截断并说明。
func formatDiff(diff []EditDiffLine) []string {
	if len(diff) == 0 {
		return []string{"（内容没有变化）"}
	}
	limit := len(diff)
	truncated := false
	if limit > maxDiffLines {
		limit = maxDiffLines
		truncated = true
	}

	lines := make([]string, 0, limit+1)
	for _, line := range diff[:limit] {
		lines = append(lines, string(line.Kind)+" "+line.Text)
	}
	if truncated {
		lines = append(lines, "…（差异过长，已截断显示；全文见文件本身）")
	}
	return lines
}

// diffSummary 给出"加了几行、删了几行"的一句话，用于标题。
func diffSummary(diff []EditDiffLine) string {
	added, removed := 0, 0
	for _, line := range diff {
		switch line.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	var b strings.Builder
	b.WriteString("+")
	b.WriteString(itoa(added))
	b.WriteString(" / -")
	b.WriteString(itoa(removed))
	return b.String()
}
