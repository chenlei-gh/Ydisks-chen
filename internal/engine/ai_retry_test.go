package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// aiRoundTripFunc 让测试用函数实现 http.RoundTripper。
type aiRoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 执行测试定义的 HTTP 请求行为。
func (roundTrip aiRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

// newAIRequest 创建带可重放请求体的 AI 测试请求。
func newAIRequest(ctx context.Context) *http.Request {
	// request 是包含固定请求体的 POST 请求，http.NewRequest 会提供 GetBody。
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://ai.example.test/v1/chat/completions", strings.NewReader("payload"))
	return request
}

// newAIResponse 创建带状态码、响应头和响应体的 AI 测试响应。
func newAIResponse(status int, retryAfter, body string) *http.Response {
	// headers 保存测试响应需要验证的 Retry-After 值。
	headers := make(http.Header)
	if retryAfter != "" {
		headers.Set("Retry-After", retryAfter)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestAIRetryRetriesTemporaryStatusesOnce 验证临时 HTTP 状态最多触发一次重试且请求体可完整重放。
func TestAIRetryRetriesTemporaryStatusesOnce(t *testing.T) {
	// status 是本轮要覆盖的临时 HTTP 状态码。
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// calls 记录底层传输实际收到的请求次数。
			calls := 0
			// transport 模拟第一次临时失败、第二次成功的上游服务。
			transport := aiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				// body 是当前尝试实际发送的请求内容。
				body, readErr := io.ReadAll(request.Body)
				if readErr != nil || string(body) != "payload" {
					t.Fatalf("请求体未能完整重放: body=%q err=%v", body, readErr)
				}
				if calls == 1 {
					return newAIResponse(status, "0", "temporary failure"), nil
				}
				return newAIResponse(http.StatusOK, "", "success"), nil
			})
			// response、requestErr 是有限重试后的最终结果。
			response, requestErr := (&aiRetryTransport{base: transport}).RoundTrip(newAIRequest(context.Background()))
			if requestErr != nil || response == nil || response.StatusCode != http.StatusOK {
				t.Fatalf("临时状态重试后应成功: response=%+v err=%v", response, requestErr)
			}
			if calls != 2 {
				t.Fatalf("最多应调用两次，实际调用 %d 次", calls)
			}
			_ = response.Body.Close()
		})
	}
}

// TestAIRetryRetriesTransientNetworkError 验证连接层瞬时网络错误只重试一次。
func TestAIRetryRetriesTransientNetworkError(t *testing.T) {
	// calls 记录网络请求次数。
	calls := 0
	// transport 模拟首次连接重置、随后成功。
	transport := aiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection reset")}
		}
		return newAIResponse(http.StatusOK, "", "success"), nil
	})
	// response、requestErr 保存重试结果。
	response, requestErr := (&aiRetryTransport{base: transport}).RoundTrip(newAIRequest(context.Background()))
	if requestErr != nil || response == nil || response.StatusCode != http.StatusOK || calls != 2 {
		t.Fatalf("瞬时网络错误应重试一次: response=%+v err=%v calls=%d", response, requestErr, calls)
	}
	_ = response.Body.Close()
}

// TestAIRetryDoesNotRetryQuotaOrAuthenticationFailures 验证额度、认证和非临时错误不会重试。
func TestAIRetryDoesNotRetryQuotaOrAuthenticationFailures(t *testing.T) {
	// testCase 是需要验证不重试的错误类别及响应内容。
	for _, testCase := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "quota", status: http.StatusTooManyRequests, body: `{"error":{"code":"insufficient_quota"}}`},
		{name: "unauthorized", status: http.StatusUnauthorized, body: "invalid api key"},
		{name: "forbidden", status: http.StatusForbidden, body: "permission denied"},
		{name: "payment required", status: http.StatusPaymentRequired, body: "billing required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// calls 记录不可重试错误的底层请求次数。
			calls := 0
			// transport 始终返回当前测试用例指定的错误。
			transport := aiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				return newAIResponse(testCase.status, "0", testCase.body), nil
			})
			// response、requestErr 保存不重试时的原始响应。
			response, requestErr := (&aiRetryTransport{base: transport}).RoundTrip(newAIRequest(context.Background()))
			if requestErr != nil || response == nil || response.StatusCode != testCase.status {
				t.Fatalf("不可重试错误应保留原响应: response=%+v err=%v", response, requestErr)
			}
			if calls != 1 {
				t.Fatalf("不可重试错误不应再次请求，实际 %d 次", calls)
			}
			// body 验证额度识别过程没有消费 SDK 需要读取的响应体。
			body, readErr := io.ReadAll(response.Body)
			if readErr != nil || string(body) != testCase.body {
				t.Fatalf("原始错误响应体应保持可读: body=%q err=%v", body, readErr)
			}
			_ = response.Body.Close()
		})
	}
}

// TestAIRetryHonorsRetryAfter 验证 Retry-After 秒数和 HTTP 日期格式。
func TestAIRetryHonorsRetryAfter(t *testing.T) {
	// now 是解析 HTTP 日期时使用的固定参考时间。
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	// got 是整数秒 Retry-After 解析后的等待时间。
	if got := aiRetryDelayAt("3", now); got != 3*time.Second {
		t.Fatalf("秒数 Retry-After=%s，期望 3s", got)
	}
	// got 是 HTTP 日期 Retry-After 解析后的等待时间。
	if got := aiRetryDelayAt(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("日期 Retry-After=%s，期望 5s", got)
	}
	// got 是无效 Retry-After 使用的默认退避时间。
	if got := aiRetryDelayAt("invalid", now); got != 250*time.Millisecond {
		t.Fatalf("无效 Retry-After 应使用短退避，实际 %s", got)
	}
}

// TestAIRetryStopsWhenContextIsCancelled 验证等待重试时用户取消会立即终止且不再发送请求。
func TestAIRetryStopsWhenContextIsCancelled(t *testing.T) {
	// ctx 和 cancel 控制重试等待期间的用户取消。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// calls 记录底层请求次数。
	calls := 0
	// transport 在首次返回临时错误前取消请求上下文。
	transport := aiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return newAIResponse(http.StatusServiceUnavailable, "5", "temporary failure"), nil
	})
	// response、requestErr 保存取消后的结果。
	response, requestErr := (&aiRetryTransport{base: transport}).RoundTrip(newAIRequest(ctx))
	if !errors.Is(requestErr, context.Canceled) || response != nil {
		t.Fatalf("取消后应立即返回 context.Canceled: response=%+v err=%v", response, requestErr)
	}
	if calls != 1 {
		t.Fatalf("取消后不应发起第二次请求，实际 %d 次", calls)
	}
}

// TestAIRetryDoesNotReplayUnrewindableRequest 验证没有 GetBody 的请求不会被重放。
func TestAIRetryDoesNotReplayUnrewindableRequest(t *testing.T) {
	// calls 记录底层请求次数。
	calls := 0
	// transport 返回临时状态码。
	transport := aiRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return newAIResponse(http.StatusServiceUnavailable, "0", "temporary failure"), nil
	})
	// request 是显式设置为不可重放 body 的请求。
	request := newAIRequest(context.Background())
	request.GetBody = nil
	// response、requestErr 保存原始临时失败。
	response, requestErr := (&aiRetryTransport{base: transport}).RoundTrip(request)
	if requestErr != nil || response == nil || response.StatusCode != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("不可重放请求应保留原响应且不重试: response=%+v err=%v calls=%d", response, requestErr, calls)
	}
	_ = response.Body.Close()
}
