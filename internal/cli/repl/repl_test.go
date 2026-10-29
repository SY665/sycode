package repl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/llm"
)

// newTestModel 造一个可直接驱动 Update 的模型，不启动真实终端。
func newTestModel() Model {
	return newTestModelWithCore(nil)
}

// newTestModelWithCore 同上，但注入假的回合引擎。
func newTestModelWithCore(core agentcore.AgentCore) Model {
	m := NewModel(Deps{Version: "0.1.0", Model: "test-model", Core: core})
	m.width, m.height = 80, 24
	m.input.SetWidth(78)
	return m
}

// fakeCore 按脚本产出事件；Interrupt 只记次数，不真的取消（取消逻辑在 agentcore 测）。
type fakeCore struct {
	events    []llm.StreamEvent
	submitErr error
	interrupt int
	inputs    []string
}

func (f *fakeCore) Submit(_ context.Context, input string) (<-chan llm.StreamEvent, error) {
	f.inputs = append(f.inputs, input)
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	ch := make(chan llm.StreamEvent, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (f *fakeCore) Interrupt()              { f.interrupt++ }
func (f *fakeCore) Messages() []llm.Message { return nil }
func (f *fakeCore) Clear()                  {}

// runCmd 执行一条 tea.Cmd 并把结果喂回 Update，模拟 Bubble Tea 的调度。
func runCmd(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("期望有 cmd，实际为 nil")
	}
	updated, next := m.Update(cmd())
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("Update 返回的不是 Model")
	}
	return got, next
}

// press 构造按键消息。用 tea.KeyPressMsg 的字面量而不是模拟输入流，
// 这样测试直接覆盖状态迁移，与终端实现解耦。
func press(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	updated, cmd := m.Update(msg)
	got, ok := updated.(Model)
	if !ok {
		panic("Update 返回的不是 Model")
	}
	return got, cmd
}

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEsc}
	keyCtrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	keyRune  = tea.KeyPressMsg{Code: 'x', Text: "x"}
)

func TestSubmitWithoutCoreExplainsItself(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("列出当前目录")

	m, cmd := press(m, keyEnter)

	if cmd != nil {
		t.Errorf("提交不应产生 cmd，实际 %T", cmd)
	}
	if len(m.transcript) != 2 {
		t.Fatalf("应追加 2 行（用户输入 + 占位回复），实际 %d 行：%q", len(m.transcript), m.transcript)
	}
	if got := m.transcript[0]; got != "› 列出当前目录" {
		t.Errorf("第一行不符：期望 %q，实际 %q", "› 列出当前目录", got)
	}
	if !strings.Contains(m.transcript[1], "没有可用的模型") {
		t.Errorf("没有引擎时应说明原因，实际 %q", m.transcript[1])
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("提交后输入框应清空，实际 %q", got)
	}
}

func TestEmptySubmitIgnored(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("   ")

	m, _ = press(m, keyEnter)

	if len(m.transcript) != 0 {
		t.Errorf("空白输入不应产生历史，实际 %q", m.transcript)
	}
}

func TestExitCommandQuits(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("/exit")

	m, cmd := press(m, keyEnter)

	if !m.quitting {
		t.Error("/exit 应设置 quitting")
	}
	if cmd == nil {
		t.Error("/exit 应返回退出命令")
	}
	if len(m.transcript) != 0 {
		t.Errorf("/exit 不应写入历史，实际 %q", m.transcript)
	}
}

func TestCtrlCRequiresTwoPresses(t *testing.T) {
	m := newTestModel()

	m, cmd := press(m, keyCtrlC)
	if m.quitting {
		t.Fatal("第一次 Ctrl+C 不应退出")
	}
	if !m.quitArmed {
		t.Error("第一次 Ctrl+C 应进入待确认状态")
	}
	if cmd != nil {
		t.Errorf("第一次 Ctrl+C 不应返回 cmd，实际 %T", cmd)
	}

	m, cmd = press(m, keyCtrlC)
	if !m.quitting {
		t.Error("第二次 Ctrl+C 应退出")
	}
	if cmd == nil {
		t.Error("第二次 Ctrl+C 应返回退出命令")
	}
}

