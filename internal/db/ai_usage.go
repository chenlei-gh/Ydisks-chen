package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// AIUsageRecord 是一次 AI 模型请求的用量摘要，不包含提示词、响应正文或凭证。
type AIUsageRecord struct {
	CookieID         string
	Provider         string
	Model            string
	TaskType         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	DurationMS       int64
	Status           string
	ErrorKind        string
}

// AIUsageDailySummary 是用户在某日、某模型和任务类型下的聚合用量。
type AIUsageDailySummary struct {
	Date             string
	Provider         string
	Model            string
	TaskType         string
	Calls            int64
	FailedCalls      int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	DurationMS       int64
}

// AIUsage 保存 AI 模型请求的最小用量账本。
type AIUsage struct {
	DB *sql.DB
}

// Record 记录一次模型请求；错误信息只保存受控分类，绝不保存原始错误文本。
func (a *AIUsage) Record(ctx context.Context, record AIUsageRecord) error {
	if a == nil || a.DB == nil {
		return errors.New("AI 用量存储未初始化")
	}
	record.CookieID = strings.TrimSpace(record.CookieID)
	record.Provider = strings.TrimSpace(record.Provider)
	record.Model = strings.TrimSpace(record.Model)
	record.TaskType = strings.TrimSpace(record.TaskType)
	if record.CookieID == "" || record.Model == "" {
		return errors.New("AI 用量记录缺少账号或模型")
	}
	if record.TaskType != "text" && record.TaskType != "vision" {
		return errors.New("AI 用量任务类型无效")
	}
	if record.Status != "success" && record.Status != "failed" {
		return errors.New("AI 用量状态无效")
	}
	if record.PromptTokens < 0 || record.CompletionTokens < 0 || record.TotalTokens < 0 || record.DurationMS < 0 {
		return errors.New("AI 用量数值不能为负数")
	}
	// now 是本次用量记录采用的 UTC 时间。
	now := time.Now().UTC()
	// err 是本次用量账本插入操作的数据库错误。
	_, err := a.DB.ExecContext(ctx, `
		INSERT INTO ai_usage_records
		(cookie_id,provider,model,task_type,prompt_tokens,completion_tokens,total_tokens,duration_ms,status,error_kind,usage_date,created_at_unix)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.CookieID, record.Provider, record.Model, record.TaskType, record.PromptTokens,
		record.CompletionTokens, record.TotalTokens, record.DurationMS, record.Status,
		strings.TrimSpace(record.ErrorKind), now.Format("2006-01-02"), now.Unix())
	return err
}

// DailySummaryForUser 只返回当前用户拥有账号的聚合用量，日期范围包含起止两天。
func (a *AIUsage) DailySummaryForUser(ctx context.Context, userID int64, startDate, endDate string) ([]AIUsageDailySummary, error) {
	if a == nil || a.DB == nil {
		return nil, errors.New("AI 用量存储未初始化")
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	if !validAIUsageDate(startDate) || !validAIUsageDate(endDate) || startDate > endDate {
		return nil, errors.New("AI 用量日期范围无效")
	}
	// rows 和 err 分别保存用户用量聚合查询结果集及查询错误。
	rows, err := a.DB.QueryContext(ctx, `
		SELECT u.usage_date,u.provider,u.model,u.task_type,COUNT(*),
		       SUM(CASE WHEN u.status='failed' THEN 1 ELSE 0 END),
		       SUM(u.prompt_tokens),SUM(u.completion_tokens),SUM(u.total_tokens),SUM(u.duration_ms)
		  FROM ai_usage_records u
		  JOIN cookies c ON c.id=u.cookie_id
		 WHERE c.user_id=? AND u.usage_date>=? AND u.usage_date<=?
		 GROUP BY u.usage_date,u.provider,u.model,u.task_type
		 ORDER BY u.usage_date,u.provider,u.model,u.task_type`, userID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// summaries 保存当前用户日期范围内的分组用量汇总。
	var summaries []AIUsageDailySummary
	for rows.Next() {
		// summary 保存当前数据库结果行对应的用量汇总。
		var summary AIUsageDailySummary
		// err 是当前用量汇总行的字段扫描错误。
		if err := rows.Scan(&summary.Date, &summary.Provider, &summary.Model, &summary.TaskType,
			&summary.Calls, &summary.FailedCalls, &summary.PromptTokens, &summary.CompletionTokens,
			&summary.TotalTokens, &summary.DurationMS); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// validAIUsageDate 校验日期必须采用 YYYY-MM-DD 格式。
func validAIUsageDate(value string) bool {
	// parsed 和 err 分别保存解析后的日期及日期格式校验错误。
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
