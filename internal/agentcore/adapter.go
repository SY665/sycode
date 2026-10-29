package agentcore

import (
	"context"
	"strings"
	"sync"

	"github.com/sy665/sycode/internal/llm"
)

// Direct 是 P2 的直连实现：一次请求、一串事件、一份历史。
//
// 它没有工具、没有会话落盘、没有上下文压缩——那些属于 P3 的 appstate。
// P3 会用 appstate 版实现替换它，而 AgentCore 接口不变，repl 不需要改。
type Direct struct {
	client llm.Client
	// system 是每回合重建的系统提示（按需读取 AGENTS.md，所以这里存的是"已经拼好的文本"）。
	// P3 交接给 appstate 后，它会改成按回合调用 systemprompt.Build。
	system string

	mu       sync.Mutex
	messages []llm.Message
	cancel   context.CancelFunc
}

// NewDirect 构造一个直连实例。system 为空表示不加系统提示。
func NewDirect(client llm.Client, system string) *Direct {
	return &Direct{client: client, system: system}
}

// Submit 发起一次请求，并把事件原样转发给界面。
//
// 历史只在这里改：先拼出"历史 + 本轮输入"再发请求，成功后才把本轮输入记进历史——
// 请求都发不出去的那次不该在历史里留一条用户消息（否则下一轮模型会看到一个没人回答的提问）。
func (d *Direct) Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	user := llm.Message{Role: llm.RoleUser, Content: input}
	req := llm.Request{
		System:   d.system,
		Messages: append(cloneMessages(d.messages), user),
	}

	turnCtx, cancel := context.WithCancel(ctx)
	stream, err := d.client.StreamChat(turnCtx, req)
	if err != nil {
		cancel()
		return nil, err
	}

	// 同一时刻只允许一个回合：新的 Submit 覆盖旧的中断句柄，旧的回合已被 UI 放弃。
	d.cancel = cancel
	d.messages = append(d.messages, user)

	out := make(chan llm.StreamEvent)
	go d.forward(turnCtx, stream, out)
	return out, nil
}

// Interrupt 取消当前回合。事件流会随之结束，已产出的文本仍然入历史。
func (d *Direct) Interrupt() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Messages 返回历史副本。
func (d *Direct) Messages() []llm.Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneMessages(d.messages)
}

// Clear 清空历史；不打断进行中的回合（调用方若要停，先 Interrupt）。
func (d *Direct) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = nil
}

// forward 转发事件并在结束时把本轮回答记入历史。
//
// 无论正常结束还是中断，只要有产出就记：用户已经看到的内容，下一轮模型也该看到，
// 否则它会以为自己的上一条回答不存在，接着说出自相矛盾的话。
func (d *Direct) forward(ctx context.Context, stream <-chan llm.StreamEvent, out chan<- llm.StreamEvent) {
	defer close(out)

	var answer strings.Builder
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				d.recordAnswer(answer.String())
				return
			}
			if ev.Type == llm.EventText {
				answer.WriteString(ev.Text)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				// 界面已放弃接收（用户中断）：仍要记下已产出内容，再退出。
				d.recordAnswer(answer.String())
				return
			}
		case <-ctx.Done():
			d.recordAnswer(answer.String())
			return
		}
	}
}

// recordAnswer 把本轮回答写入历史；空回答不写（避免历史里出现空 assistant 消息）。
func (d *Direct) recordAnswer(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, llm.Message{Role: llm.RoleAssistant, Content: text})
}

// cloneMessages 复制切片，防止调用方 append 时改动内部状态。
func cloneMessages(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	return out
}
