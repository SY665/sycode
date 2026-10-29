// Package llm 定义"与模型对话"的抽象：请求、消息、流式事件、系统提示。
//
// 本包只描述说什么、回来什么，不决定策略（要不要继续调工具由 appstate 决定）。
// 每个 provider 文件只做协议转换与流式解析（v0.1 只有 openai.go）。
// 依赖方向见 docs/CODING-STANDARDS.md §2.2：llm 不认识 cli/agentcore/appstate。
package llm

import "context"

// Client 是一次流式对话的发起者。
//
// 契约（消费方须知）：
//   - 返回的 channel 由发送方关闭，消费方不要自己关；
//   - 流内错误以 StreamEvent{Type: EventError} 传出，channel 里必有且只有一个终态事件
//     （EventDone 或 EventError），且是最后一个；
//   - 第二个返回值只表示"请求都构造不出来"（模型名为空、凭据缺失），此时 channel 为 nil，
//     消费方不需要读它。
type Client interface {
	StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
