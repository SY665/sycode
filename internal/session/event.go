// Package session 把一次对话按事件流落成 JSONL，并能原样读回来。
//
// 只追加、永不改写已有行：进程被杀死时最多丢掉最后半行，不会把整个会话写成乱码。
// 恢复时损坏的行跳过并计数，一行坏掉不该让整段对话读不出来。
//
// 本包不认识模型消息：载荷是中立的 SessionStarted/UserMessage/AssistantTurn，
// 转成 llm.Message 是 appstate 的事。反过来若 import llm，恢复逻辑就被消息结构绑死，
// 换协议时连存储都要动（常见实现在这里反向依赖 llm/core，我们不跟）。
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventKind 是事件类型。新增类型只需要加一个载荷与一个解码分支——
// 这也是选"Kind + RawMessage"判别联合、而不是"一个大 struct 带若干可选指针"的原因。
type EventKind string

const (
	// KindSessionStarted 是每个会话的第一条事件，带工作目录与版本。
	KindSessionStarted EventKind = "session_started"
	// KindUserMessage 是用户输入。
	KindUserMessage EventKind = "user_message"
	// KindAssistantTurn 是一个回合的结果（正文 + 用过的工具）。
	KindAssistantTurn EventKind = "assistant_turn"
	// KindCompaction 预留给上下文压缩（P6），v0.1 不会产生。
	KindCompaction EventKind = "compaction_applied"
)

// Event 是 JSONL 的一行。
type Event struct {
	// Seq 从 1 开始，单调递增。追加时赋值，读回来的值保持原样。
	Seq  uint64          `json:"seq"`
	Kind EventKind       `json:"kind"`
	TS   time.Time       `json:"ts"`
	Data json.RawMessage `json:"data"`
}

// SessionStarted 是会话的第一条载荷。
//
// 只记工作目录：版本与模型属于"这次运行"而不是"这段对话"，
// 写进来只会让恢复出来的旧会话看上去像是用了错误的模型。
type SessionStarted struct {
	WorkDir string `json:"work_dir"`
}

// UserMessage 是一条用户输入。
type UserMessage struct {
	Text string `json:"text"`
}

// AssistantTurn 是一个回合的结果。
//
// 只存"人需要看回去"的东西：正文与工具摘要。完整的工具输入输出不入库——
// 那会把会话文件撑成几 MB，而用户重读时想看的是"改了什么"。
type AssistantTurn struct {
	Text  string       `json:"text"`
	Tools []ToolRecord `json:"tools,omitempty"`
}

// ToolRecord 是一次工具调用的摘要。
type ToolRecord struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary,omitempty"`
}

// NewEvent 把载荷编成事件。
//
// Seq 故意留 0：只有写入方能知道上一条是几号，在这里猜会让并发追加互相覆盖。
func NewEvent(kind EventKind, payload any) (Event, error) {
	if kind == "" {
		return Event{}, errors.New("参数错误：事件类型为空")
	}
	if payload == nil {
		// 统一成空对象：JSONL 里出现 `"data":null` 会让解码分支多一种空值判断。
		payload = struct{}{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("执行失败：无法编码事件 %q：%w", kind, err)
	}
	return Event{Kind: kind, TS: time.Now(), Data: raw}, nil
}

// Decode 把载荷解到 v。
func (e Event) Decode(v any) error {
	if len(e.Data) == 0 {
		return fmt.Errorf("执行失败：事件 %q（#%d）没有载荷", e.Kind, e.Seq)
	}
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("执行失败：事件 %q（#%d）的载荷无法解析：%w", e.Kind, e.Seq, err)
	}
	return nil
}
