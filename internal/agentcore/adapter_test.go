package agentcore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/llm"
)

// fakeClient 记录收到的请求，并按 reply 产出事件流。
type fakeClient struct {
	mu       sync.Mutex
	requests []llm.Request
	reply    func(ctx context.Context) (<-chan llm.StreamEvent, error)
}

func (f *fakeClient) StreamChat(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.reply(ctx)
}

func (f *fakeClient) lastRequest(t *testing.T) llm.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("没有收到任何请求")
	}
	return f.requests[len(f.requests)-1]
}

// scripted 返回一个"先发 events，再关闭"的事件流。
func scripted(events ...llm.StreamEvent) func(context.Context) (<-chan llm.StreamEvent, error) {
	return func(context.Context) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, len(events))
		for _, ev := range events {
			ch <- ev
		}
		close(ch)
		return ch, nil
	}
}

// drain 读完事件流（3 秒超时，避免实现回归导致测试挂死）。
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

func TestSubmitBuildsRequestWithHistory(t *testing.T) {
	client := &fakeClient{reply: scripted(textEvent("我在"))}
	core := NewDirect(client, "你是 SyCode。")

	ch, err := core.Submit(context.Background(), "你是谁")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	drain(t, ch)

	first := client.lastRequest(t)
	if first.System != "你是 SyCode。" {
		t.Errorf("系统提示未注入：%q", first.System)
	}
	if len(first.Messages) != 1 || first.Messages[0].Role != llm.RoleUser || first.Messages[0].Content != "你是谁" {
		t.Fatalf("首轮请求应只含本轮输入：%+v", first.Messages)
	}

	// 第二轮必须带上"用户 + 上一条回答 + 新输入"，否则模型会失忆。
	if _, err := core.Submit(context.Background(), "接着说"); err != nil {
		t.Fatalf("第二轮提交失败：%v", err)
	}
	second := client.lastRequest(t)
	if len(second.Messages) != 3 {
		t.Fatalf("第二轮应带 3 条消息，实际 %d：%+v", len(second.Messages), second.Messages)
	}
	if second.Messages[1].Role != llm.RoleAssistant || second.Messages[1].Content != "我在" {
		t.Errorf("历史里应包含上一轮回答：%+v", second.Messages[1])
	}
	if second.Messages[2].Content != "接着说" {
		t.Errorf("最后一条应是本轮输入：%+v", second.Messages[2])
	}
}

func TestSubmitRecordsNothingWhenStreamFails(t *testing.T) {
	client := &fakeClient{reply: func(context.Context) (<-chan llm.StreamEvent, error) {
		return nil, errors.New("模型名为空")
	}}
	core := NewDirect(client, "")

	if _, err := core.Submit(context.Background(), "你好"); err == nil {
		t.Fatal("构造失败应返回错误")
	}
	// 请求都没发出去，历史里不该留下一条没人回答的提问。
	if got := core.Messages(); len(got) != 0 {
		t.Errorf("历史应为空，实际 %+v", got)
	}
}

func TestSubmitForwardsEventsUnchanged(t *testing.T) {
	client := &fakeClient{reply: scripted(
		textEvent("答"),
		llm.StreamEvent{Type: llm.EventUsage, Usage: &llm.Usage{TotalTokens: 9}},
		llm.StreamEvent{Type: llm.EventDone},
	)}
	core := NewDirect(client, "")

	ch, err := core.Submit(context.Background(), "问")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	events := drain(t, ch)

	if len(events) != 3 {
		t.Fatalf("应原样转发 3 个事件，实际 %d", len(events))
	}
	if events[0].Type != llm.EventText || events[0].Text != "答" {
		t.Errorf("正文事件被改动：%+v", events[0])
	}
	if events[1].Usage == nil || events[1].Usage.TotalTokens != 9 {
		t.Errorf("用量事件被改动：%+v", events[1])
	}
	if !events[2].IsTerminal() {
		t.Errorf("终态事件被改动：%+v", events[2])
	}
}

func TestEmptyAnswerIsNotRecorded(t *testing.T) {
	// 只有 done、没有正文：历史里不该出现空 assistant 消息（有些接口会拒绝这种消息）。
	client := &fakeClient{reply: scripted(llm.StreamEvent{Type: llm.EventDone})}
	core := NewDirect(client, "")

	ch, err := core.Submit(context.Background(), "问")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	drain(t, ch)

	if got := core.Messages(); len(got) != 1 || got[0].Role != llm.RoleUser {
		t.Errorf("空回答不应入历史，实际 %+v", got)
	}
}

