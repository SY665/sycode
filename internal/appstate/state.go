// Package appstate 是回合状态与工具循环：整个进程里唯一有循环的地方（ADR-001）。
//
// 一次 Submit = 若干次"请求 → 读事件 → 执行工具 → 回灌结果"，直到模型不再要工具。
// 本包不碰 UI、不读 stdin、不发事件之外的日志；provider 只管一次请求，不认识循环。
package appstate

import (
	"context"
	"log/slog"
	"sync"

	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/session"
	"github.com/sy665/sycode/internal/tools"
)

// defaultMaxTurns 是单次 Submit 内最多几轮"模型 → 工具 → 模型"。
//
// 25 轮足够完成"看结构 → 读几个文件 → 改一处 → 跑测试"这类任务；
// 再多基本是模型在原地打转，继续下去只是烧 token。
const defaultMaxTurns = 25

// Deps 是回合需要的全部外部依赖。显式注入，不用全局。
type Deps struct {
	Client llm.Client
	Tools  *tools.Registry
	Guard  *filesystem.Guard
	// Perm 是权限询问通道；为空时 Pending 操作会被拒绝并说明原因。
	Perm tools.PermissionRequester
	// Store 与 Session 是会话库与当前会话。任一为空就不落盘——
	// 存不下历史不该妨碍对话，所以这里是可选依赖而不是必需依赖。
	Store   *session.Store
	Session *session.Session
	// System 是系统提示（P3 是启动时拼一次的静态文本）。
	System string
	// MaxTurns 为 0 时用 defaultMaxTurns。
	MaxTurns int
}

// State 保存一次会话的历史与进行中的回合。
//
// 所有导出方法都可以被界面在别的 goroutine 调用，因此内部状态一律加锁。
type State struct {
	deps Deps

	mu       sync.Mutex
	messages []llm.Message
	cancel   context.CancelFunc
}

// New 只存引用，不做 IO。
func New(d Deps) *State {
	return &State{deps: d}
}

// Messages 返回历史副本：界面只读，改不动内部状态。
func (s *State) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.messages) == 0 {
		return nil
	}
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Clear 清空历史。不打断进行中的回合：要停请先 Interrupt。
func (s *State) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
}

// Restore 把恢复出来的回合灌进历史。
//
// 只放正文：**工具调用不进去**。tool 消息必须挂在一个带 id 与参数的 tool_calls 后面，
// 而会话库里只存了摘要——硬塞会让下一次请求的消息序列非法，模型直接报错。
// 代价是重启后模型不知道"上次跑过什么"，用户却看得到（界面用的是同一份 Turn）。
// 要让模型也知道，就得在 AssistantTurn 里存完整调用，那是改存储格式，另作决定。
//
// 没有用户输入的孤立回答跳过：它在界面上有位置，但在上下文里会形成连续两条 assistant
// 消息，破坏 user/assistant 交替，部分供应商会拒收。
func (s *State) Restore(turns []session.Turn) {
	restored := make([]llm.Message, 0, len(turns)*2)
	for _, turn := range turns {
		if turn.User == "" {
			continue
		}
		restored = append(restored, llm.Message{Role: llm.RoleUser, Content: turn.User})
		if turn.Assistant != "" {
			restored = append(restored, llm.Message{Role: llm.RoleAssistant, Content: turn.Assistant})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, restored...)
}

// Interrupt 取消进行中的回合；没有回合时是空操作。
func (s *State) Interrupt() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// maxTurns 取轮次上限。
func (s *State) maxTurns() int {
	if s.deps.MaxTurns > 0 {
		return s.deps.MaxTurns
	}
	return defaultMaxTurns
}

// appendMessage 追加一条历史。
func (s *State) appendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

// setCancel 记录当前回合的取消句柄。
func (s *State) setCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
}

// record 往会话库追加一条事件。
//
// 失败只记日志：存不下历史是遗憾，让对话因此中断是事故。
// 会话依赖是可选的，所以这里也接受"没配库"的正常情况。
func (s *State) record(kind session.EventKind, payload any) {
	if s.deps.Store == nil || s.deps.Session == nil {
		return
	}
	ev, err := session.NewEvent(kind, payload)
	if err == nil {
		err = s.deps.Store.Append(s.deps.Session, ev)
	}
	if err != nil {
		slog.Warn("写入会话失败，本次对话继续", "kind", string(kind), "err", err)
	}
}
