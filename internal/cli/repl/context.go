package repl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sy665/sycode/internal/llm"
)

// 上下文占用展示。
//
// 数字来自两处：装配时算好的固定部分（系统提示 + 工具定义），以及此刻的历史消息。
// 固定部分在一次运行里不变，所以不重复算——系统提示加六个工具 schema 有几千字，
// 每敲一次 /context 就重算一遍没有意义。
//
// 与常见实现的差别：它把 breakdown 放在引擎里（`GetContextBreakdown`），我们让界面自己
// 用 `Core.Messages()` 算。理由是那三个数只有界面要用，为它扩 `AgentCore` 接口不值得。

// contextUsage 是 /context 要显示的一组数字，单位都是估算 token。
type contextUsage struct {
	Base      int // 系统提示 + 工具定义
	User      int
	Assistant int
	Tool      int
	Total     int
	// Window 是模型上下文窗口；0 表示未知。
	Window int
	// Reported 是供应商上报的输入 token；0 表示这次还没有。
	Reported int
}

// effective 返回"用来算百分比"的数：有上报就用上报的，它包含了工具调用的 JSON 等估算看不见的部分。
func (u contextUsage) effective() int {
	if u.Reported > 0 {
		return u.Reported
	}
	return u.Total
}

// contextUsageFor 汇总当前的占用情况。
func (m Model) contextUsageFor() contextUsage {
	usage := contextUsage{
		Base:     m.deps.ContextBase,
		Window:   m.deps.ContextWindow,
		Reported: m.lastPromptTokens,
	}
	if m.deps.Core == nil {
		return usage
	}
	for _, msg := range m.deps.Core.Messages() {
		switch msg.Role {
		case llm.RoleUser:
			usage.User += llm.EstimateTokens(msg.Content)
		case llm.RoleAssistant:
			usage.Assistant += llm.EstimateTokens(msg.Content)
		case llm.RoleTool:
			usage.Tool += llm.EstimateTokens(msg.Content)
		}
	}
	usage.Total = usage.Base + usage.User + usage.Assistant + usage.Tool
	return usage
}

// renderContext 画出 /context 的内容。
func (m Model) renderContext(u contextUsage) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("上下文占用"))
	b.WriteString("\n")

	rows := []struct {
		label string
		value int
	}{
		{"系统提示与工具", u.Base},
		{"用户消息", u.User},
		{"助手回复", u.Assistant},
		{"工具结果", u.Tool},
	}
	for _, row := range rows {
		b.WriteString(dimStyle.Render("  "+padLabel(row.label)) + formatTokens(row.value))
		b.WriteString("\n")
	}

	b.WriteString(dimStyle.Render("  "+padLabel("合计")) + contextTotalLine(u))
	return b.String()
}

// contextTotalLine 拼合计那一行：数字、上限（如果知道）、百分比、来源标注。
func contextTotalLine(u contextUsage) string {
	tokens := u.effective()
	prefix := "≈ "
	source := "（估算）"
	if u.Reported > 0 {
		// 供应商给了数字就不用猜；但分类明细仍是估的，所以只在这一行去掉 ≈。
		prefix = ""
		source = "（供应商上报）"
	}

	line := prefix + formatTokens(tokens)
	if u.Window <= 0 {
		// 窗口未知时绝不显示 0%：那会让人以为还剩一大截。
		return line + " · 上限未知" + source
	}
	return line + " / " + formatTokens(u.Window) +
		fmt.Sprintf("（%.1f%%）", float64(tokens)*100/float64(u.Window)) + source
}

// formatTokens 把 token 数写成便于阅读的短形式。
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return trimZero(float64(n)/1000) + "k"
	default:
		return trimZero(float64(n)/1_000_000) + "M"
	}
}

// trimZero 保留一位小数并去掉多余的 `0` / `.`：12.0k 读起来比 12k 啰嗦。
func trimZero(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return s
}

// padLabel 按字符数补齐标签，让数字列对齐。
//
// 中文在终端里占两列，所以补齐到固定"字数"就够了；用 %-14s 按字节补会错位。
func padLabel(label string) string {
	const width = 7
	if runes := len([]rune(label)); runes < width {
		return label + strings.Repeat(" ", width-runes)
	}
	return label
}
