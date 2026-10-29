package appstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/session"
	"github.com/sy665/sycode/internal/tools"
)

// fakeClient 按脚本返回事件流，并记录每次请求。
type fakeClient struct {
	mu       sync.Mutex
	requests []llm.Request
	turns    []func(ctx context.Context) (<-chan llm.StreamEvent, error)
}

func (f *fakeClient) StreamChat(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	f.mu.Lock()
	index := len(f.requests)
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	if index >= len(f.turns) {
		return nil, errors.New("脚本里没有更多回合了")
	}
	return f.turns[index](ctx)
}

func (f *fakeClient) requestAt(t *testing.T, index int) llm.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.requests) {
		t.Fatalf("没有第 %d 次请求（共 %d 次）", index, len(f.requests))
	}
	return f.requests[index]
}

func (f *fakeClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// scripted 造一个"发完就关"的事件流。
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

// failing 造一个请求阶段就失败的回合。
func failing(err error) func(context.Context) (<-chan llm.StreamEvent, error) {
	return func(context.Context) (<-chan llm.StreamEvent, error) { return nil, err }
}

// collect 读完事件流。
func collect(t *testing.T, ch <-chan llm.StreamEvent) []llm.StreamEvent {
	t.Helper()
	var events []llm.StreamEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("读取事件超时，已收到 %d 个", len(events))
		}
	}
}

// mustSubmit 起一个回合，失败即终止测试。
func mustSubmit(t *testing.T, state *State, input string) <-chan llm.StreamEvent {
	t.Helper()
	ch, err := state.Submit(context.Background(), input)
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	return ch
}

// stubTool 是最小工具实现。
type stubTool struct {
	name   string
	result tools.Result
	err    error
	calls  int
	inputs []tools.Input
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "测试工具" }
func (s *stubTool) InputSchema() map[string]any {
	return map[string]any{"path": map[string]any{"type": "string"}}
}
func (s *stubTool) Execute(_ context.Context, in tools.Input) (tools.Result, error) {
	s.calls++
	s.inputs = append(s.inputs, in)
	return s.result, s.err
}

// newState 造一个带工具注册表的 State。
func newState(client llm.Client, registry *tools.Registry, maxTurns int) *State {
	return New(Deps{Client: client, Tools: registry, System: "你是 SyCode。", MaxTurns: maxTurns})
}

// terminal 断言"只有一个终态事件且在最后"。
func terminal(t *testing.T, events []llm.StreamEvent) llm.StreamEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("事件流为空")
	}
	last := events[len(events)-1]
	if !last.IsTerminal() {
		t.Fatalf("最后一个事件不是终态：%+v", last)
	}
	for _, ev := range events[:len(events)-1] {
		if ev.IsTerminal() {
			t.Fatalf("终态事件出现了不止一次：%+v", ev)
		}
	}
	return last
}

func textOf(events []llm.StreamEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == llm.EventText {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func TestSubmitSingleTurnWithoutTools(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "你"},
			llm.StreamEvent{Type: llm.EventText, Text: "好"},
			llm.StreamEvent{Type: llm.EventDone, Usage: &llm.Usage{TotalTokens: 5}},
		),
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, err := state.Submit(context.Background(), "打个招呼")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	events := collect(t, ch)

	if got := textOf(events); got != "你好" {
		t.Errorf("正文不符：%q", got)
	}
	final := terminal(t, events)
	if final.Type != llm.EventDone || final.Usage == nil || final.Usage.TotalTokens != 5 {
		t.Errorf("终态应带用量：%+v", final)
	}
	if client.count() != 1 {
		t.Errorf("没有工具调用时不该再请求，实际 %d 次", client.count())
	}

	// 历史：user + assistant
	msgs := state.Messages()
	if len(msgs) != 2 || msgs[0].Role != llm.RoleUser || msgs[1].Role != llm.RoleAssistant {
		t.Fatalf("历史不符：%+v", msgs)
	}
	if msgs[1].Content != "你好" {
		t.Errorf("assistant 内容应累加：%q", msgs[1].Content)
	}
}

