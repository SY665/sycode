package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestTranslateHTTPStatusHints(t *testing.T) {
	// 每种状态码都要给出"下一步该干什么"，而不是照抄英文。
	cases := []struct {
		status   int
		wantHint string
	}{
		{401, "密钥"},
		{403, "密钥"},
		{404, "base_url"},
		{429, "稍后重试"},
		{500, "服务端故障"},
		{503, "服务端故障"},
		{400, "请求参数"},
	}

	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			msg := Translate(&httpStatusError{Status: c.status, Code: "some_code", Body: "boom"}).Error()

			if !strings.HasPrefix(msg, "执行失败：") {
				t.Errorf("应带「执行失败：」前缀：%s", msg)
			}
			if !strings.Contains(msg, fmt.Sprint(c.status)) {
				t.Errorf("应保留状态码：%s", msg)
			}
			if !strings.Contains(msg, c.wantHint) {
				t.Errorf("缺少指引 %q：%s", c.wantHint, msg)
			}
		})
	}
}

func TestHTTPStatusErrorDeduplicatesCodeAndBody(t *testing.T) {
	// 不少兼容端点把 code 与 message 填成一样，重复两遍会让用户以为有两个问题。
	msg := (&httpStatusError{Status: 400, Code: "invalid_request", Body: "invalid_request"}).Error()
	if strings.Count(msg, "invalid_request") != 1 {
		t.Errorf("code 与 body 相同时应只出现一次：%s", msg)
	}
}

func TestTranslateContextErrors(t *testing.T) {
	canceled := Translate(fmt.Errorf("stream: %w", context.Canceled))
	if !strings.HasPrefix(canceled.Error(), "已中断：") {
		t.Errorf("取消应译为「已中断」：%v", canceled)
	}
	if !errors.Is(canceled, context.Canceled) {
		t.Error("取消的错误链不能断")
	}

	timeout := Translate(context.DeadlineExceeded)
	if !strings.HasPrefix(timeout.Error(), "超时：") {
		t.Errorf("超时应译为「超时」：%v", timeout)
	}
}

func TestTranslateConnectionError(t *testing.T) {
	// SDK 把连接失败包成 *url.Error；必须排在 context 判断之后，
	// 否则用户的主动中断会被说成"连不上"。
	err := &url.Error{Op: "Post", URL: "https://api.openai.com/v1/chat/completions", Err: errors.New("dial tcp: connection refused")}

	msg := Translate(err).Error()
	for _, want := range []string{"无法连接模型服务", "connection refused", "base_url"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误文案缺少 %q：%s", want, msg)
		}
	}
}

func TestTranslateNil(t *testing.T) {
	if got := Translate(nil); got != nil {
		t.Errorf("nil 应原样返回 nil，实际 %v", got)
	}
}

func TestTranslateIsIdempotent(t *testing.T) {
	// 已经是本包产出的错误不能再翻一层，否则界面上会出现"执行失败：执行失败："。
	first := Translate(&httpStatusError{Status: 500, Code: "server_error", Body: "boom"})
	second := Translate(first)
	if second.Error() != first.Error() {
		t.Errorf("重复翻译改变了文案：%q → %q", first, second)
	}

	var statusErr *httpStatusError
	if !errors.As(second, &statusErr) || statusErr.Status != 500 {
		t.Errorf("翻译后应保持结构化错误，实际 %v", second)
	}
}

func TestTranslateKeepsUnknownErrorsWrapped(t *testing.T) {
	sentinel := errors.New("json: cannot unmarshal")
	got := Translate(sentinel)

	if !errors.Is(got, sentinel) {
		t.Error("未知错误也要用 %w 保留链，否则上层无法判定")
	}
	if !strings.HasPrefix(got.Error(), "执行失败：") {
		t.Errorf("未知错误也应带中文前缀：%v", got)
	}
}
