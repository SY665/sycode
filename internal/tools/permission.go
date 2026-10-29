package tools

import (
	"context"
	"fmt"

	"github.com/sy665/sycode/internal/filesystem"
)

// access 描述一次"要碰某个路径"的请求。
//
// 用结构体而不是七个位置参数：调用点写出来自带标签，读代码时不必数到第几个是 op。
type access struct {
	guard  *filesystem.Guard
	perm   PermissionRequester
	tool   string
	path   string
	op     filesystem.Op
	detail string // 给用户看的一句话："读取文件内容"、"替换 3 行为 5 行"
}

// askAccess 走完"问 Guard + 必要时问用户"这一套，返回可直接回给模型的错误。
//
// 所有工具都必须走它，而不是自己去调 guard.Check：权限三态到文案的映射只应该有一处，
// 否则"被拒绝"在某些工具里说成"执行失败"，模型就会以为是临时故障而反复重试。
func askAccess(ctx context.Context, req access) error {
	if req.guard == nil {
		// 装配错误按拒绝处理：没有守卫就没有安全边界，不能放行。
		return fmt.Errorf("权限不足：没有权限守卫，已拒绝访问 %q（内部装配错误）", req.path)
	}

	permission, reason := req.guard.Check(req.path, req.op)
	switch permission {
	case filesystem.Granted:
		return nil

	case filesystem.Denied:
		return fmt.Errorf("权限不足：%s 不允许 %s（%s）", req.path, opName(req.op), reason.Message)

	default: // Pending
		if req.perm == nil {
			return fmt.Errorf("权限不足：%s 需要你确认后才能%s，但当前没有确认通道（%s）。请在交互式终端里运行",
				req.path, opName(req.op), reason.Message)
		}
		decision, err := req.perm.RequestPermission(ctx, PermissionRequest{
			Tool:   req.tool,
			Op:     string(req.op),
			Path:   req.path,
			Detail: req.detail,
		})
		if err != nil {
			return fmt.Errorf("执行失败：等待权限确认时出错：%w", err)
		}
		if !decision.Allowed() {
			// 拒绝要说得像"决定"，不像"故障"，否则模型会换个说法再试一次。
			return fmt.Errorf("权限不足：用户拒绝了对 %s 的%s操作。不要重试同一路径，先问清楚要做什么",
				req.path, opName(req.op))
		}
		return nil
	}
}

// opName 把操作名换成中文动词，拼进错误文案。
func opName(op filesystem.Op) string {
	switch op {
	case filesystem.OpRead:
		return "读取"
	case filesystem.OpWrite:
		return "修改"
	case filesystem.OpExec:
		return "执行"
	default:
		return string(op)
	}
}
