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
	response, requestErr := t.base.RoundTrip(request)
	if request.Context().Err() != nil || !isRetryableAIResponse(response, requestErr) {
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
	for _, marker := range []string{"insufficient_quota", "billing_hard_limit_reached", "quota_exceeded", "billing_not_active", "exceeded your current quota"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// aiRetryDelay 返回 Retry-After 指定的等待时间；缺少或无效时使用短退避。
func aiRetryDelay(response *http.Response) time.Duration {
	const fallbackDelay = 250 * time.Millisecond
	if response == nil {
		return fallbackDelay
	}
	value := strings.TrimSpace(response.Header.Get("Retry-After"))
	if value == "" {
		return fallbackDelay
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		delay := time.Until(retryAt)
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
