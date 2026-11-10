package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAIProviderNameStoresOnlyHost 验证用量账本只使用模型服务主机名，不保留 URL 路径或查询参数。
func TestAIProviderNameStoresOnlyHost(t *testing.T) {
	if got := aiProviderName("https://api.example.com/v1/chat/completions?key=not-stored"); got != "api.example.com" {
		t.Fatalf("provider host=%q", got)
	}
	if got := aiProviderName("://invalid"); got != "" {
		t.Fatalf("invalid provider should be empty, got %q", got)
	}
}

// TestAIUsageErrorKindUsesBoundedCategories 验证失败原因只归类为有限标签，不泄露原始错误文本。
func TestAIUsageErrorKindUsesBoundedCategories(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := aiUsageErrorKind(cancelledCtx, errors.New("secret provider response")); got != "cancelled" {
		t.Fatalf("cancelled error kind=%q", got)
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if got := aiUsageErrorKind(deadlineCtx, errors.New("secret provider response")); got != "deadline" {
		t.Fatalf("deadline error kind=%q", got)
	}
	if got := aiUsageErrorKind(context.Background(), errors.New("secret provider response")); got != "request_error" {
		t.Fatalf("generic error kind=%q", got)
	}
}
