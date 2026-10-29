package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newTestModel 造一个可直接驱动 Update 的模型，不启动真实终端。
func newTestModel() Model {
	m := NewModel(Deps{Version: "0.1.0", Model: "test-model"})
	m.width, m.height = 80, 24
	m.input.SetWidth(78)
	return m
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
	keyCtrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	keyRune  = tea.KeyPressMsg{Code: 'x', Text: "x"}
)

func TestSubmitAppendsInputAndPlaceholder(t *testing.T) {
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
	if !strings.Contains(m.transcript[1], "模型尚未接入") {
		t.Errorf("第二行应说明模型未接入，实际 %q", m.transcript[1])
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
