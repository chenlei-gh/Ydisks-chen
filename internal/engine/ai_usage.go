package engine

import (
	"context"
	"net/url"
	"strings"
	"time"

	"xianyu-go/internal/db"
)

// aiProviderName 只提取模型服务地址的主机名，不持久化完整 URL、路径或凭证。
func aiProviderName(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// aiUsageErrorKind 将请求失败归类为有限标签，避免把供应商错误正文或敏感信息写入用量账本。
func aiUsageErrorKind(ctx context.Context, requestErr error) string {
	if ctx.Err() == context.Canceled {
		return "cancelled"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "deadline"
	}
	if requestErr != nil {
		return "request_error"
	}
	return "unknown"
}

// recordAIUsage 尽力记录模型调用用量；用量写入失败不得改变 AI 回复主流程结果。
func (a *AIReplierImpl) recordAIUsage(ctx context.Context, record db.AIUsageRecord) {
	if a == nil || a.store == nil || a.store.AIUsage == nil {
		return
	}
	// recordCtx 与原请求取消信号脱钩，确保已发生的失败调用仍有机会留下用量记录。
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := a.store.AIUsage.Record(recordCtx, record); err != nil {
		a.logger.Warn("记录 AI Token 用量失败", "model", record.Model, "task", record.TaskType, "error", err)
	}
}
