package agentcore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/sy665/sycode/internal/tools"
)

// PermissionRequest 与 Decision 是 tools 类型的别名。
//
// 界面只 import agentcore，不必为了读弹窗字段去 import tools；
// 也不另立一套同构类型——每多一次转换，就多一处走样的机会。
type (
	PermissionRequest = tools.PermissionRequest
	Decision          = tools.Decision
)

const (
	DecisionDeny         = tools.DecisionDeny
	DecisionAllowOnce    = tools.DecisionAllowOnce
	DecisionAllowSession = tools.DecisionAllowSession
)

// PermissionPrompt 是权限询问的双向通路：工具侧阻塞地问，界面侧异步地答。
//
// 为什么不在 AgentCore 接口上加方法：那要求 Core 反过来持有界面才能把问题送出去，
// 而 main 必须先建 Core 再建界面，接线就成了环。一对 channel 把两侧解耦，
// 装配顺序不再重要，AgentCore 的契约也不必为一个 UI 能力而变宽。
//
// 协议：同一时刻只有一条询问在路上——回合是串行的，工具逐个执行。
// 界面读到请求后必须作答，否则工具侧会一直挂着（等到 ctx 取消为止）。
type PermissionPrompt struct {
	requests chan PermissionRequest

	mu      sync.Mutex
	reply   chan Decision
	session map[string]bool
}

// NewPermissionPrompt 建一条通路。不做 IO，也不启动 goroutine。
func NewPermissionPrompt() *PermissionPrompt {
	return &PermissionPrompt{
		requests: make(chan PermissionRequest),
		session:  make(map[string]bool),
	}
}

// RequestPermission 实现 tools.PermissionRequester：把请求交给界面，阻塞到有答案。
//
// 返回值语义：ctx 取消时返回 DecisionDeny 加错误，调用方据此把"没等到答复"与
// "用户明确拒绝"区分开——前者是中断，后者是个决定，回给模型的文案不一样。
func (p *PermissionPrompt) RequestPermission(ctx context.Context, req PermissionRequest) (Decision, error) {
	key := sessionKey(req)

	p.mu.Lock()
	if p.session[key] {
		p.mu.Unlock()
		// 命中"本会话总是允许"：不再打扰，直接放行。
		return DecisionAllowSession, nil
	}
	if p.reply != nil {
		p.mu.Unlock()
		return DecisionDeny, errors.New("执行失败：同时出现了两个权限询问，工具本应串行执行")
	}
	reply := make(chan Decision, 1)
	p.reply = reply
	p.mu.Unlock()

	// 没有界面（headless、测试）时这一步会一直等到 ctx 取消：
	// 这正是"没人点头就不动"的正确表现。
	select {
	case p.requests <- req:
	case <-ctx.Done():
		p.forget(reply)
		return DecisionDeny, ctx.Err()
	}

	select {
	case decision := <-reply:
		p.forget(reply)
		if decision == DecisionAllowSession {
			p.mu.Lock()
			p.session[key] = true
			p.mu.Unlock()
		}
		return decision, nil
	case <-ctx.Done():
		p.forget(reply)
		return DecisionDeny, ctx.Err()
	}
}

// Requests 供界面等待下一次询问。
//
// 界面拿到请求后不要重复等待：先作答（Answer），再回来等下一个，
// 否则两个 goroutine 会争抢同一个通道，请求可能落到不显示弹窗的那个身上。
func (p *PermissionPrompt) Requests() <-chan PermissionRequest { return p.requests }

// Answer 记录界面给出的决定，返回是否有待答的请求。
//
// 重复调用是安全的：没有待答请求时是空操作，不会让下一次询问收到上一次的答案。
func (p *PermissionPrompt) Answer(decision Decision) bool {
	p.mu.Lock()
	reply := p.reply
	p.reply = nil
	p.mu.Unlock()

	if reply == nil {
		return false
	}
	// 容量 1 且只可能有一个读者，写入不会阻塞。
	reply <- decision
	return true
}

// forget 清掉等待槽，但只在它仍属于自己时清：别的请求可能已经把槽换掉了。
func (p *PermissionPrompt) forget(reply chan Decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reply == reply {
		p.reply = nil
	}
}

// sessionKey 是"本会话总是允许"的缓存键。
//
// 按目录而不是按文件：一次任务常常要连着改同目录下好几个文件，
// 逐个弹窗只会把用户训练成闭眼点允许——那比不问更糟。
// exec 类请求的路径固定是 "."，于是"允许执行"一次即覆盖本会话的所有命令，
// 弹窗文案必须把这个范围说清楚，不能让人以为是只放行这一条命令。
func sessionKey(req PermissionRequest) string {
	dir := req.Path
	if dir != "" {
		dir = filepath.Dir(dir)
	}
	return req.Tool + "|" + req.Op + "|" + dir
}
