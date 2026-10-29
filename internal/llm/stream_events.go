package llm

import "errors"

// EventType 标识流上的一次增量属于哪一类。
type EventType string

const (
	EventText      EventType = "text"      // 正文增量
	EventReasoning EventType = "reasoning" // 思考过程增量（支持 thinking 的模型）
	EventToolStart EventType = "tool_start"
	EventToolEnd   EventType = "tool_end"
	EventUsage     EventType = "usage"
	EventRetry     EventType = "retry"
	EventDone      EventType = "done"  // 终态：正常结束
	EventError     EventType = "error" // 终态：失败
)

// Usage 是一次请求的 token 用量。部分供应商在流末尾才给，也可能完全不给。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// StreamEvent 是流上的一次增量。
//
// 字段按 Type 取用，互不干扰：Text 用于 text/reasoning，Tool 用于 tool_start/tool_end，
// Usage 用于 usage，Attempt 用于 retry，Err 用于 error。
type StreamEvent struct {
	Type    EventType
	Text    string
	Tool    *ToolCall
	Usage   *Usage
	Attempt int
	Err     error
}

// IsTerminal 判断是否为终态事件。
//
// 消费方据此收尾，不要依赖 channel 关闭时机：关闭发生在最后一个事件之后，
// 两者之间有窗口，靠关闭判断会让 UI 少渲染最后一帧。
func (e StreamEvent) IsTerminal() bool {
	return e.Type == EventDone || e.Type == EventError
}

// eventOpt 给 newEvent 传可选字段。
//
// 用选项而不是对外暴露可随意填的构造：字段与 Type 的搭配只在包内一处定义，
// 六个 provider 各自拼装时不容易把 Usage 挂到 text 事件上。
type eventOpt func(*StreamEvent)

func withText(text string) eventOpt {
	return func(e *StreamEvent) { e.Text = text }
}

func withTool(call *ToolCall) eventOpt {
	return func(e *StreamEvent) { e.Tool = call }
}

func withUsage(usage Usage) eventOpt {
	return func(e *StreamEvent) { e.Usage = &usage }
}

func withAttempt(attempt int) eventOpt {
	return func(e *StreamEvent) { e.Attempt = attempt }
}

// newEvent 是除终态外所有事件的统一入口。
func newEvent(t EventType, opts ...eventOpt) StreamEvent {
	ev := StreamEvent{Type: t}
	for _, opt := range opts {
		opt(&ev)
	}
	return ev
}

// errEvent 构造终态错误事件。
//
// err 为 nil 时兜底一条说明：让消费方可以放心地假定"error 事件的 Err 一定不为 nil"，
// 否则每个调用点都要写一次 nil 判断，漏一个就是空指针。
func errEvent(err error) StreamEvent {
	if err == nil {
		err = errors.New("模型请求失败，但没有给出原因")
	}
	return StreamEvent{Type: EventError, Err: err}
}

// doneEvent 构造终态完成事件。usage 可为 nil：不是所有供应商都会报用量。
func doneEvent(usage *Usage) StreamEvent {
	return StreamEvent{Type: EventDone, Usage: usage}
}
