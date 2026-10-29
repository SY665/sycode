package repl

import (
	"strings"
	"testing"

	"github.com/sy665/sycode/internal/llm"
)

// fakeCoreWithMessages 让 /context 能读到"历史"。
type fakeCoreWithMessages struct {
	fakeCore
	messages []llm.Message
}

func (f *fakeCoreWithMessages) Messages() []llm.Message { return f.messages }

func TestContextUsageSplitsByRole(t *testing.T) {
	core := &fakeCoreWithMessages{messages: []llm.Message{
		{Role: llm.RoleUser, Content: "三个字"},
		{Role: llm.RoleAssistant, Content: "四个字的"},
		{Role: llm.RoleTool, Content: "工具结果一段"},
	}}
	m := newTestModelWithCore(core)
	m.deps.ContextBase = 3000
	m.deps.ContextWindow = 128000

	u := m.contextUsageFor()

	if u.Base != 3000 {
		t.Errorf("固定开销应取自装配时的估算：%d", u.Base)
	}
	if u.User != llm.EstimateTokens("三个字") {
		t.Errorf("用户消息估算不符：%d", u.User)
	}
	if u.Assistant != llm.EstimateTokens("四个字的") {
		t.Errorf("助手回复估算不符：%d", u.Assistant)
	}
	if u.Tool != llm.EstimateTokens("工具结果一段") {
		t.Errorf("工具结果估算不符：%d", u.Tool)
	}
	if want := u.Base + u.User + u.Assistant + u.Tool; u.Total != want {
		t.Errorf("合计不符：%d，期望 %d", u.Total, want)
	}
}

func TestContextPrefersReportedUsage(t *testing.T) {
	core := &fakeCoreWithMessages{messages: []llm.Message{
		{Role: llm.RoleUser, Content: "很短"},
	}}
	m := newTestModelWithCore(core)
	m.deps.ContextBase = 100
	m.deps.ContextWindow = 128000

	// 上报之前：估算
	before := m.renderContext(m.contextUsageFor())
	if !strings.Contains(before, "≈") || !strings.Contains(before, "估算") {
		t.Errorf("没有上报时应标注估算：\n%s", before)
	}

	// 收到 usage 之后：以它为准，并且不再标 ≈（只有这一行是准的，分类仍是估的）
	m.lastPromptTokens = 64000
	after := m.renderContext(m.contextUsageFor())
	if strings.Contains(after, "≈") {
		t.Errorf("有上报时不该再标 ≈：\n%s", after)
	}
	if !strings.Contains(after, "供应商上报") {
		t.Errorf("应说明数字来源：\n%s", after)
	}
	if !strings.Contains(after, "50.0%") {
		t.Errorf("64000/128000 应是 50%%：\n%s", after)
	}
}

func TestContextWithoutWindowSaysUnknown(t *testing.T) {
	m := newTestModel()
	m.deps.ContextBase = 500
	m.deps.ContextWindow = 0 // 注册表里没填

	out := m.renderContext(m.contextUsageFor())
	if strings.Contains(out, "0.0%") {
		t.Errorf("窗口未知时绝不能显示 0%%：\n%s", out)
	}
	if !strings.Contains(out, "上限未知") {
		t.Errorf("应明说上限未知：\n%s", out)
	}
}

func TestContextCommandAppendsOutput(t *testing.T) {
	m := newTestModel()
	m.deps.ContextBase = 1200
	m.deps.ContextWindow = 64000
	m.input.InsertString("/context")

	m, cmd := press(m, keyEnter)

	if cmd != nil {
		t.Errorf("/context 不该产生 cmd，实际 %T", cmd)
	}
	if len(m.transcript) != 1 {
		t.Fatalf("应只有一块输出，实际 %d 行：%q", len(m.transcript), m.transcript)
	}
	for _, want := range []string{"上下文占用", "系统提示与工具", "1.2k", "64k"} {
		if !strings.Contains(m.transcript[0], want) {
			t.Errorf("输出缺 %q：\n%s", want, m.transcript[0])
		}
	}
	// 命令本身不该被当成用户输入送进对话。
	if strings.Contains(m.transcript[0], "› /context") {
		t.Error("/context 不该写进对话记录")
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1k",
		1250:    "1.2k",
		128000:  "128k",
		1050000: "1.1M",
	}
	for in, want := range cases {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q，期望 %q", in, got, want)
		}
	}
}