func TestSubmitRunsToolThenAnswers(t *testing.T) {
	toolCall := llm.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`}

	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		// 第一轮：模型要看文件
		scripted(
			llm.StreamEvent{Type: llm.EventToolStart, Tool: &toolCall},
			llm.StreamEvent{Type: llm.EventToolEnd, Tool: &toolCall},
			llm.StreamEvent{Type: llm.EventDone},
		),
		// 第二轮：基于结果作答
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "文件里是 package main"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}

	registry := tools.NewRegistry()
	readTool := &stubTool{name: "read_file", result: tools.Result{Content: "package main\n"}}
	if err := registry.Register(readTool); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	state := newState(client, registry, 0)
	ch, err := state.Submit(context.Background(), "main.go 里是什么")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	events := collect(t, ch)

	if readTool.calls != 1 {
		t.Fatalf("工具应执行一次，实际 %d", readTool.calls)
	}
	if got := readTool.inputs[0]["path"]; got != "main.go" {
		t.Errorf("参数未解析：%+v", readTool.inputs[0])
	}
	if !strings.Contains(textOf(events), "package main") {
		t.Errorf("最终回答应包含模型的话：%q", textOf(events))
	}

	// 界面要能看到工具执行结果。
	var results []llm.StreamEvent
	for _, ev := range events {
		if ev.Type == llm.EventToolResult {
			results = append(results, ev)
		}
	}
	if len(results) != 1 || results[0].Tool == nil || results[0].Tool.Name != "read_file" {
		t.Fatalf("应有一个工具结果事件：%+v", results)
	}
	if results[0].Err != nil {
		t.Errorf("成功时不该带错误：%v", results[0].Err)
	}

	if final := terminal(t, events); final.Type != llm.EventDone {
		t.Errorf("应以 done 收尾：%+v", final)
	}

	// 第二轮请求必须带上工具结果，否则模型看不到自己调用的返回值。
	second := client.requestAt(t, 1)
	if len(second.Messages) != 3 {
		t.Fatalf("第二轮应有 3 条消息（user/assistant/tool），实际 %d：%+v", len(second.Messages), second.Messages)
	}
	if second.Messages[2].Role != llm.RoleTool || second.Messages[2].ToolCallID != "call_1" {
		t.Errorf("最后一条应是带 ID 的工具结果：%+v", second.Messages[2])
	}
	if !strings.Contains(second.Messages[2].Content, "package main") {
		t.Errorf("工具结果内容未回灌：%q", second.Messages[2].Content)
	}
	// assistant 那条必须带 tool_calls，否则消息序列非法。
	if len(second.Messages[1].ToolCalls) != 1 {
		t.Errorf("assistant 消息应带 tool_calls：%+v", second.Messages[1])
	}
}

func TestToolErrorIsFedBackToModel(t *testing.T) {
	toolCall := llm.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{}`}

	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventToolEnd, Tool: &toolCall},
			llm.StreamEvent{Type: llm.EventDone},
		),
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "那我换个路径"}, llm.StreamEvent{Type: llm.EventDone}),
	}}

	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file", err: errors.New(`参数错误：缺少必填字段 "path"`)})

	state := newState(client, registry, 0)
	ch, _ := state.Submit(context.Background(), "读一下")
	events := collect(t, ch)

	// 错误必须回灌给模型：它看到"缺 path"才知道怎么修。
	second := client.requestAt(t, 1)
	last := second.Messages[len(second.Messages)-1]
	if last.Role != llm.RoleTool || !strings.Contains(last.Content, "缺少必填字段") {
		t.Fatalf("错误应作为工具结果回灌：%+v", last)
	}

	// 界面上也要标出失败。
	var failed bool
	for _, ev := range events {
		if ev.Type == llm.EventToolResult && ev.Err != nil {
			failed = true
		}
	}
	if !failed {
		t.Error("工具结果事件应带错误")
	}
	// 失败之后仍应正常收尾（模型已经改口），而不是当成回合失败。
	if final := terminal(t, events); final.Type != llm.EventDone {
		t.Errorf("工具失败不等于回合失败：%+v", final)
	}
}

func TestConsecutiveFailuresGetHint(t *testing.T) {
	makeCall := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "read_file", Arguments: `{}`}
	}
	call1, call2, call3 := makeCall("call_1"), makeCall("call_2"), makeCall("call_3")

	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call1}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call2}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call3}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "停"}, llm.StreamEvent{Type: llm.EventDone}),
	}}

	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file", err: errors.New("执行失败：文件不存在")})

	state := newState(client, registry, 0)
	ch, _ := state.Submit(context.Background(), "一直试")
	collect(t, ch)

	// 第三条工具结果里应出现"换个做法"的提示。
	last := client.requestAt(t, 3)
	toolMsg := last.Messages[len(last.Messages)-1]
	if !strings.Contains(toolMsg.Content, "连续 3 次") {
		t.Errorf("连续失败应提示换策略：%q", toolMsg.Content)
	}
}