func TestOtherKeyDisarmsQuit(t *testing.T) {
	m := newTestModel()

	m, _ = press(m, keyCtrlC)
	if !m.quitArmed {
		t.Fatal("前置条件失败：应先处于待确认状态")
	}

	m, _ = press(m, keyRune)
	if m.quitArmed {
		t.Error("按其它键应解除退出待确认")
	}
}

func TestRenderShowsTitleAndQuitHint(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("hi")
	m, _ = press(m, keyEnter)

	got := m.render()
	for _, want := range []string{"SyCode", "0.1.0", "test-model", "› hi"} {
		if !strings.Contains(got, want) {
			t.Errorf("视图缺少 %q：\n%s", want, got)
		}
	}

	m, _ = press(m, keyCtrlC)
	if !strings.Contains(m.render(), quitHint) {
		t.Errorf("待确认状态应显示退出提示：\n%s", m.render())
	}
}

func TestVisibleTranscriptRespectsWindowHeight(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 50; i++ {
		m.append("行")
	}
	m.height = 12

	visible := m.visibleTranscript()
	limit := 12 - inputHeight - 6
	if len(visible) > limit {
		t.Errorf("可见行数应受窗口高度限制：上限 %d，实际 %d", limit, len(visible))
	}
}

func TestIsInteractiveFalseForRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-tty")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备测试文件失败：%v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开测试文件失败：%v", err)
	}
	defer f.Close()

	if isInteractive(f) {
		t.Error("普通文件不应被判定为终端")
	}
}

func TestIsInteractiveFalseForNullDevice(t *testing.T) {
	// 回归：曾只判字符设备，导致 `sycode < /dev/null` 被当成终端而挂死。
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("打开空设备失败：%v", err)
	}
	defer f.Close()

	if isInteractive(f) {
		t.Error("空设备不应被判定为终端")
	}
}

func TestStreamingTurnRendersTextAndFinishes(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "你"},
		{Type: llm.EventText, Text: "好"},
		{Type: llm.EventDone},
	}}
	m := newTestModelWithCore(core)
	m.input.InsertString("打个招呼")

	// 回车后应进入流式状态，并返回"等下一个事件"的 cmd。
	m, cmd := press(m, keyEnter)
	if !m.streaming {
		t.Fatal("提交成功应进入流式状态")
	}
	if cmd == nil {
		t.Fatal("提交成功应返回等待事件的 cmd")
	}

	// 逐个事件喂回去，模拟 Bubble Tea 的调度。
	for i := 0; i < 3; i++ {
		if m.streaming && m.answer.Len() > 0 {
			// 还没结束的正文也要能显示出来，否则用户会觉得没反应。
			if !strings.Contains(m.render(), m.answer.String()) {
				t.Errorf("流式中的正文应参与渲染：\n%s", m.render())
			}
		}
		m, cmd = runCmd(t, m, cmd)
	}

	if m.streaming {
		t.Error("收到终态事件后应退出流式状态")
	}
	last := m.transcript[len(m.transcript)-1]
	if last != "你好" {
		t.Errorf("分片应拼成一条完整回答，实际 %q", last)
	}
}

func TestStreamingTurnShowsRetryNote(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventRetry, Attempt: 2},
		{Type: llm.EventText, Text: "好了"},
		{Type: llm.EventDone},
	}}
	m := newTestModelWithCore(core)
	m.input.InsertString("hi")

	m, cmd := press(m, keyEnter)
	m, cmd = runCmd(t, m, cmd) // 重试事件

	if !strings.Contains(m.render(), "第 2 次失败，正在重试") {
		t.Errorf("重试时必须告诉用户，否则界面像卡死：\n%s", m.render())
	}

	m, cmd = runCmd(t, m, cmd) // 正文事件到达，提示应消失
	if strings.Contains(m.render(), "正在重试") {
		t.Errorf("开始出字后应清掉重试提示：\n%s", m.render())
	}
	m, _ = runCmd(t, m, cmd)
}

