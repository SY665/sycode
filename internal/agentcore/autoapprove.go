package agentcore

import (
	"context"

	"github.com/sy665/sycode/internal/tools"
)

// AutoApprove 是"无人值守"的权限通道：一律放行。
//
// 只在 headless 显式要求时使用（`sycode run --allow-write`）——交互界面永远走
// `PermissionPrompt`，让人点头。这里刻意做成一个空结构体而不是开关字段：
// 需要放行的地方显式构造它，看代码的人一眼能看出"这一段没有人在把关"。
//
// 注意它**不能**绕过硬拒绝：`askAccess` 先问 `Guard`，Denied 的操作（系统路径、
// `.git/`、家目录隐藏项、被忽略的文件）根本不会走到这里。所以放行的只是
// "本来要问用户"的那一类，不是"一切"。
type AutoApprove struct{}

// RequestPermission 实现 tools.PermissionRequester。
func (AutoApprove) RequestPermission(context.Context, tools.PermissionRequest) (tools.Decision, error) {
	return tools.DecisionAllowOnce, nil
}