func TestTurnLimitStopsLoop(t *testing.T) {
	// 模型每轮都要调工具：必须在 MaxTurns 处停下，而不是无限循环。
	call := llm.ToolCall{ID: "call_x", Name: "read_file", Arguments: `{}`}
	turns := make([]func(context.Context) (<-chan llm.StreamEvent, error), 5)
	for i := range turns {
		turns[i] = scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call}, llm.StreamEvent{Type: llm.EventDone})
	}
	client := &fakeClient{turns: turns}

	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file", result: tools.Result{Content: "ok"}})

	state := newState(client, registry, 2)
	ch, _ := state.Submit(context.Background(), "无限循环")
	events := collect(t, ch)

	final := terminal(t, events)
	if final.Type != llm.EventError {
		t.Fatalf("应以上限错误收尾：%+v", final)
	}
	if !strings.Contains(final.Err.Error(), "上限") {
		t.Errorf("错误应说明是轮次上限：%v", final.Err)
	}
	if client.count() != 2 {
		t.Errorf("应请求 %d 次，实际 %d", 2, client.count())
	}
}

func TestStreamErrorEndsTurn(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "半句话"},
			llm.StreamEvent{Type: llm.EventError, Err: errors.New("执行失败：模型服务返回 500")},
		),
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, _ := state.Submit(context.Background(), "hi")
	events := collect(t, ch)

	final := terminal(t, events)
	if final.Type != llm.EventError {
		t.Fatalf("应转发错误终态：%+v", final)
	}
	// 已经产出的文本仍要进历史：用户看到了，模型下一轮也该看到。
	msgs := state.Messages()
	if len(msgs) != 2 || msgs[1].Content != "半句话" {
		t.Errorf("半截回答应入历史：%+v", msgs)
	}
}

func TestSubmitFailureBeforeStream(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		failing(errors.New("模型名为空")),
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, err := state.Submit(context.Background(), "hi")
	if err != nil {
		t.Fatalf("构造失败应走事件流：%v", err)
	}
	final := terminal(t, collect(t, ch))
	if final.Type != llm.EventError || !strings.Contains(final.Err.Error(), "模型名为空") {
		t.Errorf("应把请求失败作为终态错误：%+v", final)
	}
}

func TestSubmitRejectsEmptyInput(t *testing.T) {
	state := newState(&fakeClient{}, tools.NewRegistry(), 0)
	if _, err := state.Submit(context.Background(), "   "); err == nil {
		t.Fatal("空输入应报错")
	}
}

func TestSubmitWithoutClient(t *testing.T) {
	state := New(Deps{})
	if _, err := state.Submit(context.Background(), "hi"); err == nil {
		t.Fatal("没有客户端应报错")
	}
}

func TestUnknownToolIsReportedToModel(t *testing.T) {
	call := llm.ToolCall{ID: "call_1", Name: "nope", Arguments: `{}`}
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "哦"}, llm.StreamEvent{Type: llm.EventDone}),
	}}
	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file"})

	state := newState(client, registry, 0)
	ch, _ := state.Submit(context.Background(), "用个不存在的工具")
	collect(t, ch)

	second := client.requestAt(t, 1)
	last := second.Messages[len(second.Messages)-1]
	if !strings.Contains(last.Content, "没有名为") || !strings.Contains(last.Content, "read_file") {
		t.Errorf("应告知可用工具：%q", last.Content)
	}
}