func TestEscInterruptsAndKeepsPartialText(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "这段要保留"},
		{Type: llm.EventError, Err: errors.New("已中断：context canceled")},
	}}
	m := newTestModelWithCore(core)
	m.input.InsertString("写长文")

	m, cmd := press(m, keyEnter)
	m, cmd = runCmd(t, m, cmd) // 第一个正文增量

	m, _ = press(m, keyEsc)
	if core.interrupt != 1 {
		t.Errorf("Esc 应调用 Interrupt 一次，实际 %d", core.interrupt)
	}
	if !m.streaming {
		t.Error("Esc 后仍应收完事件再收尾，此时不该退出流式状态")
	}

	m, _ = runCmd(t, m, cmd) // 错误事件（收尾）
	if m.streaming {
		t.Error("终态事件后应退出流式状态")
	}
	// PRD F-LLM-6：中断不丢已产出内容，并明确告知已中断。
	if !strings.Contains(m.render(), "这段要保留") {
		t.Errorf("中断后应保留已生成内容：\n%s", m.render())
	}
	if !strings.Contains(m.render(), "已中断") {
		t.Errorf("应提示已中断：\n%s", m.render())
	}
}

func TestEscWithoutStreamIsIgnored(t *testing.T) {
	core := &fakeCore{}
	m := newTestModelWithCore(core)

	m, cmd := press(m, keyEsc)

	if cmd != nil {
		t.Error("空闲时 Esc 不应产生 cmd")
	}
	if core.interrupt != 0 {
		t.Error("空闲时 Esc 不应调用 Interrupt")
	}
}

func TestSubmitDuringStreamIsIgnored(t *testing.T) {
	// 上一句还在流：新输入会让两股回答缠在一起，直接丢弃。
	core := &fakeCore{events: []llm.StreamEvent{{Type: llm.EventText, Text: "答"}, {Type: llm.EventDone}}}
	m := newTestModelWithCore(core)
	m.input.InsertString("第一句")
	m, cmd := press(m, keyEnter)

	m.input.InsertString("第二句")
	m, _ = press(m, keyEnter)

	if len(core.inputs) != 1 {
		t.Errorf("流式期间不该发起新回合，实际 %d 次", len(core.inputs))
	}
	if !m.streaming {
		t.Error("仍应处于流式状态")
	}
	runCmd(t, m, cmd)
}

func TestSubmitErrorIsShownInline(t *testing.T) {
	core := &fakeCore{submitErr: errors.New("模型名为空：请检查配置中的 active_model")}
	m := newTestModelWithCore(core)
	m.input.InsertString("hi")

	m, cmd := press(m, keyEnter)

	if m.streaming {
		t.Error("起不来时不该进入流式状态")
	}
	if cmd != nil {
		t.Error("起不来时不该返回等待 cmd")
	}
	if !strings.Contains(m.render(), "模型名为空") {
		t.Errorf("错误应显示在界面上：\n%s", m.render())
	}
}

func TestErrorEventIsShown(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventError, Err: errors.New("执行失败：模型服务返回 401（invalid_api_key）。")},
	}}
	m := newTestModelWithCore(core)
	m.input.InsertString("hi")

	m, cmd := press(m, keyEnter)
	m, _ = runCmd(t, m, cmd)

	if m.streaming {
		t.Error("错误事件后应退出流式状态")
	}
	if !strings.Contains(m.render(), "invalid_api_key") {
		t.Errorf("错误应显示出来：\n%s", m.render())
	}
}

func TestStreamClosedWithoutTerminalIsReported(t *testing.T) {
	// 事件流异常结束（没有 done/error）：不能静默吞掉，否则用户以为答案就这么多。
	m := newTestModelWithCore(&fakeCore{})
	m.input.InsertString("hi")
	m.streaming = true
	m.streamCh = make(chan llm.StreamEvent)

	updated, _ := m.Update(streamClosedMsg{})
	got := updated.(Model)

	if got.streaming {
		t.Error("channel 关闭后应退出流式状态")
	}
	if !strings.Contains(got.render(), "意外结束") {
		t.Errorf("应提示事件流异常结束：\n%s", got.render())
	}
}

// newTestModelWithPermission 造一个带权限通路的模型：弹窗必须有地方作答，否则工具会一直挂着。
func newTestModelWithPermission(p *agentcore.PermissionPrompt) Model {
	m := newTestModel()
	m.deps.Permission = p
	return m
}

