package appstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/tools"
)

// maxConsecutiveFailures 是"连续几次工具失败后提醒换策略"的阈值。
//
// 模型常见的坏习惯是换个措辞重试同一条命令。连续失败到第三次时，
// 与其继续放它试，不如在工具结果里直接点出来。
const maxConsecutiveFailures = 3

// Submit 起一个回合，返回事件流。
//
// 第二个返回值只表示"连请求都发不出去"（没有客户端、输入为空）；
// 之后所有问题都走事件流里的终态事件，调用方只需一条处理路径。
func (s *State) Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error) {
	if s.deps.Client == nil {
		return nil, errors.New("执行失败：没有可用的模型客户端（内部装配错误）")
	}
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("参数错误：输入为空")
	}

	turnCtx, cancel := context.WithCancel(ctx)
	s.setCancel(cancel)

	out := make(chan llm.StreamEvent)
	go s.runTurn(turnCtx, input, out)
	return out, nil
}

// runTurn 是循环本体：请求 → 读事件 → 有工具就执行并回灌 → 再请求。
//
// 终态事件只由这里发一次：provider 在单次流里也会发 done/error，
// 但那只是"这一轮流结束"，不是"这个回合结束"——中间还要经过工具执行。
func (s *State) runTurn(ctx context.Context, input string, out chan<- llm.StreamEvent) {
	defer close(out)

	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: input})

	var (
		answer   strings.Builder
		failures int
		limit    = s.maxTurns()
	)

	for turn := 1; turn <= limit; turn++ {
		stream, err := s.deps.Client.StreamChat(ctx, s.buildRequest())
		if err != nil {
			emit(ctx, out, llm.StreamEvent{Type: llm.EventError, Err: err})
			return
		}

		calls, text, usage, streamErr := s.handleEvents(ctx, stream, out)
		answer.WriteString(text)
		// assistant 消息必须整条记下来：工具结果要挂在这条带 tool_calls 的消息后面，
		// 否则下一轮请求的消息序列不合法（tool 消息找不到对应的调用）。
		s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: calls})

		if streamErr != nil {
			emit(ctx, out, llm.StreamEvent{Type: llm.EventError, Err: streamErr})
			return
		}
		if len(calls) == 0 {
			emit(ctx, out, llm.StreamEvent{Type: llm.EventDone, Usage: usage})
			return
		}

		failures = s.executeTools(ctx, calls, out, failures)
	}

	// 轮次用尽：这不是错误而是"该换个办法"，所以文案要说下一步。
	emit(ctx, out, llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf(
		"已达到 %d 轮工具调用上限，先停在这里。可以把任务拆小，或直接告诉我下一步做什么", limit)})
}

// buildRequest 组装下一轮的请求。
//
// 系统提示每轮都带（不参与历史）；工具 schema 每轮都从注册表导出，
// 这样将来支持"中途禁用某个工具"时不用额外同步。
func (s *State) buildRequest() llm.Request {
	s.mu.Lock()
	messages := append([]llm.Message(nil), s.messages...)
	s.mu.Unlock()

	return llm.Request{
		System:   s.deps.System,
		Messages: messages,
		Tools:    toLLMTools(s.deps.Tools),
	}
}

// toLLMTools 把工具 schema 转成模型层类型。
//
// 两个包各有自己的 Schema 类型（tools 不 import llm），转换就在这里做一次：
// 放在这层的理由是它同时认识两边，而两边都不该认识对方。
func toLLMTools(registry *tools.Registry) []llm.ToolSchema {
	if registry == nil {
		return nil
	}
	schemas := registry.Schemas()
	if len(schemas) == 0 {
		return nil
	}
	out := make([]llm.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		out = append(out, llm.ToolSchema{
			Name:        schema.Name,
			Description: schema.Description,
			Input:       schema.Input,
		})
	}
	return out
}

// handleEvents 读一次流：转发增量、收集工具调用，遇到终态就返回。
//
// 不转发 done/error：那是"这一轮结束"，不是"这个回合结束"。
func (s *State) handleEvents(ctx context.Context, stream <-chan llm.StreamEvent, out chan<- llm.StreamEvent) (
	calls []llm.ToolCall, text string, usage *llm.Usage, err error,
) {
	var builder strings.Builder

	for ev := range stream {
		switch ev.Type {
		case llm.EventText:
			builder.WriteString(ev.Text)
			if !emit(ctx, out, ev) {
				return calls, builder.String(), nil, ctx.Err()
			}

		case llm.EventToolEnd:
			// provider 的 tool_end 表示"参数收齐了"，这是可执行的那一份（tool_start 只是快照）。
			if ev.Tool != nil {
				calls = append(calls, *ev.Tool)
			}
			if !emit(ctx, out, ev) {
				return calls, builder.String(), nil, ctx.Err()
			}

		case llm.EventDone:
			return calls, builder.String(), ev.Usage, nil

		case llm.EventError:
			return calls, builder.String(), nil, ev.Err

		default:
			// reasoning / retry / usage / tool_start 原样转发给界面。
			if !emit(ctx, out, ev) {
				return calls, builder.String(), nil, ctx.Err()
			}
		}
	}

	// 流关了却没有终态：实现有问题，但不能静默当成正常结束。
	return calls, builder.String(), nil, errors.New("执行失败：事件流在给出终态之前就结束了")
}