func TestInvalidArgumentsAreRejected(t *testing.T) {
	call := llm.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{"path":`}
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "改好了"}, llm.StreamEvent{Type: llm.EventDone}),
	}}
	registry := tools.NewRegistry()
	tool := &stubTool{name: "read_file"}
	_ = registry.Register(tool)

	state := newState(client, registry, 0)
	ch, _ := state.Submit(context.Background(), "坏参数")
	collect(t, ch)

	if tool.calls != 0 {
		t.Error("参数非法时不该执行工具")
	}
	second := client.requestAt(t, 1)
	last := second.Messages[len(second.Messages)-1]
	if !strings.Contains(last.Content, "不是合法 JSON") {
		t.Errorf("应说明参数问题并给出示例：%q", last.Content)
	}
}

func TestRequestCarriesSystemAndTools(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventDone}),
	}}
	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file"})
	_ = registry.Register(&stubTool{name: "bash"})

	state := newState(client, registry, 0)
	ch, _ := state.Submit(context.Background(), "hi")
	collect(t, ch)

	req := client.requestAt(t, 0)
	if req.System != "你是 SyCode。" {
		t.Errorf("系统提示未带上：%q", req.System)
	}
	if len(req.Tools) != 2 || req.Tools[0].Name != "read_file" || req.Tools[1].Name != "bash" {
		t.Errorf("工具 schema 应保持注册顺序：%+v", req.Tools)
	}
}

func TestInterruptCancelsTurn(t *testing.T) {
	// 卡住不返回的回合：Interrupt 后事件流必须关闭。
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		func(ctx context.Context) (<-chan llm.StreamEvent, error) {
			ch := make(chan llm.StreamEvent)
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventText, Text: "开始"}
				<-ctx.Done()
			}()
			return ch, nil
		},
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, err := state.Submit(context.Background(), "写长文")
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if ev := <-ch; ev.Text != "开始" {
		t.Fatalf("首个事件不符：%+v", ev)
	}

	state.Interrupt()

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

func TestClearResetsHistory(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "答"}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventDone}),
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, _ := state.Submit(context.Background(), "问")
	collect(t, ch)
	if len(state.Messages()) != 2 {
		t.Fatalf("前置条件不成立：%+v", state.Messages())
	}

	state.Clear()
	if len(state.Messages()) != 0 {
		t.Error("清空后应为空")
	}

	ch, _ = state.Submit(context.Background(), "新话题")
	collect(t, ch)
	if got := client.requestAt(t, 1); len(got.Messages) != 1 {
		t.Errorf("清空后第二轮请求不该带旧历史：%+v", got.Messages)
	}
}

func TestMessagesReturnsCopy(t *testing.T) {
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "答"}, llm.StreamEvent{Type: llm.EventDone}),
	}}
	state := newState(client, tools.NewRegistry(), 0)

	ch, _ := state.Submit(context.Background(), "问")
	collect(t, ch)

	got := state.Messages()
	got[0].Content = "被改了"
	if state.Messages()[0].Content != "问" {
		t.Error("外部改写不该影响内部历史")
	}
}

func TestToolUsesRealGuardAndFilesystem(t *testing.T) {
	// 端到端一点：循环 + 真 read_file + 真 Guard（不是 stub），确认接线正确。
	dir, err := os.MkdirTemp("", "sycode-appstate-")
	if err != nil {
		t.Fatalf("建临时目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	call := llm.ToolCall{ID: "call_1", Name: tools.ReadFileToolName, Arguments: `{"path":"main.go"}`}
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(llm.StreamEvent{Type: llm.EventToolEnd, Tool: &call}, llm.StreamEvent{Type: llm.EventDone}),
		scripted(llm.StreamEvent{Type: llm.EventText, Text: "是 package main"}, llm.StreamEvent{Type: llm.EventDone}),
	}}

	guard := filesystem.NewGuard(dir, nil)
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewReadFileTool(guard, nil)); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	state := New(Deps{Client: client, Tools: registry, Guard: guard})
	ch, _ := state.Submit(context.Background(), "main.go 里是什么")
	collect(t, ch)

	second := client.requestAt(t, 1)
	toolMsg := second.Messages[len(second.Messages)-1]
	if !strings.Contains(toolMsg.Content, "package main") {
		t.Errorf("真实工具结果未回灌：%q", toolMsg.Content)
	}
}

// toolResults 取出所有工具结果事件。
func toolResults(events []llm.StreamEvent) []llm.StreamEvent {
	var out []llm.StreamEvent
	for _, ev := range events {
		if ev.Type == llm.EventToolResult {
			out = append(out, ev)
		}
	}
	return out
}

func TestToolResultCarriesDisplayBlock(t *testing.T) {
	display := &tools.Display{
		Kind:  tools.DisplayDiff,
		Title: "a.go（+1 / -1）",
		Lines: []string{"  package main", "+ 新行", "- 旧行"},
	}
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventToolEnd, Tool: &llm.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{"path":"a.go"}`}},
			llm.StreamEvent{Type: llm.EventDone},
		),
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "改好了"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}
	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{
		name:   "edit_file",
		result: tools.Result{Content: "ok", Display: display},
	})

	events := collect(t, mustSubmit(t, newState(client, registry, 0), "改一下 a.go"))
	results := toolResults(events)
	if len(results) != 1 {
		t.Fatalf("应有一个工具结果事件，实际 %d", len(results))
	}

	// 界面靠"首行是摘要、其后是展示块"这个约定渲染，两者缺一用户就看不到改了什么。
	lines := strings.Split(results[0].Text, "\n")
	want := []string{"edit_file 完成（2 B）", "a.go（+1 / -1）", "  package main", "+ 新行", "- 旧行"}
	if len(lines) != len(want) {
		t.Fatalf("展示文本应为 %d 行，实际 %d 行：%q", len(want), len(lines), results[0].Text)
	}
	for i, expect := range want {
		if lines[i] != expect {
			t.Errorf("第 %d 行不符：期望 %q，实际 %q", i, expect, lines[i])
		}
	}
}

