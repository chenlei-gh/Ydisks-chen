package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// aiRetryTransport 在单次 AI 请求的总上下文期限内最多重试一次瞬时失败。
type aiRetryTransport struct {
	base http.RoundTripper
}

// RoundTrip 仅重试可安全重放的请求体，并保留原始响应供 SDK 正常解析。
func (t *aiRetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// response、requestErr 保存首次传输得到的响应或网络错误。
	response, requestErr := t.base.RoundTrip(request)
	// contextErr 表示首次传输期间用户是否已取消或触发总请求 deadline。
	if contextErr := request.Context().Err(); contextErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, contextErr
	}
	if !isRetryableAIResponse(response, requestErr) {
		return response, requestErr
	}
	if response != nil && response.StatusCode == http.StatusTooManyRequests && isAIQuotaFailure(response) {
		return response, requestErr
	}
	if request.GetBody == nil {
		return response, requestErr
	}

	// retryBody 是重新创建的请求体，避免重试时发送已读完的 body。
	retryBody, bodyErr := request.GetBody()
	if bodyErr != nil {
		return response, requestErr
	}
	// delay 是按 Retry-After 或短退避策略计算的等待时间。
	delay := aiRetryDelay(response)
	// deadline 是原始 AI 请求的总时限；剩余时间不足时保留原响应，不再等待重试。
	if deadline, hasDeadline := request.Context().Deadline(); hasDeadline && time.Until(deadline) <= delay {
		return response, requestErr
	}
	// waitErr 表示等待退避期间是否收到取消或 deadline 信号。
	if waitErr := waitForAIRetry(request.Context(), delay); waitErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, waitErr
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	// retryRequest 复制原请求的 headers、URL 和 context，仅替换可重放的 body。
	retryRequest := request.Clone(request.Context())
	retryRequest.Body = retryBody
	return t.base.RoundTrip(retryRequest)
}

// isRetryableAIResponse 只允许网络瞬时错误及明确的临时服务端状态进入一次重试。
func isRetryableAIResponse(response *http.Response, requestErr error) bool {
	if requestErr != nil {
		if errors.Is(requestErr, context.Canceled) || errors.Is(requestErr, context.DeadlineExceeded) {
			return false
		}
		// networkErr 表示底层连接层报告的可恢复网络错误。
		var networkErr net.Error
		return errors.As(requestErr, &networkErr) || errors.Is(requestErr, io.EOF) || errors.Is(requestErr, io.ErrUnexpectedEOF)
	}
	if response == nil {
		return false
	}
	switch response.StatusCode {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// isAIQuotaFailure 检查 429 响应中的明确额度/账单错误，避免对不可恢复问题重复计费。
func isAIQuotaFailure(response *http.Response) bool {
	if response == nil || response.Body == nil {
		return false
	}
	// originalBody 是 SDK 后续仍需读取的完整响应体。
	originalBody := response.Body
	// preview 只读取有限长度用于识别额度错误，剩余字节仍保留给 SDK。
	preview, _ := io.ReadAll(io.LimitReader(originalBody, 64*1024))
	response.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(preview), originalBody), Closer: originalBody}
	// message 是错误响应的小写文本，仅用于匹配明确的额度错误标记。
	message := strings.ToLower(string(preview))
	// marker 是需要阻止重试的额度或账单错误代码。
	for _, marker := range []string{"insufficient_quota", "billing_hard_limit_reached", "quota_exceeded", "billing_not_active", "exceeded your current quota"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// aiRetryDelay 返回 Retry-After 指定的等待时间；缺少或无效时使用短退避。
func aiRetryDelay(response *http.Response) time.Duration {
	if response == nil {
		return 250 * time.Millisecond
	}
	return aiRetryDelayAt(response.Header.Get("Retry-After"), time.Now())
}

// aiRetryDelayAt 以给定时钟解析 Retry-After，便于确定性测试秒数和 HTTP 日期。
func aiRetryDelayAt(value string, now time.Time) time.Duration {
	// fallbackDelay 是服务端未提供 Retry-After 时使用的短等待时间。
	const fallbackDelay = 250 * time.Millisecond
	value = strings.TrimSpace(value)
	if value == "" {
		return fallbackDelay
	}
	// seconds、parseErr 保存 Retry-After 的整数秒数及解析错误。
	seconds, parseErr := strconv.ParseInt(value, 10, 64)
	if parseErr == nil && seconds >= 0 {
		// maxSeconds 防止异常大的 Retry-After 数值导致 duration 溢出。
		const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
		if seconds > maxSeconds {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	// retryAt、dateErr 保存 Retry-After 的 HTTP 日期及解析错误。
	retryAt, dateErr := http.ParseTime(value)
	if dateErr == nil {
		// delay 是服务端指定重试时间相对当前时钟的间隔。
		delay := retryAt.Sub(now)
		if delay > 0 {
			return delay
		}
		return 0
	}
	return fallbackDelay
}

// waitForAIRetry 等待重试窗口，同时立即响应用户取消或总请求 deadline。
func waitForAIRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	// timer 在等待结束或上下文取消时释放。
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
