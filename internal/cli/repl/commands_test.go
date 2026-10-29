package repl

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/agentcore"
)

func TestHelpListsEveryCommand(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("/help")

	m, _ = press(m, keyEnter)

	if len(m.transcript) != 1 {
		t.Fatalf("应只有一块输出：%q", m.transcript)
	}
	view := m.transcript[0]

	// 帮助与实际命令必须同源：写死两份，早晚出现"照帮助敲却没反应"。
	for _, cmd := range commandList() {
		if !strings.Contains(view, cmd.name) {
			t.Errorf("/help 漏了 %s：\n%s", cmd.name, view)
		}
		if cmd.alias != "" && !strings.Contains(view, cmd.alias) {
			t.Errorf("/help 漏了别名 %s：\n%s", cmd.alias, view)
		}
	}
	if !strings.Contains(view, "Esc") || !strings.Contains(view, "Ctrl+C") {
		t.Errorf("应提示两个快捷键的行为：\n%s", view)
	}
}

func TestQuitAliasWorks(t *testing.T) {
	m := newTestModel()
	m.input.InsertString("/quit")

	m, cmd := press(m, keyEnter)

	if !m.quitting || cmd == nil {
		t.Fatalf("/quit 应退出：quitting=%v cmd=%v", m.quitting, cmd)
	}
}

func TestClearResetsContextButSaysRecordKept(t *testing.T) {
	core := &fakeCore{}
	m := newTestModelWithCore(core)
	m.deps.ContextBase = 100
	m.lastPromptTokens = 500
	m.transcript = []string{"旧的一行"}
	m.answer.WriteString("还在流式里的半句")

	m.input.InsertString("/clear")
	m, _ = press(m, keyEnter)

	if core.cleared != 1 {
		t.Errorf("应调用一次 Core.Clear，实际 %d", core.cleared)
	}
	if m.lastPromptTokens != 0 {
		t.Errorf("上报的用量应清零，实际 %d", m.lastPromptTokens)
	}
	if m.answer.Len() != 0 {
		t.Errorf("流式缓冲应清空，实际 %q", m.answer.String())
	}
	if len(m.transcript) != 1 {
		t.Fatalf("清空后只该剩提示行，实际 %q", m.transcript)
	}
	// 会话文件是只追加的账本，清上下文不会删记录——不说清用户会以为磁盘上也空了。
	if !strings.Contains(m.transcript[0], "记录仍然保留") {
		t.Errorf("提示应说明记录仍保留：%q", m.transcript[0])
	}
}

func TestSessionsListsWhatTheEngineReports(t *testing.T) {
	core := &fakeCore{sessions: []agentcore.SessionInfo{
		{ID: "20261029-201835-0b30", Title: "改一下 a.go", Turns: 3, Updated: time.Date(2026, 10, 29, 20, 18, 0, 0, time.Local)},
		{ID: "20261028-101010-abcd", Turns: 1, Updated: time.Date(2026, 10, 28, 10, 10, 0, 0, time.Local)},
	}}
	m := newTestModelWithCore(core)
	m.input.InsertString("/sessions")

	m, _ = press(m, keyEnter)

	view := m.transcript[0]
	for _, want := range []string{"历史会话（2）", "20261029-201835-0b30", "3 轮", "改一下 a.go", "（无标题）", "只列出，不切换"} {
		if !strings.Contains(view, want) {
			t.Errorf("/sessions 输出缺 %q：\n%s", want, view)
		}
	}
}

func TestSessionsEmptyAndError(t *testing.T) {
	empty := newTestModelWithCore(&fakeCore{})
	empty.input.InsertString("/sessions")
	empty, _ = press(empty, keyEnter)
	if !strings.Contains(empty.transcript[0], "还没有历史会话") {
		t.Errorf("空列表应说明白：%q", empty.transcript[0])
	}

	broken := newTestModelWithCore(&fakeCore{sessionsErr: errors.New("执行失败：读不出来")})
	broken.input.InsertString("/sessions")
	broken, _ = press(broken, keyEnter)
	if !strings.Contains(broken.transcript[0], "读取会话失败") {
		t.Errorf("出错应显示原因：%q", broken.transcript[0])
	}
}

func TestUnknownSlashCommandIsSentAsInput(t *testing.T) {
	// 打错的命令按普通输入处理：v0.1 没有"未知命令"的提示，
	// 这条测试是把现状钉住——哪天要加提示，先改这里。
	core := &fakeCore{}
	m := newTestModelWithCore(core)
	m.input.InsertString("/nope")

	m, _ = press(m, keyEnter)

	if len(core.inputs) != 1 || core.inputs[0] != "/nope" {
		t.Fatalf("未知斜杠命令应作为输入送出，实际 %q", core.inputs)
	}
}
