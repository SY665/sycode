package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	oai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"
)

const (
	// eventBuffer 是事件队列的深度。
	//
	// 要有一点缓冲：解析 goroutine 不必等 UI 处理完就能继续读流，首 token 到屏幕更快；
	// 但也不能太大，否则模型快而 UI 慢时会在内存里攒下整段回答，失去背压。
	eventBuffer = 32

	// finishToolCalls 是"这一轮要调工具"的 finish_reason，决定何时收尾 tool_end。
	finishToolCalls = "tool_calls"
)

// OpenAIConfig 是构造客户端的全部输入。
type OpenAIConfig struct {
	BaseURL string
	APIKey  string // 本地端点可为空
	Model   string
}

// OpenAI 走 OpenAI 兼容的 chat/completions 流式接口。
//
// 一份实现覆盖 OpenAI / DeepSeek / Kimi / vLLM / Ollama 等兼容端点：
// 差异只在 base_url、model 与是否要密钥，不需要第二份代码。
type OpenAI struct {
	client oai.Client
	model  string
}

// NewOpenAI 只校验配置，不发请求。
//
// 失败条件：模型名为空、端点不是合法的 http(s) 地址。密钥为空不算失败——本地端点就是这样。
func NewOpenAI(cfg OpenAIConfig) (*OpenAI, error) {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("模型名为空：请检查配置中的 active_model 与 providers 注册表")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("端点地址非法 %q：需要形如 https://api.openai.com/v1 的 http(s) 地址", baseURL)
		}
	}

	// 显式传 key：SDK 默认会读 OPENAI_API_KEY/OPENAI_BASE_URL，那会让环境变量悄悄盖掉配置文件，
	// 用户改 configs.json 却不生效，排查起来极难。
	// 显式关重试：重试由 llm/retry.go 统一做（要发 EventRetry 事件），否则次数会翻倍且 UI 看不到。
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return &OpenAI{client: oai.NewClient(opts...), model: model}, nil
}

// StreamChat 发起一次流式请求。
//
// HTTP 层失败（401、连不上）不在这里返回：SDK 要等第一次读取才暴露，统一走
// StreamEvent{Type: EventError}，消费方只需处理一条路径。
func (c *OpenAI) StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamEvent, eventBuffer)
	go c.run(ctx, params, out)
	return out, nil
}

// run 负责"请求 → 解析"的整轮，并按可重试性决定要不要再来一次。
//
// 重试放在这里而不是包一层 StreamChat：流式接口一旦开始吐字就无法"撤回"，
// 只有第一次尝试什么都没产出时，重试对用户才是透明的。
func (c *OpenAI) run(ctx context.Context, params oai.ChatCompletionNewParams, out chan<- StreamEvent) {
	defer close(out)

	var outcome pumpOutcome
	err := Do(ctx, maxRetries+1, func(attemptCtx context.Context) error {
		stream := c.client.Chat.Completions.NewStreaming(attemptCtx, params)
		result, err := c.pump(attemptCtx, stream, out)
		outcome = result
		if err != nil && result.emitted {
			// 已经给用户看过内容：包一层让 isRetryable 拒绝重试，但错误链保持原样。
			return &partialError{err: err}
		}
		return err
	}, func(attempt int, _ error) {
		// 让 UI 能显示"第 N 次失败，正在重试"，否则用户只会觉得卡住。
		emit(ctx, out, newEvent(EventRetry, withAttempt(attempt)))
	})

	if err == nil {
		emit(ctx, out, doneEvent(outcome.usage))
		return
	}
	// 终态错误：Translate 把英文原文换成中文；ctx 已取消时 emit 会自行放弃。
	emit(ctx, out, errEvent(Translate(err)))
}

