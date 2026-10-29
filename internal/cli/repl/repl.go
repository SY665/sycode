// Package repl 提供交互式终端界面（Bubble Tea）。
//
// 本包只负责"收集输入、渲染输出、转发用户动作"，不发起模型请求、不读文件——
// 那些属于 agentcore/appstate。依赖方向见 docs/CODING-STANDARDS.md §2.2。
//
// v0.1 阶段（P1.4）只实现最小闭环：输入、提交、退出。模型调用在 P2 接。
package repl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// inputHeight 是输入框固定行数：REPL 的输入以短句为主，给 3 行足够容纳换行粘贴。
	inputHeight = 3

	// maxTranscript 是保留的历史条数。渲染只取窗口高度内的最后若干条，
	// 但保留上限避免长时间会话把内存堆起来（真正的持久化在 P4 的 session）。
	maxTranscript = 200

	// quitHint 是双击 Ctrl+C 的提示文案；第一次按下后出现，按其它键消失。
	quitHint = "再按一次 Ctrl+C 退出"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
)

// Deps 是界面的外部依赖。P1 只有展示用的版本号与模型名，P2 会加入 AgentCore。
type Deps struct {
	Version string
	Model   string
}

// Model 是 Bubble Tea 的状态。所有字段只允许在 Update 里修改（单写者原则）。
type Model struct {
	deps       Deps
	input      textarea.Model
	transcript []string
	width      int
	height     int
	quitArmed  bool
	quitting   bool
}

// NewModel 构造初始状态，不做任何 IO。
func NewModel(d Deps) Model {
	ta := textarea.New()
	ta.Prompt = "› "
	ta.Placeholder = "说说你想让 SyCode 做什么…（/exit 退出）"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	ta.SetHeight(inputHeight)

	// Enter 用来提交，因此必须解绑 textarea 自带的"插入换行"，否则永远提交不了。
	ta.KeyMap.InsertNewline.SetEnabled(false)

	return Model{deps: d, input: ta}
}

// Init 让输入框获得焦点；Bubble Tea 会在启动后发送窗口尺寸事件。
func (m Model) Init() tea.Cmd {
	return m.input.Focus()
}

// Update 是唯一修改状态的地方。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(msg.Width-2, 20))
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.quitArmed {
				m.quitting = true
				return m, tea.Quit
			}
			// 第一次按下只"武装"退出，并给一行提示；避免误触丢上下文。
			m.quitArmed = true
			return m, nil

		case "enter":
			m.quitArmed = false
			return m.submit()

		default:
			// 任何其它键都解除退出武装，符合"按错了继续输入"的直觉。
			m.quitArmed = false
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submit 处理一次回车：空输入忽略，/exit 退出，其余作为一次占位回合。
func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	if text == "/exit" || text == "/quit" {
		m.quitting = true
		return m, tea.Quit
	}

	m.append("› " + text)
	// P2 之前没有模型可调，这里明确告知，而不是假装在思考。
	m.append(dimStyle.Render("（模型尚未接入，P2 生效）"))
	return m, nil
}

// append 追加一行历史，并裁剪到保留上限。
func (m *Model) append(line string) {
	m.transcript = append(m.transcript, line)
	if len(m.transcript) > maxTranscript {
		m.transcript = m.transcript[len(m.transcript)-maxTranscript:]
	}
}

// View 只读状态，不做修改。
//
// Bubble Tea v2 把视图从 string 改成 tea.View（可携带鼠标处理等元数据），
// 因此这里返回 tea.NewView(内容)，而不是裸字符串。
func (m Model) View() tea.View {
	return tea.NewView(m.render())
}

// render 拼出屏幕内容，与框架类型解耦，便于单测直接断言文本。
func (m Model) render() string {
	var b strings.Builder

	title := titleStyle.Render("SyCode") + dimStyle.Render(" "+m.deps.Version)
	if m.deps.Model != "" {
		title += dimStyle.Render(" · " + m.deps.Model)
	}
	b.WriteString(title)
	b.WriteString("\n\n")

	for _, line := range m.visibleTranscript() {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(m.transcript) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(m.input.View())
	if m.quitArmed {
		b.WriteString("\n" + dimStyle.Render(quitHint))
	}
	return b.String()
}

// visibleTranscript 只取窗口能放下的最后若干行，避免整段历史参与排版。
func (m Model) visibleTranscript() []string {
	limit := m.height - inputHeight - 6
	if limit <= 0 || limit > len(m.transcript) {
		limit = len(m.transcript)
	}
	return m.transcript[len(m.transcript)-limit:]
}

// Run 启动界面并阻塞到用户退出。
//
// stdin 不是终端时直接报错：没有 TTY 的环境里启动 Bubble Tea 只会得到一个坏掉的屏幕，
// 明确提示用户改用 headless 入口（sycode run）更有用。
func Run(ctx context.Context, d Deps) error {
	if !isInteractive(os.Stdin) {
		return errors.New("交互式界面需要终端（stdin 不是 TTY）。请改用 sycode run \"提示词\"")
	}

	p := tea.NewProgram(NewModel(d), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("界面异常退出：%w", err)
	}
	return nil
}

// isInteractive 判断给定文件是否为终端。
//
// 只判字符设备是不够的：/dev/null 与 Windows 的 NUL 同样是字符设备，
// 于是 `sycode < /dev/null` 会被误判成终端，把屏幕交给 Bubble Tea 后挂死。
// 因此额外排除空设备——用 os.SameFile 比较真实 inode/文件索引，跨平台可用。
func isInteractive(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}