func TestDisplayBlockTruncatesLongOutput(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "+ 某一行"
	}

	block := displayBlock(&tools.Display{Kind: tools.DisplayList, Title: "20 条", Lines: lines})
	got := strings.Split(block, "\n")

	// 1 行标题 + maxDisplayLines 行内容 + 1 行省略说明。
	if len(got) != maxDisplayLines+2 {
		t.Fatalf("应为 %d 行，实际 %d 行：%q", maxDisplayLines+2, len(got), block)
	}
	if got[0] != "20 条" {
		t.Errorf("首行应是标题，实际 %q", got[0])
	}
	if !strings.Contains(got[len(got)-1], "其余 8 行") {
		t.Errorf("末行应说明省略了多少，实际 %q", got[len(got)-1])
	}
}

func TestDisplayBlockWithoutLines(t *testing.T) {
	if got := displayBlock(nil); got != "" {
		t.Errorf("没有展示时不该产生文本，实际 %q", got)
	}
	// 只有标题的展示（glob 的"无匹配"）必须保留：否则界面看不出是没找到。
	if got := displayBlock(&tools.Display{Title: "无匹配"}); got != "无匹配" {
		t.Errorf("只有标题时应返回标题，实际 %q", got)
	}
}

// newStoreForTest 在临时 home 里建一个真会话库：落盘路径必须走真实现，
// 否则测不到"事件真的写进了 JSONL"。
func newStoreForTest(t *testing.T) (*session.Store, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("建会话库失败：%v", err)
	}
	sess, err := store.Create()
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	return store, sess
}

func TestTurnWritesUserAndAssistantEvents(t *testing.T) {
	store, sess := newStoreForTest(t)

	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "你好，我是 SyCode"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}
	state := New(Deps{Client: client, Tools: tools.NewRegistry(), Store: store, Session: sess, System: "你是 SyCode。"})

	collect(t, mustSubmit(t, state, "你好"))

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回会话失败：%v", err)
	}
	// session_started + user_message + assistant_turn
	if len(loaded.Events) != 3 {
		t.Fatalf("应有 3 条事件，实际 %d：%+v", len(loaded.Events), loaded.Events)
	}

	var user session.UserMessage
	if err := loaded.Events[1].Decode(&user); err != nil || user.Text != "你好" {
		t.Errorf("用户输入未落库：%q（err=%v）", user.Text, err)
	}

	var turn session.AssistantTurn
	if err := loaded.Events[2].Decode(&turn); err != nil {
		t.Fatalf("回合未落库：%v", err)
	}
	if turn.Text != "你好，我是 SyCode" {
		t.Errorf("正文不符：%q", turn.Text)
	}
	if len(turn.Tools) != 0 {
		t.Errorf("这轮没有工具，不该有记录：%+v", turn.Tools)
	}
}