// buildParams 把请求翻译成 SDK 参数；只在"请求根本构造不出来"时返回错误。
func (c *OpenAI) buildParams(req Request) (oai.ChatCompletionNewParams, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.model
	}
	if model == "" {
		return oai.ChatCompletionNewParams{}, errors.New("模型名为空：请求与客户端都没有指定模型")
	}
	if len(req.Messages) == 0 {
		return oai.ChatCompletionNewParams{}, errors.New("请求里没有任何消息：至少要有一条用户消息")
	}

	messages := toOpenAIMessages(req.Messages)
	if system := strings.TrimSpace(req.System); system != "" {
		// 系统提示单独一个 role=system 消息放在最前：兼容端点都认这种形态。
		messages = append([]oai.ChatCompletionMessageParamUnion{oai.SystemMessage(system)}, messages...)
	}

	params := oai.ChatCompletionNewParams{
		Model:    model,
		Messages: messages,
		// 不打开就拿不到用量：兼容端点默认不往流里塞 usage。
		StreamOptions: oai.ChatCompletionStreamOptionsParam{IncludeUsage: oai.Bool(true)},
	}
	if req.MaxOutputTokens > 0 {
		params.MaxCompletionTokens = oai.Int(int64(req.MaxOutputTokens))
	}
	if effort := strings.TrimSpace(req.ThinkingEffort); effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(effort)
	}
	if tools := toOpenAITools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	return params, nil
}

// pumpOutcome 是一次解析的副产物。
type pumpOutcome struct {
	// usage 是供应商报的用量，没报则为 nil。
	usage *Usage
	// emitted 表示已经向用户展示过内容（正文或工具调用）。
	// 它决定"这次失败能否重试"：展示过再重试会出现重复内容。
	emitted bool
}

// pump 解析一次请求的流，只发增量与工具事件；终态事件由 run 统一发。
//
// 返回错误表示这次尝试失败：由 run 判断能否重试、并在最后译成中文。
func (c *OpenAI) pump(ctx context.Context, stream *ssestream.Stream[oai.ChatCompletionChunk], out chan<- StreamEvent) (pumpOutcome, error) {
	defer stream.Close()

	var (
		outcome pumpOutcome
		calls   toolCallBuffer
		sawEnd  bool // 是否收到过"正常结束"的信号（finish_reason 或 usage）
	)

	for stream.Next() {
		chunk := stream.Current()

		// 开了 include_usage 后，最后一个分片的 choices 为空、只有 usage。
		if chunk.Usage.TotalTokens > 0 {
			outcome.usage = &Usage{
				PromptTokens:     int(chunk.Usage.PromptTokens),
				CompletionTokens: int(chunk.Usage.CompletionTokens),
				TotalTokens:      int(chunk.Usage.TotalTokens),
			}
			sawEnd = true
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				sawEnd = true
			}

			if choice.Delta.Content != "" {
				outcome.emitted = true
				if !emit(ctx, out, newEvent(EventText, withText(choice.Delta.Content))) {
					return outcome, ctx.Err()
				}
			}

			for _, delta := range choice.Delta.ToolCalls {
				if started := calls.merge(delta); started != nil {
					// 工具调用也要算"已展示"：重试会让 UI 里多出一次调用。
					outcome.emitted = true
					if !emit(ctx, out, newEvent(EventToolStart, withTool(started))) {
						return outcome, ctx.Err()
					}
				}
			}

			if choice.FinishReason == finishToolCalls && !emitCalls(ctx, out, calls.drain()) {
				return outcome, ctx.Err()
			}
		}
	}

	if err := stream.Err(); err != nil {
		return outcome, err
	}

	if !sawEnd {
		// SDK 的 SSE 解码器会静默丢掉"没有空行收尾"的半截事件，看起来和正常结束一样
		// （实测：连到一半断开时 stream.Err() 仍为 nil）。因此靠结束信号判断截断：
		// 真实的兼容端点一定会在最后一个分片给 finish_reason（调工具时是 "tool_calls"），
		// 少数不给的也会在 include_usage 下补一个 usage 分片。
		// 宁可提示"可能不完整"，也不能把半句话当完整回答交给用户。
		return outcome, errStreamTruncated
	}

	// 有的兼容端点只给 finish_reason=stop 就结束了；把剩下的调用补发，避免它凭空消失。
	if !emitCalls(ctx, out, calls.drain()) {
		return outcome, ctx.Err()
	}
	return outcome, nil
}

