// Package repl 提供交互式终端界面（Bubble Tea）。
//
// 本包只负责"收集输入、渲染输出、转发用户动作"，不发起模型请求、不读文件——
// 那些属于 agentcore/appstate。依赖方向见 docs/CODING-STANDARDS.md §2.2。
//
// 模型调用不在这里发生：界面把输入交给 agentcore.AgentCore，再把事件流渲染出来。
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

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/llm"
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

// Deps 是界面的外部依赖。
type Deps struct {
	Version string
	Model   string

	// Core 是回合引擎；为空时界面只做回显（测试与降级用）。
	Core agentcore.AgentCore

	// Ctx 由 Run 注入，供 Submit 使用；为空时退回 Background。
	Ctx context.Context
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

	// 流式回合的状态：一句话在流里还没结束，就不能接受新输入。
	streamCh    <-chan llm.StreamEvent
	streaming   bool
	answer      strings.Builder
	retryNote   string
	interrupted bool
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
			// 上一句还没流完：不接受新输入，否则两股回答会缠在一起。
			if m.streaming {
				return m, nil
			}
			return m.submit()

		case "esc":
			m.quitArmed = false
			return m.interrupt()

		default:
			// 任何其它键都解除退出武装，符合"按错了继续输入"的直觉。
			m.quitArmed = false
		}

	case streamEventMsg:
		return m.handleStreamEvent(llm.StreamEvent(msg))

	case streamClosedMsg:
		// 没有终态事件就到了尽头：按"流异常结束"提示，但不当成错误弹红字。
		return m.finishTurn(errStreamClosed)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submit 处理一次回车：空输入忽略，/exit 退出，其余交给 Core 起一个回合。
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

	// 没有引擎时只回显：明确告知，而不是假装在思考。
	if m.deps.Core == nil {
		m.append(dimStyle.Render("（没有可用的模型：请检查配置与密钥）"))
		return m, nil
	}

	ch, err := m.deps.Core.Submit(m.context(), text)
	if err != nil {
		// 起不来就不进入流式状态，错误直接留在屏幕上。
		m.append(dimStyle.Render("错误：" + err.Error()))
		return m, nil
	}

	m.streamCh = ch
	m.streaming = true
	m.answer.Reset()
	m.retryNote = ""
	m.interrupted = false
	return m, waitEvent(ch)
}

// interrupt 处理 Esc：只在流式回合里生效，且保留已产出的内容（PRD F-LLM-6）。
//
// 这里不直接改 streaming：真正收尾要等流给出终态或关闭，
// 否则界面会显示"已中断"却还在收事件，状态两处不一致。
func (m Model) interrupt() (tea.Model, tea.Cmd) {
	if !m.streaming {
		return m, nil
	}
	if m.deps.Core != nil {
		m.deps.Core.Interrupt()
	}
	m.interrupted = true
	m.retryNote = ""
	return m, nil
}

// handleStreamEvent 渲染一个流事件，并继续等下一个。
func (m Model) handleStreamEvent(ev llm.StreamEvent) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case llm.EventText:
		m.answer.WriteString(ev.Text)
		m.retryNote = ""

	case llm.EventReasoning:
		// v0.1 不渲染思考过程（P4 做 thinking 档位时再折叠成摘要），
		// 但它要清掉重试提示：说明供应商已经在正常出字。
		m.retryNote = ""

	case llm.EventRetry:
		// 用户看不到后台在干什么，必须显式告知，否则会以为卡死。
		m.retryNote = fmt.Sprintf("第 %d 次失败，正在重试…", ev.Attempt)

	case llm.EventDone, llm.EventError:
		return m.finishTurn(ev.Err)

	default:
		// 工具事件、用量事件：P2 不展示（工具在 P3 才存在，用量留给状态栏）。
	}

	if m.streamCh == nil {
		return m, nil
	}
	return m, waitEvent(m.streamCh)
}

// finishTurn 收尾一个回合：把正文落到历史里，再把错误或中断提示补在后面。
func (m Model) finishTurn(err error) (tea.Model, tea.Cmd) {
	m.streaming = false
	m.streamCh = nil
	m.retryNote = ""

	if text := m.answer.String(); text != "" {
		m.append(text)
	}
	m.answer.Reset()

	if m.interrupted {
		m.interrupted = false
		m.append(dimStyle.Render("（已中断，保留已生成内容）"))
	}
	if err != nil {
		m.append(dimStyle.Render("错误：" + err.Error()))
	}
	return m, nil
}

// context 取注入的 ctx；没注入时用 Background，保证测试不必构造 context。
func (m Model) context() context.Context {
	if m.deps.Ctx != nil {
		return m.deps.Ctx
	}
	return context.Background()
}

// waitEvent 把"从 channel 读一个事件"变成一条 tea.Cmd：UI 与回合引擎由此解耦，
// 事件到来时 Bubble Tea 会自动调 Update，不需要界面自己轮询。
func waitEvent(ch <-chan llm.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg(ev)
	}
}

// streamEventMsg 包一层是必需的：tea.Msg 是 any，直接放 llm.StreamEvent 会和
// 其它库的消息类型撞车，也不好看清"这是界面自己的消息"。
type streamEventMsg llm.StreamEvent

// streamClosedMsg 表示"channel 关了但没给终态事件"（实现有问题时才会出现）。
type streamClosedMsg struct{}

// errStreamClosed 是上面那种情况的提示文案。
var errStreamClosed = errors.New("事件流意外结束（没有收到完成或错误事件）")

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
	// 正在生成的正文与历史一起显示：还没进 transcript，但它已经是"用户看到的内容"。
	if text := m.answer.String(); text != "" {
		b.WriteString(text)
		b.WriteString("\n")
	}
	if m.retryNote != "" {
		b.WriteString(dimStyle.Render(m.retryNote))
		b.WriteString("\n")
	}
	if len(m.transcript) > 0 || m.answer.Len() > 0 {
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

	// ctx 交给模型：Submit 要用它，且程序退出时能一并取消在飞的请求。
	d.Ctx = ctx
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
