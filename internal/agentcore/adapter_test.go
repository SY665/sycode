package agentcore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/tools"
)

// fakeClient 按需返回一段流。
type fakeClient struct {
	requests int
	reply    func(ctx context.Context) (<-chan llm.StreamEvent, error)
}

func (f *fakeClient) StreamChat(ctx context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	f.requests++
	return f.reply(ctx)
}

// newCore 造一个不挂工具、不挂权限通道的引擎。
func newCore(t *testing.T, client llm.Client, system string) AgentCore {
	t.Helper()
	guard := filesystem.NewGuard(t.TempDir(), nil)
	return New(client, tools.NewRegistry(), guard, nil, system)
}

// drain 读完事件流。
func drain(t *testing.T, ch <-chan llm.StreamEvent) []llm.StreamEvent {
	t.Helper()
	var events []llm.StreamEvent
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatal("读取事件超时")
		}
	}
}

// simpleReply 是一轮"吐一个字就结束"的流。
func simpleReply() func(context.Context) (<-chan llm.StreamEvent, error) {
	return func(context.Context) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, Text: "答"}
		ch <- llm.StreamEvent{Type: llm.EventDone}
		close(ch)
		return ch, nil
	}
}

func TestSubmitForwardsToAppstate(t *testing.T) {
	client := &fakeClient{reply: simpleReply()}
	core := newCore(t, client, "你是 SyCode。")

	ch, err := core.Submit(context.Background(), "问")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	events := drain(t, ch)

	if len(events) != 2 || events[1].Type != llm.EventDone {
		t.Fatalf("事件不符：%+v", events)
	}
	if client.requests != 1 {
		t.Errorf("应请求 1 次，实际 %d", client.requests)
	}
	// 历史由 appstate 管：界面通过 Messages 读。
	msgs := core.Messages()
	if len(msgs) != 2 || msgs[0].Content != "问" || msgs[1].Content != "答" {
		t.Errorf("历史不符：%+v", msgs)
	}
}

func TestClearDelegated(t *testing.T) {
	core := newCore(t, &fakeClient{reply: simpleReply()}, "")

	ch, _ := core.Submit(context.Background(), "问")
	drain(t, ch)

	core.Clear()
	if len(core.Messages()) != 0 {
		t.Error("Clear 应清空历史")
	}
}

func TestInterruptDelegated(t *testing.T) {
	client := &fakeClient{reply: func(ctx context.Context) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, Text: "开始"}
			<-ctx.Done()
		}()
		return ch, nil
	}}
	core := newCore(t, client, "")

	ch, err := core.Submit(context.Background(), "写长文")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if ev := <-ch; ev.Text != "开始" {
		t.Fatalf("首个事件不符：%+v", ev)
	}

	core.Interrupt()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("中断后事件流没有关闭")
		}
	}
}

func TestNewWiresSystemPrompt(t *testing.T) {
	// 系统提示必须真的传到了 appstate：它靠 New 的参数串起来，漏了没人会发现。
	recorder := &capturingClient{system: make(chan string, 1)}
	guard := filesystem.NewGuard(t.TempDir(), nil)
	core := New(recorder, tools.NewRegistry(), guard, nil, "你是 SyCode。")

	ch, _ := core.Submit(context.Background(), "hi")
	drain(t, ch)

	select {
	case got := <-recorder.system:
		if !strings.Contains(got, "SyCode") {
			t.Errorf("系统提示内容不符：%q", got)
		}
	default:
		t.Fatal("请求里没有系统提示")
	}
}

// capturingClient 记录请求里的系统提示。
type capturingClient struct {
	system chan string
}

func (c *capturingClient) StreamChat(_ context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	c.system <- req.System
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Type: llm.EventDone}
	close(ch)
	return ch, nil
}