func TestInterruptKeepsPartialAnswer(t *testing.T) {
	// 产出半个回答后卡住，模拟用户按 Esc。
	client := &fakeClient{reply: func(ctx context.Context) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent)
		go func() {
			defer close(ch)
			ch <- textEvent("这段会保留")
			<-ctx.Done()
		}()
		return ch, nil
	}}
	core := NewDirect(client, "")

	ch, err := core.Submit(context.Background(), "写点东西")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}

	// 先收到第一个增量，再中断。
	if ev, ok := <-ch; !ok || ev.Text != "这段会保留" {
		t.Fatalf("首个事件不符：%+v", ev)
	}
	core.Interrupt()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				// PRD F-LLM-6：已经看到的内容不该因为中断而丢失。
				msgs := core.Messages()
				if len(msgs) != 2 {
					t.Fatalf("历史应含用户与半截回答，实际 %+v", msgs)
				}
				if msgs[1].Role != llm.RoleAssistant || msgs[1].Content != "这段会保留" {
					t.Errorf("半截回答未入历史：%+v", msgs[1])
				}
				return
			}
		case <-deadline:
			t.Fatal("中断后事件流没有关闭")
		}
	}
}

func TestInterruptWithoutTurnIsNoop(t *testing.T) {
	// 没提交过就按 Esc：不能 panic。
	NewDirect(&fakeClient{}, "").Interrupt()
}

func TestClearResetsHistory(t *testing.T) {
	client := &fakeClient{reply: scripted(textEvent("答"))}
	core := NewDirect(client, "")

	ch, _ := core.Submit(context.Background(), "问")
	drain(t, ch)
	if len(core.Messages()) != 2 {
		t.Fatalf("前置条件不成立：%+v", core.Messages())
	}

	core.Clear()
	if got := core.Messages(); len(got) != 0 {
		t.Errorf("清空后应为空，实际 %+v", got)
	}

	// 清空后下一轮请求不应带旧历史。
	ch, _ = core.Submit(context.Background(), "新话题")
	drain(t, ch)
	if req := client.lastRequest(t); len(req.Messages) != 1 {
		t.Errorf("清空后请求应只含新输入，实际 %+v", req.Messages)
	}
}

func TestMessagesReturnsCopy(t *testing.T) {
	client := &fakeClient{reply: scripted(textEvent("答"))}
	core := NewDirect(client, "")

	ch, _ := core.Submit(context.Background(), "问")
	drain(t, ch)

	got := core.Messages()
	got[0].Content = "被外部改了"
	got = append(got, llm.Message{Role: llm.RoleUser, Content: "外部追加"})

	after := core.Messages()
	if len(after) != 2 {
		t.Errorf("外部 append 不该影响内部历史，实际 %d 条", len(after))
	}
	if after[0].Content != "问" {
		t.Errorf("外部改写污染了内部历史：%q", after[0].Content)
	}
}

func TestAnswerTextIsConcatenated(t *testing.T) {
	// 正文是分片到达的，历史里必须拼成一条完整回答，而不是一条一个字的碎片。
	client := &fakeClient{reply: scripted(textEvent("你"), textEvent("好"), llm.StreamEvent{Type: llm.EventDone})}
	core := NewDirect(client, "")

	ch, _ := core.Submit(context.Background(), "打招呼")
	drain(t, ch)

	msgs := core.Messages()
	if len(msgs) != 2 {
		t.Fatalf("应只有用户与回答两条消息，实际 %+v", msgs)
	}
	if msgs[1].Content != "你好" {
		t.Errorf("分片应拼成完整回答，实际 %q", msgs[1].Content)
	}
}

// textEvent 造一个正文增量事件。
func textEvent(text string) llm.StreamEvent {
	return llm.StreamEvent{Type: llm.EventText, Text: text}
}

func TestSubmitPropagatesParentCancel(t *testing.T) {
	// 界面取消（Esc 或退出界面）必须真的传到 client：否则底层还在读流，白花钱。
	ctx, cancel := context.WithCancel(context.Background())
	innerCtx := make(chan context.Context, 1)

	client := &fakeClient{reply: func(c context.Context) (<-chan llm.StreamEvent, error) {
		innerCtx <- c
		ch := make(chan llm.StreamEvent)
		go func() {
			<-c.Done()
			close(ch)
		}()
		return ch, nil
	}}
	core := NewDirect(client, "")

	ch, err := core.Submit(ctx, "问")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	cancel()

	select {
	case c := <-innerCtx:
		select {
		case <-c.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("父 context 取消后，传给 client 的 context 没有跟着取消")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client 没有被调用")
	}

	// 事件流也要随之关闭，否则界面会一直等下去。
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("取消后事件流没有关闭")
		}
	}
}
