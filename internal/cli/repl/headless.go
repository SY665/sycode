package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/llm"
)

// headless 单回合执行：没有界面，结果直接写 stdout。
//
// 与 TUI 的差别只在"谁来读事件"：同一个 AgentCore、同一条事件流，
// 这里把正文直接转发到输出，把工具结果攒成摘要。
//
// 不做会话恢复、也不落盘：`sycode run` 是"执行一次就结束"的语义，
// 每次都留一个会话会让 `/sessions` 里堆满两三条事件的一次性记录。
// 需要留痕时用交互界面。

// HeadlessFormat 是输出格式。
type HeadlessFormat string

const (
	// FormatText 只输出正文，边生成边写（脚本里能直接当管道用）。
	FormatText HeadlessFormat = "text"
	// FormatJSON 输出一个对象：正文、工具摘要、用量。
	FormatJSON HeadlessFormat = "json"
)

// HeadlessOptions 是一次无界面执行的参数。
type HeadlessOptions struct {
	Prompt string
	Format HeadlessFormat
	Out    io.Writer
}

// HeadlessTool 是一次工具调用的摘要，进 JSON 的 tools 数组。
type HeadlessTool struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary,omitempty"`
}

// HeadlessUsage 是供应商上报的用量。
type HeadlessUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// HeadlessResult 是 JSON 格式的输出。
type HeadlessResult struct {
	Text  string         `json:"text"`
	Tools []HeadlessTool `json:"tools,omitempty"`
	Usage *HeadlessUsage `json:"usage,omitempty"`
}

// ParseFormat 校验并归一化输出格式。
//
// 返回的错误**不带**"参数错误："前缀：它在命令行里是用法错误，前缀由那一层统一加，
// 这里再带一遍就会打印成"参数错误：参数错误：…"。
func ParseFormat(raw string) (HeadlessFormat, error) {
	switch HeadlessFormat(raw) {
	case "", FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("不支持的输出格式 %q（可选 text / json）", raw)
	}
}

// RunHeadless 执行一个回合并把结果写出去。
//
// 返回的错误只用于"连结果都拿不到"；模型侧的失败会以 error 返回，但**已经产出的正文
// 仍然写出去**——脚本拿到半句话，好过拿到一片空白却不知道为什么。
func RunHeadless(ctx context.Context, core agentcore.AgentCore, opts HeadlessOptions) (HeadlessResult, error) {
	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		return HeadlessResult{}, errors.New("参数错误：提示词为空")
	}
	if core == nil {
		return HeadlessResult{}, errors.New("执行失败：没有可用的模型客户端（请检查配置与密钥）")
	}

	stream, err := core.Submit(ctx, prompt)
	if err != nil {
		return HeadlessResult{}, err
	}

	var (
		text  strings.Builder
		tools []HeadlessTool
		usage *HeadlessUsage
	)

	for event := range stream {
		switch event.Type {
		case llm.EventText:
			text.WriteString(event.Text)
			if opts.Format == FormatText {
				// 边生成边写：脚本能更早拿到第一段，不必等整句结束。
				_, _ = io.WriteString(opts.Out, event.Text)
			}

		case llm.EventToolResult:
			if event.Tool != nil {
				tools = append(tools, HeadlessTool{
					Name:    event.Tool.Name,
					OK:      event.Err == nil,
					Summary: firstLine(event.Text),
				})
			}

		case llm.EventUsage:
			if event.Usage != nil {
				usage = &HeadlessUsage{
					PromptTokens:     event.Usage.PromptTokens,
					CompletionTokens: event.Usage.CompletionTokens,
					TotalTokens:      event.Usage.TotalTokens,
				}
			}

		case llm.EventError:
			// 先把已有的正文交给调用方，再报错：半句话也比空输出有用。
			result := HeadlessResult{Text: text.String(), Tools: tools, Usage: usage}
			if opts.Format == FormatJSON {
				writeHeadlessJSON(opts.Out, result)
			} else if text.Len() > 0 {
				_, _ = io.WriteString(opts.Out, "\n")
			}
			return result, event.Err
		}
	}

	result := HeadlessResult{Text: text.String(), Tools: tools, Usage: usage}
	if opts.Format == FormatJSON {
		writeHeadlessJSON(opts.Out, result)
		return result, nil
	}

	// 正文已经逐字写出去了，这里只补一个换行；没有正文时也补，免得 shell 提示符
	// 粘在同一行上。
	_, _ = io.WriteString(opts.Out, "\n")
	return result, nil
}

// writeHeadlessJSON 输出一个 JSON 对象。
//
// 写失败无处可报（stdout 已经坏了），所以忽略错误——调用方的下一层会看到管道断开。
func writeHeadlessJSON(out io.Writer, result HeadlessResult) {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(result)
}

// firstLine 取多行文本的第一行：工具摘要的第二行起是给人当场看的展示块。
func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}
