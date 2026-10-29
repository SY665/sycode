package session

// Turn 是一轮对话：用户说了什么，助手回了什么，用了哪些工具。
//
// 中立结构是刻意的：session 不认识 llm.Message，转成消息序列是 appstate 的事。
// 常见实现在这里反向依赖 llm/core，于是存储层被消息格式绑死，换协议要动存储。
type Turn struct {
	User      string
	Assistant string
	Tools     []ToolRecord
}

// BuildTurns 把事件流投影成回合列表。
//
// 规则：user_message 开一个新回合，紧随其后的 assistant_turn 填进当前回合。
// 没有 user 打头的 assistant_turn（异常中断留下的）单独成回合、User 留空——
// 丢掉它会让用户在恢复时看不到模型已经说过的话。
//
// 没有 error 返回值：坏载荷按"跳过这条事件"处理，与 readEvents 的容错口径一致。
// 若在这里返回错误，调用方除了"整段历史作废"没有别的选择，而那一行本来就该被忽略。
func BuildTurns(events []Event) []Turn {
	turns := make([]Turn, 0, len(events)/2+1)

	// current 用指针是为了让 append 分支能改到"正在攒的那一轮"；
	// 一旦开新一轮，上一轮已经按值存进列表，不会再被改。
	var current *Turn

	for _, ev := range events {
		switch ev.Kind {
		case KindUserMessage:
			var payload UserMessage
			if err := ev.Decode(&payload); err != nil {
				continue
			}
			if current != nil {
				turns = append(turns, *current)
			}
			current = &Turn{User: payload.Text}

		case KindAssistantTurn:
			var payload AssistantTurn
			if err := ev.Decode(&payload); err != nil {
				continue
			}
			if current == nil {
				current = &Turn{}
			}
			// 一个回合里可能有多条 assistant_turn（工具循环中途多次输出过话），
			// 它们都属于这一轮：用空行连起来，别丢。
			if current.Assistant != "" && payload.Text != "" {
				current.Assistant += "\n\n"
			}
			current.Assistant += payload.Text
			current.Tools = append(current.Tools, payload.Tools...)

		case KindSessionStarted, KindCompaction:
			// 前者是元信息，后者 v0.1 不会产生：都不构成回合。

		default:
			// 未知类型直接跳过：老版本写的会话要能被新版本读出来。
		}
	}

	if current != nil {
		turns = append(turns, *current)
	}
	return turns
}
