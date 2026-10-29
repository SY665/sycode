package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	oai "github.com/openai/openai-go/v3"
)

// httpStatusError 是"供应商回了 HTTP 错误"的结构化形态。
//
// 保留状态码与供应商的 code/message，供上层判定可重试性与排障；
// 面向用户的文案在 Error() 里统一成中文（CODING-STANDARDS §13 的前缀表）。
type httpStatusError struct {
	Status int
	Code   string
	Body   string

	// cause 是原始错误，供 errors.Is/As 继续追溯（不能让翻译切断错误链）。
	cause error
}

func (e *httpStatusError) Error() string {
	detail := e.Code
	switch {
	case detail == "" && e.Body == "":
		detail = ""
	case detail == "":
		detail = e.Body
	case e.Body == "" || e.Body == e.Code:
		// 不少 OpenAI 兼容端点把 code 与 message 填成一样，重复两遍只会让用户以为有两个问题。
	default:
		detail = e.Code + "：" + e.Body
	}

	msg := fmt.Sprintf("执行失败：模型服务返回 %d", e.Status)
	if detail != "" {
		msg += "（" + detail + "）"
	}
	return msg + "。" + statusHint(e.Status)
}

func (e *httpStatusError) Unwrap() error { return e.cause }

// statusHint 给出"下一步该干什么"。按状态码分类而不是照抄英文原文：
// 用户需要的是动作，不是供应商的错误码语义。
func statusHint(status int) string {
	switch {
	case status == 401 || status == 403:
		return "请检查密钥是否正确、是否有该模型的权限。"
	case status == 404:
		return "请检查配置里的 base_url 与 active_model 是否存在。"
	case status == 429:
		return "请求过于频繁或额度不足，请稍后重试。"
	case status >= 500:
		return "供应商服务端故障，请稍后重试。"
	case status >= 400:
		return "请检查请求参数与模型是否匹配。"
	default:
		return "请稍后重试。"
	}
}

// Translate 把供应商/网络的原始错误翻成中文，并保留错误链。
//
// 只处理"原始错误"：已经是本包产出的错误（如 httpStatusError）原样返回，
// 否则会出现"执行失败：执行失败：…"这种二次包装（CODE-DESIGN §0.1）。
func Translate(err error) error {
	if err == nil {
		return nil
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return err
	}

	// 取消要排在 url.Error 之前判断：SDK 把 ctx 取消包在 url.Error 里，
	// 先匹配 url.Error 会把用户的主动中断说成"连不上"。
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("已中断：%w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("超时：模型服务未在限定时间内响应。请稍后重试，或改用更快的端点（%w）", err)
	}

	var apiErr *oai.Error
	if errors.As(err, &apiErr) {
		return &httpStatusError{
			Status: apiErr.StatusCode,
			Code:   apiErr.Code,
			Body:   apiErr.Message,
			cause:  err,
		}
	}

	// 连不上：DNS、拒绝连接、TLS 握手失败都在这里。附上底层原因，否则用户只看到"连不上"。
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("执行失败：无法连接模型服务（%s）。请检查网络与配置里的 base_url。", urlErr.Err)
	}

	return fmt.Errorf("执行失败：%w", err)
}
