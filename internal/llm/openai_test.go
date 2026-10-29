package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer 起一个按给定分片回流的兼容端点。done 为 true 时在末尾补 data: [DONE]。
//
// 用 httptest 而不是 mock SDK：这样测的是"我们发出的 HTTP 请求 + 真实解析"，
// 换 SDK 版本时测试不会跟着失效。
func sseServer(t *testing.T, done bool, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("请求 Content-Type 不符：%q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("测试服务器不支持 flush")
			return
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		if done {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient 指向测试服务器；密钥随意，服务端不校验。
func newTestClient(t *testing.T, baseURL string) *OpenAI {
	t.Helper()
	client, err := NewOpenAI(OpenAIConfig{BaseURL: baseURL, APIKey: "test-key", Model: "test-model"})
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	return client
}

// collect 读完事件流，超时即失败（避免实现回归导致测试挂死）。
func collect(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var events []StreamEvent
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

// textOf 拼出所有正文增量。
func textOf(events []StreamEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == EventText {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// terminal 断言"有且只有一个终态事件，且位于最后"，返回它。
func terminal(t *testing.T, events []StreamEvent) StreamEvent {
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

const (
	chunkHello = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"你"},"finish_reason":null}]}`
	chunkWorld = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"好"},"finish_reason":null}]}`
	chunkStop  = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	chunkUsage = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`
)

func TestStreamChat_TextAndUsage(t *testing.T) {
	srv := sseServer(t, true, chunkHello, chunkWorld, chunkStop, chunkUsage)
	client := newTestClient(t, srv.URL)

	ch, err := client.StreamChat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "打个招呼"}},
	})
	if err != nil {
		t.Fatalf("发起请求失败：%v", err)
	}

	events := collect(t, ch)
	if got := textOf(events); got != "你好" {
		t.Errorf("正文增量拼接不符：期望 %q，实际 %q", "你好", got)
	}

	final := terminal(t, events)
	if final.Type != EventDone {
		t.Fatalf("正常结束应为 done，实际 %+v", final)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 8 || final.Usage.PromptTokens != 3 {
		t.Errorf("用量没有解析出来：%+v", final.Usage)
	}
}

func TestStreamChat_WithoutDoneSentinel(t *testing.T) {
	// 有些兼容端点不发 [DONE] 就关连接；只要分片完整，仍应正常收尾。
	srv := sseServer(t, false, chunkHello, chunkStop)
	client := newTestClient(t, srv.URL)

	ch, err := client.StreamChat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("发起请求失败：%v", err)
	}

	events := collect(t, ch)
	final := terminal(t, events)
	if final.Type != EventDone {
		t.Errorf("缺 [DONE] 但分片完整时应正常结束，实际 %+v", final)
	}
	if got := textOf(events); got != "你" {
		t.Errorf("正文不符：%q", got)
	}
}

func TestStreamChat_TruncatedStreamIsAnError(t *testing.T) {
	// 连接在半个分片处断掉：必须有终态错误，不能静默当成正常结束，
	// 否则用户会以为模型只说了半句话。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+chunkHello+"\n\n")
		fmt.Fprint(w, `data: {"id":"c1","object":"chat.comp`)
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(srv.Close)

	ch, err := newTestClient(t, srv.URL).StreamChat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("发起请求失败：%v", err)
	}

	final := terminal(t, collect(t, ch))
	if final.Type != EventError || final.Err == nil {
		t.Fatalf("截断的流应以错误收尾，实际 %+v", final)
	}
}

func TestStreamChat_HTTPErrorIsTranslated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`)
	}))
	t.Cleanup(srv.Close)

	ch, err := newTestClient(t, srv.URL).StreamChat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("HTTP 错误应走事件流而不是构造期失败：%v", err)
	}

	final := terminal(t, collect(t, ch))
	if final.Type != EventError {
		t.Fatalf("401 应以错误收尾，实际 %+v", final)
	}
	msg := final.Err.Error()
	for _, want := range []string{"执行失败", "401", "invalid_api_key", "密钥"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误文案缺少 %q：%s", want, msg)
		}
	}
}

func TestStreamChat_ToolCallFragments(t *testing.T) {
	// 工具调用是分片的：id/name 只来一次，arguments 逐段拼接。
	srv := sseServer(t, true,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	ch, err := newTestClient(t, srv.URL).StreamChat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "读文件"}},
		Tools:    []ToolSchema{{Name: "read_file", Description: "读文件"}},
	})
	if err != nil {
		t.Fatalf("发起请求失败：%v", err)
	}

	events := collect(t, ch)
	var starts, ends []*ToolCall
	for _, ev := range events {
		switch ev.Type {
		case EventToolStart:
			starts = append(starts, ev.Tool)
		case EventToolEnd:
			ends = append(ends, ev.Tool)
		}
	}

	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("每个调用应各发一次 start/end，实际 %d/%d", len(starts), len(ends))
	}
	if starts[0].ID != "call_1" || starts[0].Name != "read_file" {
		t.Errorf("start 事件应带 id/name：%+v", starts[0])
	}
	// start 是快照：此时参数还没到齐，不应被后续分片改写。
	if starts[0].Arguments != `{"pa` {
		t.Errorf("start 快照应保留当时已到的参数，实际 %q", starts[0].Arguments)
	}
	if ends[0].Arguments != `{"path":"a.go"}` {
		t.Errorf("end 事件应带完整参数，实际 %q", ends[0].Arguments)
	}
	if final := terminal(t, events); final.Type != EventDone {
		t.Errorf("工具调用后仍应正常收尾，实际 %+v", final)
	}
}

func TestStreamChat_CancelUnblocksReader(t *testing.T) {
	// 用户按 Esc：下游不再读，解析 goroutine 必须退出而不是卡在发送上。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 200; i++ {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", chunkHello); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := newTestClient(t, srv.URL).StreamChat(ctx, Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("发起请求失败：%v", err)
	}

	// 读到第一个事件就放弃，然后取消：channel 必须被关闭，否则这里会超时。
	if ev, ok := <-ch; !ok || ev.Type != EventText {
		t.Fatalf("第一个事件应为正文增量，实际 %+v", ev)
	}
	cancel()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // 已关闭，goroutine 正常退出
			}
		case <-deadline:
			t.Fatal("取消后 channel 没有关闭，解析 goroutine 泄漏")
		}
	}
}

