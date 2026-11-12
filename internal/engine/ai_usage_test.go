package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAIProviderNameStoresOnlyHost 验证用量账本只使用模型服务主机名，不保留 URL 路径或查询参数。
func TestAIProviderNameStoresOnlyHost(t *testing.T) {
	// got 是包含路径和查询参数的模型服务地址解析出的主机名。
	if got := aiProviderName("https://api.example.com/v1/chat/completions?key=not-stored"); got != "api.example.com" {
		t.Fatalf("provider host=%q", got)
	}
	// got 是非法模型服务地址解析出的主机名结果。
	if got := aiProviderName("://invalid"); got != "" {
		t.Fatalf("invalid provider should be empty, got %q", got)
	}
}

// TestAIUsageErrorKindUsesBoundedCategories 验证失败原因只归类为有限标签，不泄露原始错误文本。
func TestAIUsageErrorKindUsesBoundedCategories(t *testing.T) {
	// cancelledCtx 和 cancel 分别保存可取消测试上下文及其取消函数。
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	// got 是已取消请求被归类后的有限错误标签。
	if got := aiUsageErrorKind(cancelledCtx, errors.New("secret provider response")); got != "cancelled" {
		t.Fatalf("cancelled error kind=%q", got)
	}
	// deadlineCtx 和 cancelDeadline 分别保存已超时测试上下文及其清理函数。
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	// got 是超时请求被归类后的有限错误标签。
	if got := aiUsageErrorKind(deadlineCtx, errors.New("secret provider response")); got != "deadline" {
		t.Fatalf("deadline error kind=%q", got)
	}
	// got 是普通请求错误被归类后的有限错误标签。
	if got := aiUsageErrorKind(context.Background(), errors.New("secret provider response")); got != "request_error" {
		t.Fatalf("generic error kind=%q", got)
	}
}
