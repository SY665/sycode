package agentcore

import (
	"context"

	"github.com/sy665/sycode/internal/appstate"
	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/session"
	"github.com/sy665/sycode/internal/tools"
)

// Turn 是恢复出来的一轮对话。别名指向 session：界面不该为了显示历史去 import 存储层。
type Turn = session.Turn

// Core 是 AgentCore 的正式实现：把调用转给 appstate。
//
// 这一层刻意薄：它存在的唯一目的是让界面不依赖 appstate。
// 将来换回合实现（加压缩、加子代理）时，界面一行都不用改——这正是 P2 留这道缝的目的。
type Core struct {
	state *appstate.State
}

// 编译期确认它满足接口：漏一个方法在这里就报错，而不是等装配时才炸。
var _ AgentCore = (*Core)(nil)

// New 装配一个回合引擎（不落盘）。
//
// perm 为 nil 表示没有权限询问通道：写与执行类操作会被拒绝并说明原因（等价于只读模式）。
// 这不是残缺，而是安全的默认——没有界面点头，就不该改用户的东西。
func New(client llm.Client, registry *tools.Registry, guard *filesystem.Guard,
	perm tools.PermissionRequester, system string) AgentCore {
	return NewWithSession(client, registry, guard, perm, system, nil, nil)
}

// NewWithSession 在 New 的基础上挂上会话库：回合结束把 user/assistant 追加进 JSONL。
//
// 返回具体类型而不是 AgentCore：调用方通常在装配完成后还要补一句 Restore（把上次的历史
// 灌回来），而"恢复"不是界面能力，不该塞进 AgentCore 接口。
func NewWithSession(client llm.Client, registry *tools.Registry, guard *filesystem.Guard,
	perm tools.PermissionRequester, system string, store *session.Store, sess *session.Session) *Core {
	return &Core{state: appstate.New(appstate.Deps{
		Client:  client,
		Tools:   registry,
		Guard:   guard,
		Perm:    perm,
		Store:   store,
		Session: sess,
		System:  system,
	})}
}

// Submit 起一个回合。
func (c *Core) Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error) {
	return c.state.Submit(ctx, input)
}

// Interrupt 中断当前回合。
func (c *Core) Interrupt() { c.state.Interrupt() }

// Messages 返回历史副本。
func (c *Core) Messages() []llm.Message { return c.state.Messages() }

// Clear 清空历史。
func (c *Core) Clear() { c.state.Clear() }

// Restore 把上次会话的历史灌回来。只应在启动装配时调用一次。
func (c *Core) Restore(turns []session.Turn) { c.state.Restore(turns) }
