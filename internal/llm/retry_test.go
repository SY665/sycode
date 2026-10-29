package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	oai "github.com/openai/openai-go/v3"
)

// withFastBackoff 把退避压到毫秒级：否则每个重试用例都要真等 1 秒起。
func withFastBackoff(t *testing.T) {
	t.Helper()
	original := backoffBase
	backoffBase = time.Millisecond
	t.Cleanup(func() { backoffBase = original })
}

// countingFn 造一个"按预设结果依次返回"的 fn，并记录调用次数。
func countingFn(errs ...error) (func(context.Context) error, *int) {
	calls := 0
	return func(context.Context) error {
		if calls < len(errs) {
			err := errs[calls]
			calls++
			return err
		}
		calls++
		return nil
	}, &calls
}

func TestBackoffDelay(t *testing.T) {
	original := backoffBase
	backoffBase = 100 * time.Millisecond
	t.Cleanup(func() { backoffBase = original })

	// 首次重试不加抖动：可预期的等待，别让用户以为卡住。
	if got := backoffDelay(1); got != 100*time.Millisecond {
		t.Errorf("第 1 次重试应等于基准值，实际 %v", got)
	}
	// 之后翻倍，并带最多 50% 的抖动。
	for _, c := range []struct {
		attempt   int
		low, high time.Duration
	}{
		{2, 200 * time.Millisecond, 300 * time.Millisecond},
		{3, 400 * time.Millisecond, 600 * time.Millisecond},
	} {
		got := backoffDelay(c.attempt)
		if got < c.low || got > c.high {
			t.Errorf("第 %d 次重试应在 [%v,%v]，实际 %v", c.attempt, c.low, c.high, got)
		}
	}
	// 指数必须封顶，否则第 10 次重试要等 8 分钟。
	if got := backoffDelay(20); got > maxBackoff {
		t.Errorf("等待时间未封顶：%v", got)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"限流可重试", &httpStatusError{Status: 429}, true},
		// 重试判定发生在 Translate 之前，此时拿到的是 SDK 的原始错误。
		{"SDK 原始 429 可重试", &oai.Error{StatusCode: 429}, true},
		{"SDK 原始 400 不重试", &oai.Error{StatusCode: 400}, false},
		{"服务端故障可重试", &httpStatusError{Status: 500}, true},
		{"服务不可用可重试", &httpStatusError{Status: 503}, true},
		{"参数错不重试", &httpStatusError{Status: 400}, false},
		{"鉴权错不重试", &httpStatusError{Status: 401}, false},
		{"模型不存在不重试", &httpStatusError{Status: 404}, false},
		{"流截断可重试", errStreamTruncated, true},
		{"主动中断不重试", context.Canceled, false},
		{"超时不重试", context.DeadlineExceeded, false},
		{"连接失败可重试", &url.Error{Op: "Post", URL: "https://x", Err: errors.New("connection refused")}, true},
		{"未知错误不重试", errors.New("json: cannot unmarshal"), false},
		{"nil 不重试", nil, false},
		// 已经吐过字：哪怕底层可重试，也不能再来一次。
		{"吐字后的 500 不重试", &partialError{err: &httpStatusError{Status: 500}}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryable(c.err); got != c.want {
				t.Errorf("isRetryable 期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestPartialErrorKeepsChain(t *testing.T) {
	inner := &httpStatusError{Status: 503, Code: "overloaded"}
	wrapped := &partialError{err: inner}

	// 包装后 errors.Is 仍能回到真实错误。
	if !errors.Is(wrapped, inner) {
		t.Error("包装后错误链断了")
	}
	// Translate 透过包装认出"已是中文错误"，原样返回——不会再套一层"执行失败：""。
	if got := Translate(wrapped).Error(); got != wrapped.Error() {
		t.Errorf("已经中文化的错误不应被二次包装：%s", got)
	}
	if !strings.Contains(wrapped.Error(), "服务端故障") {
		t.Errorf("应保留按状态码给出的指引：%s", wrapped.Error())
	}
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	withFastBackoff(t)

	fn, calls := countingFn(&httpStatusError{Status: 500}, nil)
	var retried []int
	err := Do(context.Background(), 3, fn, func(attempt int, _ error) {
		retried = append(retried, attempt)
	})

	if err != nil {
		t.Fatalf("第二次成功时不应报错：%v", err)
	}
	if *calls != 2 {
		t.Errorf("应尝试 2 次，实际 %d", *calls)
	}
	if len(retried) != 1 || retried[0] != 1 {
		t.Errorf("回调应报告失败的是第 1 次，实际 %v", retried)
	}
}

func TestDoStopsOnNonRetryable(t *testing.T) {
	withFastBackoff(t)

	fn, calls := countingFn(&httpStatusError{Status: 400})
	retried := 0
	err := Do(context.Background(), 3, fn, func(int, error) { retried++ })

	if err == nil {
		t.Fatal("400 应直接失败")
	}
	if *calls != 1 {
		t.Errorf("不可重试错误只应请求一次，实际 %d", *calls)
	}
	if retried != 0 {
		t.Errorf("不该触发重试回调，实际 %d 次", retried)
	}
}

func TestDoGivesUpAfterAttempts(t *testing.T) {
	withFastBackoff(t)

	fn, calls := countingFn(
		&httpStatusError{Status: 429},
		&httpStatusError{Status: 429},
		&httpStatusError{Status: 429},
	)
	retried := 0
	err := Do(context.Background(), 3, fn, func(int, error) { retried++ })

	if err == nil {
		t.Fatal("全部失败时应返回错误")
	}
	if *calls != 3 {
		t.Errorf("应尝试 3 次，实际 %d", *calls)
	}
	// 只有前两次失败会触发重试；最后一次是"放弃"。
	if retried != 2 {
		t.Errorf("应重试 2 次，实际 %d", retried)
	}
	// 返回的必须是最后一次的错误，不能是第一次的。
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != 429 {
		t.Errorf("应返回最后一次错误，实际 %v", err)
	}
}

func TestDoStopsOnContextCancel(t *testing.T) {
	// 等待期间用户按 Esc：必须立刻返回，不能等退避走完。
	original := backoffBase
	backoffBase = 5 * time.Second
	t.Cleanup(func() { backoffBase = original })

	ctx, cancel := context.WithCancel(context.Background())
	fn, calls := countingFn(&httpStatusError{Status: 500}, nil, nil)

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Do(ctx, 3, fn, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("应返回取消失败，实际 %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("取消后应立即返回，实际等了 %v", elapsed)
	}
	if *calls != 1 {
		t.Errorf("取消后不应再尝试，实际 %d 次", *calls)
	}
}

func TestDoSingleAttempt(t *testing.T) {
	// attempts < 1 也要保证至少执行一次，而不是直接返回 nil。
	fn, calls := countingFn(fmt.Errorf("boom"))
	err := Do(context.Background(), 0, fn, nil)

	if err == nil {
		t.Fatal("应返回错误")
	}
	if *calls != 1 {
		t.Errorf("应至少尝试 1 次，实际 %d", *calls)
	}
}