func TestStreamChat_EmptyMessagesIsConstructError(t *testing.T) {
	// 请求都构造不出来时才返回第二个返回值，此时 channel 必须是 nil。
	ch, err := newTestClient(t, "http://127.0.0.1:1").StreamChat(context.Background(), Request{})
	if err == nil {
		t.Fatal("空请求应返回错误")
	}
	if ch != nil {
		t.Error("构造失败时不应返回 channel")
	}
	if !strings.Contains(err.Error(), "没有任何消息") {
		t.Errorf("错误应说明缺什么：%v", err)
	}
}

func TestNewOpenAI_ValidatesConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     OpenAIConfig
		wantErr string
	}{
		{"模型为空", OpenAIConfig{Model: " "}, "模型名为空"},
		{"端点没有协议", OpenAIConfig{Model: "m", BaseURL: "api.openai.com/v1"}, "端点地址非法"},
		{"端点协议不支持", OpenAIConfig{Model: "m", BaseURL: "ftp://example.com"}, "端点地址非法"},
		{"端点没有主机", OpenAIConfig{Model: "m", BaseURL: "https://"}, "端点地址非法"},
		{"本地端点无密钥可用", OpenAIConfig{Model: "m", BaseURL: "http://127.0.0.1:11434/v1"}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewOpenAI(c.cfg)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错：%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("期望包含 %q 的错误，实际 %v", c.wantErr, err)
			}
		})
	}
}

func TestBuildParams(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1")

	params, err := client.buildParams(Request{
		System:          "你是 SyCode。",
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		MaxOutputTokens: 256,
		ThinkingEffort:  "low",
		Tools:           []ToolSchema{{Name: "read_file", Description: "读文件", Input: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("构造参数失败：%v", err)
	}

	if params.Model != "test-model" {
		t.Errorf("请求没指定模型时应回落到客户端模型，实际 %q", params.Model)
	}
	if len(params.Messages) != 2 || params.Messages[0].OfSystem == nil {
		t.Errorf("系统提示应作为第一条 system 消息：%+v", params.Messages)
	}
	if !params.MaxCompletionTokens.Valid() || params.MaxCompletionTokens.Value != 256 {
		t.Errorf("输出上限未传递：%+v", params.MaxCompletionTokens)
	}
	if params.ReasoningEffort != "low" {
		t.Errorf("思考档位未传递：%q", params.ReasoningEffort)
	}
	if len(params.Tools) != 1 || params.Tools[0].OfFunction == nil || params.Tools[0].OfFunction.Function.Name != "read_file" {
		t.Errorf("工具说明未转换：%+v", params.Tools)
	}
	if !params.StreamOptions.IncludeUsage.Valid() || !params.StreamOptions.IncludeUsage.Value {
		t.Error("必须打开 include_usage，否则拿不到用量")
	}
}

func TestBuildParams_RequestModelOverridesClient(t *testing.T) {
	params, err := newTestClient(t, "http://127.0.0.1:1").buildParams(Request{
		Model:    "another-model",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("构造参数失败：%v", err)
	}
	if params.Model != "another-model" {
		t.Errorf("请求里的模型应优先，实际 %q", params.Model)
	}
}

func TestToOpenAIMessages(t *testing.T) {
	msgs := toOpenAIMessages([]Message{
		{Role: RoleUser, Content: "看下 a.go"},
		{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: RoleTool, Content: "package main", ToolCallID: "call_1"},
		{Role: Role("unknown"), Content: "当作 user"},
	})

	if len(msgs) != 4 {
		t.Fatalf("消息数量不符：%d", len(msgs))
	}
	if msgs[0].OfUser == nil {
		t.Error("第一条应为 user")
	}
	if msgs[1].OfAssistant == nil || len(msgs[1].OfAssistant.ToolCalls) != 1 {
		t.Fatalf("assistant 消息应带工具调用：%+v", msgs[1])
	}
	call := msgs[1].OfAssistant.ToolCalls[0].OfFunction
	if call == nil || call.ID != "call_1" || call.Function.Name != "read_file" || call.Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("工具调用回放不完整：%+v", call)
	}
	if msgs[2].OfTool == nil || msgs[2].OfTool.ToolCallID != "call_1" {
		t.Errorf("tool 消息应带 tool_call_id：%+v", msgs[2])
	}
	// 未知角色按 user 处理：丢消息会让模型看到断掉的历史。
	if msgs[3].OfUser == nil {
		t.Error("未知角色应退回 user，而不是被丢弃")
	}
}