// emit 发送一个事件，返回 false 表示消费方已不再接收（ctx 取消或下游退出）。
//
// 所有发送都走它：直接写 channel 会在下游放弃读取时卡死 goroutine，泄漏到进程退出。
func emit(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitCalls 依次发送 tool_end；中途被取消时返回 false。
func emitCalls(ctx context.Context, out chan<- StreamEvent, calls []*ToolCall) bool {
	for _, call := range calls {
		if !emit(ctx, out, newEvent(EventToolEnd, withTool(call))) {
			return false
		}
	}
	return true
}

// toOpenAIMessages 把内部消息转成 SDK 参数（纯转换，单测直接比结构）。
func toOpenAIMessages(msgs []Message) []oai.ChatCompletionMessageParamUnion {
	out := make([]oai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			asst := oai.AssistantMessage(m.Content)
			if len(m.ToolCalls) > 0 {
				calls := make([]oai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
				for _, call := range m.ToolCalls {
					calls = append(calls, toOpenAIToolCall(call))
				}
				asst.OfAssistant.ToolCalls = calls
			}
			out = append(out, asst)
		case RoleTool:
			out = append(out, oai.ToolMessage(m.Content, m.ToolCallID))
		default:
			// 只有三种角色；其余一律按 user 处理而不是丢弃——
			// 少一条消息会让模型看到断掉的历史，比角色不精确危险得多。
			out = append(out, oai.UserMessage(m.Content))
		}
	}
	return out
}

// toOpenAIToolCall 转换 assistant 消息里的一次调用回放。
func toOpenAIToolCall(call ToolCall) oai.ChatCompletionMessageToolCallUnionParam {
	return oai.ChatCompletionMessageToolCallUnionParam{
		OfFunction: &oai.ChatCompletionMessageFunctionToolCallParam{
			ID: call.ID,
			Function: oai.ChatCompletionMessageFunctionToolCallFunctionParam{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		},
	}
}

// toOpenAITools 转换工具说明。Input 为空时留空对象，表示"这个工具不需要参数"。
func toOpenAITools(schemas []ToolSchema) []oai.ChatCompletionToolUnionParam {
	out := make([]oai.ChatCompletionToolUnionParam, 0, len(schemas))
	for _, schema := range schemas {
		input := schema.Input
		if input == nil {
			input = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, oai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        schema.Name,
			Description: oai.String(schema.Description),
			Parameters:  shared.FunctionParameters(input),
		}))
	}
	return out
}

// toolCallBuffer 累积流式下发的工具调用。
//
// 协议特征：id 与 name 只出现在首个分片，arguments 是分片字符串要按 index 拼接，
// 且同一分片可能同时推进多个调用。因此必须按 index 归并，不能"来一个发一个"。
type toolCallBuffer struct {
	order []int
	calls map[int]*toolCallState
}

// toolCallState 记录单个调用的累积值与"是否已通知上层开始"。
type toolCallState struct {
	call      ToolCall
	announced bool
}

// merge 归并一个分片；当该调用的 id 与 name 都凑齐、且还没通知过时，
// 返回一份快照供上层发 tool_start（快照是副本，后续追加的参数不会改写已发出的事件）。
func (b *toolCallBuffer) merge(delta oai.ChatCompletionChunkChoiceDeltaToolCall) *ToolCall {
	if b.calls == nil {
		b.calls = make(map[int]*toolCallState)
	}

	index := int(delta.Index)
	state, ok := b.calls[index]
	if !ok {
		state = &toolCallState{}
		b.calls[index] = state
		b.order = append(b.order, index)
	}

	if delta.ID != "" && state.call.ID == "" {
		state.call.ID = delta.ID
	}
	if delta.Function.Name != "" && state.call.Name == "" {
		state.call.Name = delta.Function.Name
	}
	state.call.Arguments += delta.Function.Arguments

	if state.announced || state.call.ID == "" || state.call.Name == "" {
		return nil
	}
	state.announced = true
	snapshot := state.call
	return &snapshot
}

// drain 取出全部已累积的调用（按出现顺序）并清空缓冲。
// 返回值是副本：调用方拿去发事件后，缓冲里不会再被原地修改。
func (b *toolCallBuffer) drain() []*ToolCall {
	if len(b.order) == 0 {
		return nil
	}
	out := make([]*ToolCall, 0, len(b.order))
	for _, index := range b.order {
		call := b.calls[index].call
		out = append(out, &call)
	}
	b.order = nil
	b.calls = nil
	return out
}
