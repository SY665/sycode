package repl

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// 斜杠命令。
//
// 五条命令登记在同一张表里：`/help` 从这张表渲染，命令与说明因此不可能漂移。
// 没有像常见实现那样拆成 `commands` 子包——每条命令都要读写 Model 的状态
// （记录、流式标志、输入框），搬出去就得先把这些能力抽象成接口，
// 对五条命令来说成本远大于收益。等命令开始需要互相组合时再拆。

// command 是一条斜杠命令。
type command struct {
	name  string
	alias string
	help  string
	run   func(m Model) (tea.Model, tea.Cmd)
}

// commandList 是全部命令。顺序即 /help 的显示顺序。
//
// 用函数而不是包级变量：showHelp 要读这张表，而表里的 run 又指向 showHelp，
// 写成变量就成了初始化环。每次调用新建一个 5 元素切片，用户敲命令时才走这里，
// 这点分配不值得用 sync.Once 去省。
func commandList() []command {
	return []command{
		{name: "/clear", help: "清空上下文（会话记录仍然保留）", run: Model.clearContext},
		{name: "/context", help: "查看上下文占用", run: Model.showContext},
		{name: "/help", help: "列出所有命令", run: Model.showHelp},
		{name: "/sessions", help: "列出本目录的历史会话", run: Model.showSessions},
		{name: "/exit", alias: "/quit", help: "退出", run: Model.quitApp},
	}
}

// lookupCommand 按名字或别名找命令。
func lookupCommand(text string) (command, bool) {
	for _, cmd := range commandList() {
		if cmd.name == text || (cmd.alias != "" && cmd.alias == text) {
			return cmd, true
		}
	}
	return command{}, false
}

// quitApp 退出程序。
func (m Model) quitApp() (tea.Model, tea.Cmd) {
	m.quitting = true
	return m, tea.Quit
}

// clearContext 清空上下文。
//
// 只清内存与会话记录（屏幕、历史缓冲、上报的用量），**不碰会话文件**：
// 文件是只追加的账本，删内容只能靠新建会话。所以提示里要说清"记录仍然保留"，
// 否则用户会以为磁盘上也没了。
func (m Model) clearContext() (tea.Model, tea.Cmd) {
	if m.deps.Core != nil {
		m.deps.Core.Clear()
	}
	m.transcript = nil
	m.answer.Reset()
	m.lastPromptTokens = 0
	m.append(dimStyle.Render("已清空上下文（会话记录仍然保留，可用 /sessions 查看）"))
	return m, nil
}

// showContext 显示上下文占用。
func (m Model) showContext() (tea.Model, tea.Cmd) {
	m.append(m.renderContext(m.contextUsageFor()))
	return m, nil
}

// showHelp 列出全部命令。
func (m Model) showHelp() (tea.Model, tea.Cmd) {
	var b strings.Builder
	b.WriteString(titleStyle.Render("可用命令"))

	for _, cmd := range commandList() {
		names := cmd.name
		if cmd.alias != "" {
			names += " / " + cmd.alias
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  " + pad(names, commandColumn)))
		b.WriteString(cmd.help)
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Esc 中断当前回合（保留已生成内容）· Ctrl+C 连按两次退出"))
	m.append(b.String())
	return m, nil
}

// commandColumn 是 /help 里命令列的宽度。
//
// 取最长的那条（`/sessions` 与 `/exit / /quit`）再留两个空格：
// 宽度写小了会挤在一起，写大了又白占屏幕。
const commandColumn = 16

// showSessions 列出本目录的历史会话。
func (m Model) showSessions() (tea.Model, tea.Cmd) {
	if m.deps.Core == nil {
		m.append(dimStyle.Render("（没有可用的会话库）"))
		return m, nil
	}

	list, err := m.deps.Core.Sessions()
	if err != nil {
		m.append(dimStyle.Render("读取会话失败：" + err.Error()))
		return m, nil
	}
	if len(list) == 0 {
		m.append(dimStyle.Render("本目录还没有历史会话（这次对话会在退出时落盘）"))
		return m, nil
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("历史会话（%d）", len(list))))
	for _, info := range list {
		title := info.Title
		if title == "" {
			title = "（无标题）"
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  " + info.ID + "  "))
		b.WriteString(fmt.Sprintf("%d 轮  %s  ", info.Turns, info.Updated.Format("01-02 15:04")))
		b.WriteString(title)
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  只列出，不切换：换会话要先定「进行中的回合怎么办」"))
	m.append(b.String())
	return m, nil
}
