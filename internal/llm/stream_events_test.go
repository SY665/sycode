package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestNewEventFillsOnlyRequestedFields(t *testing.T) {
	call := &ToolCall{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}

	cases := []struct {
		name string
		ev   StreamEvent
		want func(t *testing.T, ev StreamEvent)
	}{
		{
			name: "文本增量只带 Text",
			ev:   newEvent(EventText, withText("你好")),
			want: func(t *testing.T, ev StreamEvent) {
				if ev.Type != EventText || ev.Text != "你好" {
					t.Errorf("文本事件不符：%+v", ev)
				}
				if ev.Tool != nil || ev.Usage != nil || ev.Err != nil {
					t.Errorf("文本事件不应带其它字段：%+v", ev)
				}
			},
		},
		{
			name: "工具事件带指针",
			ev:   newEvent(EventToolStart, withTool(call)),
			want: func(t *testing.T, ev StreamEvent) {
				if ev.Tool == nil || ev.Tool.ID != "call_1" || ev.Tool.Name != "read_file" {
					t.Errorf("工具事件不符：%+v", ev.Tool)
				}
			},
		},
		{
			name: "重试事件带次数",
			ev:   newEvent(EventRetry, withAttempt(2)),
			want: func(t *testing.T, ev StreamEvent) {
				if ev.Attempt != 2 {
					t.Errorf("重试次数不符：%d", ev.Attempt)
				}
			},
		},
		{
			name: "用量事件带副本",
			ev:   newEvent(EventUsage, withUsage(Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})),
			want: func(t *testing.T, ev StreamEvent) {
				if ev.Usage == nil || ev.Usage.TotalTokens != 15 {
					t.Errorf("用量事件不符：%+v", ev.Usage)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.want(t, c.ev) })
	}
}

func TestWithUsageCopiesValue(t *testing.T) {
	// withUsage 收值类型，调用方后续改动自己的变量不该影响已发出的事件。
	usage := Usage{PromptTokens: 1}
	ev := newEvent(EventUsage, withUsage(usage))
	usage.PromptTokens = 999

	if ev.Usage.PromptTokens != 1 {
		t.Errorf("事件里的用量应保持构造时的值，实际 %d", ev.Usage.PromptTokens)
	}
}

func TestErrEventAlwaysCarriesError(t *testing.T) {
	sentinel := errors.New("连接被重置")
	ev := errEvent(sentinel)

	if ev.Type != EventError {
		t.Fatalf("类型应为 error，实际 %q", ev.Type)
	}
	// 用 %w 链或直接持有，errors.Is 都必须能判定。
	if !errors.Is(ev.Err, sentinel) {
		t.Errorf("错误链被切断：%v", ev.Err)
	}
	if !ev.IsTerminal() {
		t.Error("error 事件必须被判定为终态")
	}
}

func TestErrEventNilErrorIsReplaced(t *testing.T) {
	// 允许传 nil，但必须换成一句说明：消费方可以假定 Err 一定不为 nil。
	ev := errEvent(nil)
	if ev.Err == nil {
		t.Fatal("nil 错误应被替换为兜底说明")
	}
	if !strings.Contains(ev.Err.Error(), "没有给出原因") {
		t.Errorf("兜底文案不符：%v", ev.Err)
	}
}

func TestDoneEvent(t *testing.T) {
	ev := doneEvent(&Usage{TotalTokens: 42})
	if ev.Type != EventDone || ev.Usage == nil || ev.Usage.TotalTokens != 42 {
		t.Fatalf("完成事件不符：%+v", ev)
	}
	if !ev.IsTerminal() {
		t.Error("done 事件必须被判定为终态")
	}

	// 供应商不报用量时也要能正常收尾。
	noUsage := doneEvent(nil)
	if noUsage.Usage != nil {
		t.Errorf("无用量时应保持 nil，实际 %+v", noUsage.Usage)
	}
	if !noUsage.IsTerminal() {
		t.Error("无用量的 done 事件同样是终态")
	}
}

func TestIsTerminalExcludesDeltas(t *testing.T) {
	for _, typ := range []EventType{EventText, EventReasoning, EventToolStart, EventToolEnd, EventUsage, EventRetry} {
		if newEvent(typ).IsTerminal() {
			t.Errorf("%q 不应被判定为终态", typ)
		}
	}
}