// askPermission 模拟工具侧发出一次询问，并等界面侧真的收到它。
// 两边都在 goroutine 里：RequestPermission 会阻塞到有人作答，不能占着测试主线程。
func askPermission(t *testing.T, p *agentcore.PermissionPrompt, req agentcore.PermissionRequest) <-chan agentcore.Decision {
	t.Helper()
	decided := make(chan agentcore.Decision, 1)
	go func() {
		d, _ := p.RequestPermission(context.Background(), req)
		decided <- d
	}()
	return decided
}

func TestPermissionDialogShowsWhatWillHappen(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)

	req := agentcore.PermissionRequest{
		Tool: "write_file", Op: "write", Path: "src/a.go", Detail: "写入文件内容（覆盖原有内容）",
	}
	updated, _ := m.Update(permissionRequestMsg(req))
	m = updated.(Model)

	view := m.render()
	for _, want := range []string{"需要你确认", "write_file 想要修改 src/a.go", "覆盖原有内容", "[1] 允许一次", "[2] 本会话总是允许", "[3] 拒绝"} {
		if !strings.Contains(view, want) {
			t.Errorf("弹窗缺 %q：\n%s", want, view)
		}
	}
	if strings.Contains(view, "› ") {
		t.Errorf("弹窗期间不该还显示输入框：\n%s", view)
	}
}

func TestPermissionDialogSwallowsTyping(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)
	m.input.InsertString("半截输入")

	req := agentcore.PermissionRequest{Tool: "edit_file", Op: "write", Path: "src/a.go"}
	updated, _ := m.Update(permissionRequestMsg(req))
	m = updated.(Model)

	m, _ = press(m, keyRune)

	if got := m.input.Value(); got != "半截输入" {
		t.Errorf("弹窗期间按键不该落进输入框，实际 %q", got)
	}
	if m.pending == nil {
		t.Error("未作答时弹窗应保持打开")
	}
}

func TestPermissionDialogAnswerGoesBackToTool(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)

	req := agentcore.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"}
	decided := askPermission(t, p, req)

	// 界面侧的等待者：与 Init 里挂的那条同一条链。
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- waitPermission(p)() }()
	updated, _ := m.Update(<-msgCh)
	m = updated.(Model)

	m, cmd := press(m, tea.KeyPressMsg{Code: '1', Text: "1"})

	select {
	case d := <-decided:
		if d != agentcore.DecisionAllowOnce {
			t.Fatalf("工具侧应收到 AllowOnce，实际 %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：作答没有送回去")
	}
	if m.pending != nil {
		t.Error("作答后弹窗应关闭")
	}
	if !strings.Contains(strings.Join(m.transcript, "\n"), "已允许一次") {
		t.Errorf("记录里应留一行痕迹：%q", m.transcript)
	}
	if cmd == nil {
		t.Error("作答后应继续等待下一次询问")
	}
}

func TestPermissionDialogSessionDecisionNote(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)

	decided := askPermission(t, p, agentcore.PermissionRequest{Tool: "bash", Op: "exec", Path: "."})
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- waitPermission(p)() }()
	updated, _ := m.Update(<-msgCh)
	m = updated.(Model)

	m, _ = press(m, tea.KeyPressMsg{Code: '2', Text: "2"})

	select {
	case d := <-decided:
		if d != agentcore.DecisionAllowSession {
			t.Fatalf("工具侧应收到 AllowSession，实际 %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：作答没有送回去")
	}
	if !strings.Contains(strings.Join(m.transcript, "\n"), "本会话不再询问") {
		t.Errorf("记录里应说明这是会话级放行：%q", m.transcript)
	}
}

func TestPermissionDialogEscDenies(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)

	decided := askPermission(t, p, agentcore.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"})
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- waitPermission(p)() }()
	updated, _ := m.Update(<-msgCh)
	m = updated.(Model)

	// Esc 在弹窗里是"拒绝这次"，不是中断整个回合：用户想取消的是这个操作。
	m, _ = press(m, keyEsc)

	select {
	case d := <-decided:
		if d != agentcore.DecisionDeny {
			t.Fatalf("Esc 应表示拒绝，实际 %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：Esc 没有送回答案")
	}
	if m.quitting {
		t.Error("Esc 不该退出程序")
	}
}

