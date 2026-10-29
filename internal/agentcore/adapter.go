package agentcore

import (
	"context"

	"github.com/sy665/sycode/internal/appstate"
	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/tools"
)

// Core 是 AgentCore 的正式实现：把调用转给 appstate。
//
// 这一层刻意薄：它存在的唯一目的是让界面不依赖 appstate。
// 将来换回合实现（加压缩、加子代理）时，界面一行都不用改——这正是 P2 留这道缝的目的。
type Core struct {
	state *appstate.State
}

// 编译期确认它满足接口：漏一个方法在这里就报错，而不是等装配时才炸。
var _ AgentCore = (*Core)(nil)

// New 装配一个回合引擎。
//
// perm 为 nil 表示没有权限询问通道：写与执行类操作会被拒绝并说明原因（等价于只读模式）。
// 这不是残缺，而是安全的默认——没有界面点头，就不该改用户的东西。
func New(client llm.Client, registry *tools.Registry, guard *filesystem.Guard,
	perm tools.PermissionRequester, system string) AgentCore {
	return &Core{state: appstate.New(appstate.Deps{
		Client: client,
		Tools:  registry,
		Guard:  guard,
		Perm:   perm,
		System: system,
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
