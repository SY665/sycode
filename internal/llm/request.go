package llm

// Role 是对话中的参与方。取值与 OpenAI 协议一致，转换时不需要映射表。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一条对话消息。
//
// 系统提示不在历史里：它属于 Request.System，每回合重建。
// 放进历史会在切换模型/重开会话后残留旧提示，且让上下文预算算不准。
type Message struct {
	Role    Role
	Content string

	// ToolCalls 只有 assistant 消息会带：模型要求执行的动作，可能一次多个。
	ToolCalls []ToolCall

	// ToolCallID 只有 tool 消息会带：这是对哪一个调用的回应。
	ToolCallID string
}

// ToolCall 是模型发起的一次工具调用。
//
// Arguments 保留原始 JSON 文本，不在这里解析成结构：模型可能给出非法或半截 JSON，
// 由工具层报"参数错误"并让模型重试，llm 层不该替它决定怎么补救。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolSchema 是发给模型的工具说明。
//
// 形状与 tools.Schema 相同，但刻意是独立类型：llm 不 import tools，
// 于是工具层加字段不会牵动模型层（转换由 appstate 在组装请求时做一次）。
type ToolSchema struct {
	Name        string
	Description string
	Input       map[string]any
}

// Request 是一次请求的全部输入。
type Request struct {
	// Model 是传给接口的原文，来自 providers 注册表的 models[].id。
	Model string

	// System 每回合重建，不参与历史；空串表示不加系统提示。
	System string

	Messages []Message

	// Tools 为空表示这一回合不给模型工具（v0.1 P2 阶段就是这样）。
	Tools []ToolSchema

	// ThinkingEffort 为空表示供应商/模型不支持或用户没开。
	ThinkingEffort string

	// MaxOutputTokens 为 0 表示不设上限，由供应商默认值决定。
	MaxOutputTokens int
}