func TestPermissionDialogCtrlCLeaves(t *testing.T) {
	p := agentcore.NewPermissionPrompt()
	m := newTestModelWithPermission(p)

	decided := askPermission(t, p, agentcore.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"})
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- waitPermission(p)() }()
	updated, _ := m.Update(<-msgCh)
	m = updated.(Model)

	// 弹窗里也要能出去，且必须先把拒绝送回去，否则工具侧会一直等。
	m, cmd := press(m, keyCtrlC)

	if !m.quitting || cmd == nil {
		t.Fatal("弹窗里 Ctrl+C 应退出")
	}
	select {
	case d := <-decided:
		if d != agentcore.DecisionDeny {
			t.Fatalf("退出时工具侧应收到拒绝，实际 %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：退出时没把拒绝送回去")
	}
}

func TestToolResultRendersBelowAnswer(t *testing.T) {
	m := newTestModel()
	// 模型已经说了一句，工具才执行完：结果行必须排在它后面。
	m.answer.WriteString("我来改 a.go")

	updated, _ := m.Update(streamEventMsg(llm.StreamEvent{
		Type: llm.EventToolResult,
		Text: "edit_file 完成（2 B）\na.go（+1 / -1）\n  package main\n+ 新行\n- 旧行",
	}))
	m = updated.(Model)

	joined := strings.Join(m.transcript, "\n")
	for _, want := range []string{"我来改 a.go", "⎿ edit_file 完成（2 B）", "a.go（+1 / -1）", "+ 新行", "- 旧行"} {
		if !strings.Contains(joined, want) {
			t.Errorf("记录里缺 %q：%q", want, m.transcript)
		}
	}
	if strings.Index(joined, "我来改 a.go") > strings.Index(joined, "⎿") {
		t.Errorf("正文应在工具结果之前：%q", m.transcript)
	}
	// 正文已经落进记录，就不该还留在流式缓冲里，否则回合结束会重复追加一遍。
	if m.answer.Len() != 0 {
		t.Errorf("正文应已落进记录，缓冲里剩 %q", m.answer.String())
	}
}

func TestToolResultColorsDiffLines(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(streamEventMsg(llm.StreamEvent{
		Type: llm.EventToolResult,
		Text: "edit_file 完成\na.go\n+ 新增\n- 删除\n  上下文",
	}))
	m = updated.(Model)

	// 比较渲染结果而不是颜色码：同一 Style 在测试环境里的输出是一致的，
	// 断言语义（哪一行走哪个分支）而不是具体 ANSI 序列。
	want := []string{
		dimStyle.Render("⎿ edit_file 完成"),
		dimStyle.Render("  a.go"),
		addedStyle.Render("  + 新增"),
		removedStyle.Render("  - 删除"),
		dimStyle.Render("    上下文"),
	}
	if len(m.transcript) != len(want) {
		t.Fatalf("应有 %d 行，实际 %d：%q", len(want), len(m.transcript), m.transcript)
	}
	for i, expect := range want {
		if m.transcript[i] != expect {
			t.Errorf("第 %d 行不符：期望 %q，实际 %q", i, expect, m.transcript[i])
		}
	}
}

func TestToolResultFailureIsMarked(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(streamEventMsg(llm.StreamEvent{
		Type: llm.EventToolResult,
		Text: "bash 失败：权限不足：用户拒绝了对 . 的执行操作",
		Err:  errors.New("权限不足：用户拒绝了对 . 的执行操作"),
	}))
	m = updated.(Model)

	if len(m.transcript) != 1 {
		t.Fatalf("应只有一行结果，实际 %q", m.transcript)
	}
	if !strings.Contains(m.transcript[0], "bash 失败") {
		t.Errorf("失败原因应显示出来：%q", m.transcript[0])
	}
	// 失败走红色分支，而不是和进展一样暗色。
	if m.transcript[0] == dimStyle.Render("⎿ bash 失败：权限不足：用户拒绝了对 . 的执行操作") {
		t.Error("失败行不该用暗色，否则和普通进展分不开")
	}
}
