package session

import "testing"

// ev 造一条事件，跳过 NewEvent 的编码（这里要测的正是解码之后的行为）。
func ev(kind EventKind, payload any) Event {
	event, err := NewEvent(kind, payload)
	if err != nil {
		panic(err)
	}
	return event
}

func TestBuildTurnsPairsMessages(t *testing.T) {
	turns := BuildTurns([]Event{
		ev(KindSessionStarted, SessionStarted{WorkDir: "/work"}),
		ev(KindUserMessage, UserMessage{Text: "看一下 main.go"}),
		ev(KindAssistantTurn, AssistantTurn{Text: "它是入口文件"}),
		ev(KindUserMessage, UserMessage{Text: "改个名字"}),
		ev(KindAssistantTurn, AssistantTurn{Text: "改好了"}),
	})

	if len(turns) != 2 {
		t.Fatalf("应有 2 轮，实际 %d：%+v", len(turns), turns)
	}
	if turns[0].User != "看一下 main.go" || turns[0].Assistant != "它是入口文件" {
		t.Errorf("第一轮不符：%+v", turns[0])
	}
	if turns[1].User != "改个名字" || turns[1].Assistant != "改好了" {
		t.Errorf("第二轮不符：%+v", turns[1])
	}
}

func TestBuildTurnsKeepsOrphanAssistant(t *testing.T) {
	// 没有 user 打头的 assistant_turn 只可能来自异常中断；
	// 它仍是模型说过的话，丢掉等于篡改历史。
	turns := BuildTurns([]Event{
		ev(KindAssistantTurn, AssistantTurn{Text: "上一轮的残句"}),
		ev(KindUserMessage, UserMessage{Text: "继续"}),
	})

	if len(turns) != 2 {
		t.Fatalf("应有 2 轮，实际 %d：%+v", len(turns), turns)
	}
	if turns[0].User != "" || turns[0].Assistant != "上一轮的残句" {
		t.Errorf("孤立回答应单独成轮且 User 为空：%+v", turns[0])
	}
	if turns[1].User != "继续" {
		t.Errorf("第二轮应有用户输入：%+v", turns[1])
	}
}

func TestBuildTurnsJoinsMidTurnReplies(t *testing.T) {
	// 工具循环中途模型可能说两段话：同一轮里的多条 assistant_turn 不能互相覆盖。
	turns := BuildTurns([]Event{
		ev(KindUserMessage, UserMessage{Text: "跑一下测试"}),
		ev(KindAssistantTurn, AssistantTurn{
			Text:  "我先看看有哪些包",
			Tools: []ToolRecord{{Name: "glob", OK: true}},
		}),
		ev(KindAssistantTurn, AssistantTurn{
			Text:  "都通过了",
			Tools: []ToolRecord{{Name: "bash", OK: true}},
		}),
	})

	if len(turns) != 1 {
		t.Fatalf("应合成 1 轮，实际 %d：%+v", len(turns), turns)
	}
	if turns[0].Assistant != "我先看看有哪些包\n\n都通过了" {
		t.Errorf("多段正文应用空行连接：%q", turns[0].Assistant)
	}
	if len(turns[0].Tools) != 2 {
		t.Errorf("工具记录应累积，实际 %+v", turns[0].Tools)
	}
}

func TestBuildTurnsSkipsMetaAndUnknownEvents(t *testing.T) {
	turns := BuildTurns([]Event{
		ev(KindSessionStarted, SessionStarted{WorkDir: "/work"}),
		ev(KindCompaction, nil),
		ev(EventKind("future_kind"), map[string]string{"x": "y"}),
		ev(KindUserMessage, UserMessage{Text: "只有这句"}),
	})

	if len(turns) != 1 || turns[0].User != "只有这句" {
		t.Fatalf("元信息与未知类型不该成轮：%+v", turns)
	}
}

func TestBuildTurnsSkipsCorruptPayload(t *testing.T) {
	bad := Event{Seq: 2, Kind: KindUserMessage, Data: []byte("不是 JSON")}
	turns := BuildTurns([]Event{
		bad,
		ev(KindUserMessage, UserMessage{Text: "正常的一句"}),
	})

	if len(turns) != 1 || turns[0].User != "正常的一句" {
		t.Fatalf("坏载荷应被跳过而不是让整段作废：%+v", turns)
	}
}

func TestBuildTurnsEmpty(t *testing.T) {
	if turns := BuildTurns(nil); len(turns) != 0 {
		t.Errorf("空事件流应得到空列表，实际 %+v", turns)
	}
}