func TestTurnRecordsToolUsage(t *testing.T) {
	store, sess := newStoreForTest(t)

	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventToolEnd, Tool: &llm.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"main.go"}`}},
			llm.StreamEvent{Type: llm.EventDone},
		),
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "看完了"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}
	registry := tools.NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file", result: tools.Result{Content: "package main\n"}})
	state := New(Deps{Client: client, Tools: registry, Store: store, Session: sess, System: "你是 SyCode。"})

	collect(t, mustSubmit(t, state, "看看 main.go"))

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回会话失败：%v", err)
	}
	last := loaded.Events[len(loaded.Events)-1]
	var turn session.AssistantTurn
	if err := last.Decode(&turn); err != nil {
		t.Fatalf("回合未落库：%v", err)
	}
	if len(turn.Tools) != 1 {
		t.Fatalf("应记 1 次工具调用，实际 %+v", turn.Tools)
	}
	if turn.Tools[0].Name != "read_file" || !turn.Tools[0].OK {
		t.Errorf("工具记录不符：%+v", turn.Tools[0])
	}
	// 落库的摘要必须是一行：展示块是当场给人看的，存进去只会让文件变胖。
	if strings.Contains(turn.Tools[0].Summary, "\n") {
		t.Errorf("工具摘要应只有一行：%q", turn.Tools[0].Summary)
	}
}

func TestTurnWithoutStoreDoesNotFail(t *testing.T) {
	// 没有会话库是正常装配（测试、预览都这么用）：对话必须照常走完。
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "照常回答"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}
	state := New(Deps{Client: client, Tools: tools.NewRegistry(), System: "你是 SyCode。"})

	events := collect(t, mustSubmit(t, state, "你好"))
	if final := terminal(t, events); final.Type != llm.EventDone {
		t.Errorf("应以 done 收尾：%+v", final)
	}
}

func TestRestoreBuildsAlternatingHistory(t *testing.T) {
	state := New(Deps{Tools: tools.NewRegistry(), System: "你是 SyCode。"})
	state.Restore([]session.Turn{
		{User: "第一问", Assistant: "第一答"},
		// 孤立回答：界面上要显示，但不该进上下文（会造成连续两条 assistant）。
		{Assistant: "上一轮的残句"},
		{User: "第二问", Assistant: "第二答"},
	})

	msgs := state.Messages()
	want := []struct {
		role llm.Role
		text string
	}{
		{llm.RoleUser, "第一问"},
		{llm.RoleAssistant, "第一答"},
		{llm.RoleUser, "第二问"},
		{llm.RoleAssistant, "第二答"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("应有 %d 条消息，实际 %d：%+v", len(want), len(msgs), msgs)
	}
	for i, expect := range want {
		if msgs[i].Role != expect.role || msgs[i].Content != expect.text {
			t.Errorf("第 %d 条不符：%+v", i, msgs[i])
		}
	}
}

func TestRestoreKeepsUserOnlyTurn(t *testing.T) {
	state := New(Deps{Tools: tools.NewRegistry(), System: "你是 SyCode。"})
	// 上一次提问后没有回答（用户按了 Esc 或进程被杀）：问题仍然该留在上下文里。
	state.Restore([]session.Turn{{User: "问了一半"}})

	msgs := state.Messages()
	if len(msgs) != 1 || msgs[0].Role != llm.RoleUser {
		t.Fatalf("应只有一条用户消息：%+v", msgs)
	}
}

func TestDoneTurnEmitsUsage(t *testing.T) {
	// provider 的 usage 是随 done 回来的；界面要显示上下文占用就得有人把它单独发一条。
	usage := &llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "答"},
			llm.StreamEvent{Type: llm.EventDone, Usage: usage},
		),
	}}
	state := New(Deps{Client: client, Tools: tools.NewRegistry(), System: "你是 SyCode。"})

	events := collect(t, mustSubmit(t, state, "问"))

	var seen *llm.Usage
	for _, ev := range events {
		if ev.Type == llm.EventUsage {
			seen = ev.Usage
		}
	}
	if seen == nil {
		t.Fatal("正常结束的回合应补发一条 usage 事件")
	}
	if seen.PromptTokens != 120 {
		t.Errorf("usage 不符：%+v", seen)
	}
	// usage 必须在终态之前：界面收到 done 就收尾了，之后再来的数字没人接。
	if last := events[len(events)-2]; last.Type != llm.EventUsage {
		t.Errorf("usage 应紧邻终态之前，实际倒数第二条是 %q", last.Type)
	}
}

func TestDoneTurnWithoutUsageEmitsNothing(t *testing.T) {
	// 有些端点不返回 usage：那就什么也不发，界面退回估算，而不是显示 0。
	client := &fakeClient{turns: []func(context.Context) (<-chan llm.StreamEvent, error){
		scripted(
			llm.StreamEvent{Type: llm.EventText, Text: "答"},
			llm.StreamEvent{Type: llm.EventDone},
		),
	}}
	state := New(Deps{Client: client, Tools: tools.NewRegistry(), System: "你是 SyCode。"})

	for _, ev := range collect(t, mustSubmit(t, state, "问")) {
		if ev.Type == llm.EventUsage {
			t.Fatalf("没有 usage 时不该发空事件：%+v", ev)
		}
	}
}
