// Package tools 是模型能作用于真实世界的唯一手段。
//
// 每个工具只做五件事：报名字、报说明、报参数 schema、校验参数、执行并返回结果。
// 权限判定不在这里（ADR-005）：工具查询 filesystem.Guard，由它决定能不能碰。
// 也不要 import llm——schema 的两个类型是独立的，转换在 appstate 做。
package tools

import (
	"context"
	"unicode/utf8"
)

// maxInlineBytes 是回给模型的单条结果上限。
//
// 32KiB 是权衡：模型读一整段 200 行的代码足够，而一次灌进 200KiB 会把上下文窗口吃掉大半。
// 超限内容由具体工具落盘（写进 ~/.sycode/bash 之类的目录）并在提示里给出路径。
const maxInlineBytes = 32 * 1024

// Input 是工具参数的统一形态：直接对应 JSON Schema 里的 properties。
type Input = map[string]any

// DisplayKind 标识结构化展示的类型，供 UI 选择渲染方式。
type DisplayKind string

const (
	// DisplayDiff 用于改文件的工具：UI 渲染色块差异。
	DisplayDiff DisplayKind = "diff"
	// DisplayList 用于检索类工具：UI 渲染成列表（文件、匹配行）。
	DisplayList DisplayKind = "list"
)

// Display 是给 UI 的结构化展示，不参与回给模型的文本。
//
// Content 决定模型看到什么，Display 决定人看到什么：读文件时人想看高亮与行号，
// 而模型只需要原文。两者分开，避免为了界面好看去污染给模型的文本。
type Display struct {
	Kind  DisplayKind
	Title string
	Lines []string
}

// Result 是一次工具执行的结果。
type Result struct {
	// Content 回给模型。超过 maxInlineBytes 时必须截断并说明，不能原样塞进上下文。
	Content string
	// Display 可选，给 UI。
	Display *Display
	// Artifact 是超限内容落盘后的路径；没有落盘时为空。
	Artifact string
}

// Schema 是发给模型的工具说明。
//
// 与 llm.ToolSchema 形状相同但是独立类型：tools 不 import llm，
// 于是模型层加字段不会牵动工具层，转换在 appstate 组装请求时做一次。
type Schema struct {
	Name        string
	Description string
	// Input 是 JSON Schema 的 properties 部分（不含 type: object 外壳），
	// 由 llm 侧补外壳：这样工具作者不必重复写样板。
	Input map[string]any
}

// Tool 是工具契约。恰好这 5 个方法，不允许加 BeforeExecute/PostHook 之类钩子——要加就走 ADR。
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, in Input) (Result, error)
}

// Validator 是可选能力：实现它的工具会在 Execute 之前被注册表统一校验。
//
// 单独成接口而不是塞进 Tool：读文件之类的工具没有可校验的参数，
// 强迫它们写一个空实现只会增加噪音。
type Validator interface {
	ValidateInput(ctx context.Context, in Input) error
}

// PermissionRequest 是"这件事要不要人点头"的描述。
//
// 字段刻意少：弹窗里能显示的信息有限，把整个 Input 塞进去用户也读不完。
type PermissionRequest struct {
	Tool string
	Op   string // read / write / exec，与 filesystem.Op 取值一致
	Path string
	// Detail 是一句人话："把 3 行替换成 5 行"、"运行 go build ./..."。
	Detail string
}

// Decision 是用户对一次权限询问的回答。
type Decision int

const (
	// DecisionDeny 拒绝：调用方必须保证副作用为零。
	DecisionDeny Decision = iota
	// DecisionAllowOnce 只允许这一次。
	DecisionAllowOnce
	// DecisionAllowSession 本会话内同类操作不再询问。
	DecisionAllowSession
)

// Allowed 报告这次决定是否放行。
func (d Decision) Allowed() bool { return d == DecisionAllowOnce || d == DecisionAllowSession }

// PermissionRequester 由 UI 实现并注入给工具。
type PermissionRequester interface {
	RequestPermission(ctx context.Context, req PermissionRequest) (Decision, error)
}

// clip 把内容截断到 maxInlineBytes，返回截断后的文本与"是否发生了截断"。
//
// 按字节限流但按 rune 收尾：中文一个字 3 字节，直接切会留下半个字变成 U+FFFD，
// 模型读到替换符只会困惑。落盘由调用方做——不同工具的超限提示不一样
// （bash 说"输出过长"，read 说"文件过大"），统一处理反而说不清。
func clip(content string) (string, bool) {
	if len(content) <= maxInlineBytes {
		return content, false
	}
	cut := maxInlineBytes
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	// 尽量切在行边界上：半行代码比整行缺失更难读。
	if idx := lastNewline(content[:cut]); idx > 0 {
		cut = idx + 1
	}
	return content[:cut], true
}

// lastNewline 返回 s 中最后一个换行符的下标；没有则返回 -1。
func lastNewline(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}