// executeTools 逐个执行工具并把结果回灌给模型，返回更新后的连续失败计数。
//
// 失败的**也要回灌**：模型需要看到"参数错在哪""权限被拒"，才能换个做法；
// 只往界面上报错、模型那边一片空白，它下一轮只会重复同一个调用。
func (s *State) executeTools(ctx context.Context, calls []llm.ToolCall, out chan<- llm.StreamEvent, failures int) int {
	for _, call := range calls {
		if ctx.Err() != nil {
			return failures
		}

		result, callErr := s.executeOne(ctx, call)
		if callErr != nil {
			failures++
		} else {
			failures = 0
		}

		content := result.Content
		if callErr != nil {
			content = callErr.Error()
		}
		if failures >= maxConsecutiveFailures {
			content += fmt.Sprintf("\n\n（提示：已经连续 %d 次工具调用失败，换个做法或先向用户确认，不要重复同样的调用）", failures)
		}

		s.appendMessage(llm.Message{
			Role:       llm.RoleTool,
			Content:    content,
			ToolCallID: call.ID,
		})

		// 界面需要知道"这次调用结果如何"，否则用户只看到模型在自言自语。
		emit(ctx, out, llm.StreamEvent{
			Type: llm.EventToolResult,
			Tool: &call,
			Text: summarizeResult(call, result, callErr),
			Err:  callErr,
		})
	}
	return failures
}

// executeOne 查表 → 校验 → 执行。
//
// 权限判定不在这里：工具内部通过 askAccess 问 Guard（ADR-005），
// 这样"哪个工具碰了什么"只有一处逻辑，不必在循环里按名字分支。
func (s *State) executeOne(ctx context.Context, call llm.ToolCall) (tools.Result, error) {
	if s.deps.Tools == nil {
		return tools.Result{}, fmt.Errorf("执行失败：没有注册任何工具，无法执行 %q", call.Name)
	}
	tool, ok := s.deps.Tools.Get(call.Name)
	if !ok {
		return tools.Result{}, fmt.Errorf("执行失败：没有名为 %q 的工具。可用工具：%s",
			call.Name, toolNames(s.deps.Tools))
	}

	input, err := parseInput(call.Arguments)
	if err != nil {
		return tools.Result{}, err
	}
	if validator, ok := tool.(tools.Validator); ok {
		if err := validator.ValidateInput(ctx, input); err != nil {
			return tools.Result{}, err
		}
	}

	// 工具实现必须自己保证不 panic（架构约束），这里不 recover：
	// 用 recover 兜住会让真正的 bug 变成"工具老是失败"，反而更难查。
	return tool.Execute(ctx, input)
}

// parseInput 解析工具参数。
//
// 模型给出的 JSON 可能残缺（流被截断、自己拼错），这里必须给出可操作的提示，
// 而不是把 json 包的英文错误原样丢回去。
func parseInput(raw string) (tools.Input, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return tools.Input{}, nil
	}
	input, err := decodeJSON(trimmed)
	if err != nil {
		return nil, fmt.Errorf("参数错误：工具参数不是合法 JSON（%v）。请给出形如 {\"path\":\"main.go\"} 的对象", err)
	}
	return input, nil
}

// decodeJSON 把参数文本解成工具输入。
func decodeJSON(raw string) (tools.Input, error) {
	var input tools.Input
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, err
	}
	return input, nil
}

// toolNames 把可用工具名拼成一行，供"没有这个工具"的提示使用。
func toolNames(registry *tools.Registry) string {
	all := registry.All()
	if len(all) == 0 {
		return "（空）"
	}
	names := make([]string, 0, len(all))
	for _, tool := range all {
		names = append(names, tool.Name())
	}
	return strings.Join(names, "、")
}

// summarizeResult 给界面的一句话结果。
func summarizeResult(call llm.ToolCall, result tools.Result, err error) string {
	if err != nil {
		return fmt.Sprintf("%s 失败：%s", call.Name, firstLine(err.Error()))
	}
	summary := fmt.Sprintf("%s 完成", call.Name)
	if len(result.Content) > 0 {
		summary += "（" + itoaBytes(len(result.Content)) + "）"
	}
	if result.Artifact != "" {
		summary += "，输出已落盘"
	}
	return summary
}

// firstLine 取多行文本的第一行，供摘要使用。
func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// itoaBytes 把字节数写成便于阅读的短形式。
func itoaBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}

// emit 带 ctx 取消的发送，返回 false 表示下游已放弃。
func emit(ctx context.Context, out chan<- llm.StreamEvent, ev llm.StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
