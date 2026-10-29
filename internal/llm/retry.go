package llm

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	oai "github.com/openai/openai-go/v3"
)

const (
	// maxRetries 是首次尝试之外的重试次数。
	// PRD F-LLM-5 要求"指数退避重试 3 次"：总共最多 4 次请求，UI 会看到 3 个 EventRetry。
	maxRetries = 3
)

// backoffBase 是第一次重试前的等待时间，之后逐次翻倍。
//
// 是变量而不是常量：测试要把它压到毫秒级，否则每跑一次重试用例就要真等 1 秒。
// 生产代码里不要改它——要调就是改这里，改完顺手看一眼 retry_test 的边界。
var backoffBase = time.Second

// maxBackoff 是单次等待的上限。指数增长必须封顶，否则第 10 次重试要等 8 分钟。
const maxBackoff = 30 * time.Second

// errStreamTruncated 表示"流在结束信号之前断了"。
//
// 单独成型是为了让它可重试：这是网络层问题，换个连接大概率能成。
// 文案不带"执行失败："前缀——那是 Translate 的活，带两遍会叠成"执行失败：执行失败：…"。
var errStreamTruncated = errors.New("连接在模型给出结束信号之前中断，回答可能不完整。请重试")

// partialError 标记"已经给用户看过内容之后才发生的失败"。
//
// 它包住真实错误（Unwrap 保持错误链，Translate 照常翻译），但会让 isRetryable 返回 false：
// 已经吐过字再重试，用户会看到重复内容，比直接报错更糟。
type partialError struct {
	err error
}

func (e *partialError) Error() string { return e.err.Error() }
func (e *partialError) Unwrap() error { return e.err }

// Do 执行 fn，对可重试错误按指数退避重试，最多 attempts 次（含首次）。
//
// onRetry 在"第 attempt 次尝试失败、即将再试"时调用：attempt 是刚失败的那一次（从 1 开始）。
// 返回最后一次的错误，调用方不必区分"重试用尽"与"不可重试"——两者都该走同一条报错路径。
func Do(ctx context.Context, attempts int, fn func(context.Context) error, onRetry func(attempt int, err error)) error {
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if lastErr = fn(ctx); lastErr == nil {
			return nil
		}
		if attempt == attempts || !isRetryable(lastErr) {
			return lastErr
		}

		if onRetry != nil {
			onRetry(attempt, lastErr)
		}
		if err := sleep(ctx, backoffDelay(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

// isRetryable 判定"再试一次有没有意义"。
//
// 只认基础设施类问题：限流、服务端故障、连接失败、流被截断。
// 参数错、鉴权错、模型不存在这类重试 100 次也还是同样的错，越早报越好。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// 已经吐过字：一律不重试（见 partialError）。
	var partial *partialError
	if errors.As(err, &partial) {
		return false
	}

	// 用户主动中断：不该重试，否则 Esc 按了等于没按。
	if errors.Is(err, context.Canceled) {
		return false
	}
	// 超时通常是本端设定：再试一次仍会超时，只是让用户多等一轮。
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// 流截断：网络抖动，值得再试。
	if errors.Is(err, errStreamTruncated) {
		return true
	}

	// SDK 的 HTTP 错误还没经过 Translate，同样要看状态码。
	// 这条不能省：重试判定发生在翻译之前，少了它 429 会直接失败而不重试。
	var apiErr *oai.Error
	if errors.As(err, &apiErr) {
		return retryableStatus(apiErr.StatusCode)
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return retryableStatus(statusErr.Status)
	}

	// 连接失败（DNS、拒绝连接、TLS）：url.Error 包着底层原因。
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// retryableStatus 判定 HTTP 状态码是否值得重试：限流与服务端故障算，其余 4xx 不算。
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// backoffDelay 计算第 attempt 次失败后的等待时间：base 翻倍，加最多 50% 抖动，封顶 maxBackoff。
//
// 抖动是为了避免多个客户端在同一秒一起重试（雷群效应）。
func backoffDelay(attempt int) time.Duration {
	delay := backoffBase
	for i := 1; i < attempt && delay < maxBackoff; i++ {
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	// attempt 为 1 时抖动为 0：首次重试的等待要可预期，别让用户以为卡住了。
	if attempt <= 1 {
		return delay
	}
	// 抖动不能把总时长顶出上限：否则"封顶 30 秒"会变成 45 秒。
	jitter := time.Duration(rand.Int63n(int64(delay/2) + 1))
	if delay+jitter > maxBackoff {
		return maxBackoff
	}
	return delay + jitter
}

// sleep 等待 d，但可以被 ctx 提前打断。
//
// 用 Timer 而不是 time.After：后者在提前返回时会留下一个到点才回收的定时器。
func sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
