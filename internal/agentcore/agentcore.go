// Package agentcore 是界面与回合逻辑之间的唯一接口。
//
// 界面只认 AgentCore：它不知道请求怎么组装、历史怎么存、以后工具怎么循环。
// 这样把 appstate（P3）插进来时，repl 一行都不用改。
//
// 本包不做的事：不渲染、不读文件、不决定"要不要继续调工具"——那分别是 repl 与 appstate 的职责。
package agentcore

import (
	"context"

	"github.com/sy665/sycode/internal/llm"
)

// AgentCore 是界面能看到的一切。
type AgentCore interface {
	// Submit 提交一次用户输入，返回事件流。
	// 第二个返回值只表示"连事件流都起不来"（如请求构造失败）；流内错误走 EventError。
	Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error)

	// Interrupt 中断当前回合。没有进行中的回合时是空操作。
	// 中断后已产出的文本仍留在历史里（PRD F-LLM-6：按了 Esc 不该丢已看到的内容）。
	Interrupt()

	// Messages 返回当前对话历史（副本，调用方改不动内部状态）。
	Messages() []llm.Message

	// Clear 清空历史。
	Clear()
}
