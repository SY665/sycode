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
	// 用 ANSI 基本色而不是十六进制：浅色与深色终端都能看清，也不必猜背景。
	addedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Green)
	removedStyle = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// Deps 是界面的外部依赖。
type Deps struct {
	Version string
	Model   string

	// Core 是回合引擎；为空时界面只做回显（测试与降级用）。
	Core agentcore.AgentCore

	// Permission 是权限询问通路；为空时不弹窗（工具侧会因"没有确认通道"而拒绝）。
	Permission *agentcore.PermissionPrompt

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

	// pending 是正在等用户作答的权限询问；非空即"弹窗模态"，输入暂停。
	pending *agentcore.PermissionRequest
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
//
// 权限等待只在这里挂一次，之后每次作答再由 answerPermission 续上：
// 一条链上始终只有一个等待者。
func (m Model) Init() tea.Cmd {
	if m.deps.Permission == nil {
		return m.input.Focus()
	}
	return tea.Batch(m.input.Focus(), waitPermission(m.deps.Permission))
}

// Update 是唯一修改状态的地方。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(msg.Width-2, 20))
		return m, nil

	case tea.KeyPressMsg:
		// 弹窗是模态的：所有按键先给它，避免用户以为在输入框里打字、实际把回答漏掉。
		if m.pending != nil {
			return m.answerPermission(msg)
		}
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

	case permissionRequestMsg:
		return m.showPermission(agentcore.PermissionRequest(msg))
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

	case llm.EventToolResult:
		m.appendToolResult(ev)

	default:
		// tool_start 与 usage：调用快照与统计留给状态栏。
		// tool_start 尤其不能渲染——它与 tool_result 说的是同一次调用，一起显示只是重复。
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

// permissionRequestMsg 是"某个工具在等许可"进 Update 的通道。
type permissionRequestMsg agentcore.PermissionRequest

// waitPermission 把"从权限通路读一个请求"变成一条 tea.Cmd。
//
// 与 waitEvent 同一套路：把阻塞读放进框架的 goroutine 里，界面不必轮询。
func waitPermission(p *agentcore.PermissionPrompt) tea.Cmd {
	return func() tea.Msg {
		return permissionRequestMsg(<-p.Requests())
	}
}

// showPermission 打开弹窗。
func (m Model) showPermission(req agentcore.PermissionRequest) (tea.Model, tea.Cmd) {
	// 先把已生成的正文落进历史：权限记录必须排在它后面。
	// 否则模型说了"我来改这个文件"、弹窗记录却在它上面，读起来时间顺序是反的。
	if text := m.answer.String(); text != "" {
		m.append(text)
		m.answer.Reset()
	}
	m.pending = &req
	return m, nil
}

// answerPermission 处理弹窗里的按键。只有这里能把答案送回去。
//
// 用数字/字母直答，不做"移动选中项 + 回车"：弹窗是阻塞的，多一层选中态
// 就多一类状态不一致（答案与高亮不同步），而这三个选项本来一看就能选。
func (m Model) answerPermission(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "y":
		return m.respond(agentcore.DecisionAllowOnce)
	case "2", "a":
		return m.respond(agentcore.DecisionAllowSession)
	case "3", "n", "esc":
		return m.respond(agentcore.DecisionDeny)
	case "ctrl+c":
		// 弹窗里也要留一条不靠作答就能出去的路：先拒掉，否则工具侧会一直挂着。
		m.deps.Permission.Answer(agentcore.DecisionDeny)
		m.pending = nil
		m.quitting = true
		return m, tea.Quit
	default:
		// 其余键不响应，也不透给输入框：弹窗期间打字没有意义。
		return m, nil
	}
}

// respond 把决定送回去，记一行留痕，并重新开始等待下一次询问。
func (m Model) respond(decision agentcore.Decision) (tea.Model, tea.Cmd) {
	if m.pending != nil {
		m.append(dimStyle.Render(permissionNote(*m.pending, decision)))
	}
	m.deps.Permission.Answer(decision)
	m.pending = nil
	return m, waitPermission(m.deps.Permission)
}

// permissionNote 是作答后留在记录里的一行。
//
// 用户回头翻记录时要能看出"这次是被放行的还是被拒的"，否则只看到模型失败会以为出了故障。
func permissionNote(req agentcore.PermissionRequest, decision agentcore.Decision) string {
	action := "已拒绝"
	switch decision {
	case agentcore.DecisionAllowOnce:
		action = "已允许一次"
	case agentcore.DecisionAllowSession:
		action = "本会话不再询问"
	}
	return "· " + action + "：" + req.Tool + " " + opText(req.Op) + " " + req.Path
}

// opText 把操作名换成中文动词。取值同 filesystem.Op。
func opText(op string) string {
	switch op {
	case "read":
		return "读取"
	case "write":
		return "修改"
	case "exec":
		return "执行"
	default:
		return op
	}
}

// streamClosedMsg 表示"channel 关了但没给终态事件"（实现有问题时才会出现）。
type streamClosedMsg struct{}

// errStreamClosed 是上面那种情况的提示文案。
var errStreamClosed = errors.New("事件流意外结束（没有收到完成或错误事件）")

// appendToolResult 把一次工具执行的结果落到记录里：首行摘要，其后是展示块。
//
// 先 flush 正文：模型说"我改一下这个文件"，接着才是工具结果行；
// 顺序反了读起来像工具先跑、模型后解释。
func (m *Model) appendToolResult(ev llm.StreamEvent) {
	if body := m.answer.String(); body != "" {
		m.append(body)
		m.answer.Reset()
	}

	lines := strings.Split(ev.Text, "\n")
	summary := "⎿ " + lines[0]
	if ev.Err != nil {
		// 失败要显眼：同一个位置的暗色行，用户会当成又一条进展而读漏。
		m.append(removedStyle.Render(summary))
	} else {
		m.append(dimStyle.Render(summary))
	}
	for _, line := range lines[1:] {
		m.append(displayLine(line))
	}
}

// displayLine 按行首字符给展示行上色。
//
// 不按 Kind 分支：展示块在 appstate 已拼成同一种文本，界面只认
// "+ "（新增）、"- "（删除）、其它（上下文与列表项）三种形态。
func displayLine(line string) string {
	switch {
	case strings.HasPrefix(line, "+ "):
		return addedStyle.Render("  " + line)
	case strings.HasPrefix(line, "- "):
		return removedStyle.Render("  " + line)
	default:
		return dimStyle.Render("  " + line)
	}
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

	if m.pending != nil {
		b.WriteString(m.renderPermission())
	} else {
		b.WriteString(m.input.View())
	}
	if m.quitArmed {
		b.WriteString("\n" + dimStyle.Render(quitHint))
	}
	return b.String()
}

// renderPermission 画权限弹窗，占用输入框的位置：作答期间不接受输入。
func (m Model) renderPermission() string {
	req := *m.pending

	var b strings.Builder
	b.WriteString(titleStyle.Render("需要你确认"))
	b.WriteString("\n")
	b.WriteString(req.Tool + " 想要" + opText(req.Op) + " " + req.Path)
	if req.Detail != "" {
		b.WriteString("\n" + dimStyle.Render(req.Detail))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("[1] 允许一次   [2] 本会话总是允许   [3] 拒绝"))
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
