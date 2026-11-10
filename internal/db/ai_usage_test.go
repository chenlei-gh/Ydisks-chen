package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestAIUsageRecordsAndAggregatesByUser 验证 Token 用量记录、模型任务分组和用户账号隔离。
func TestAIUsageRecordsAndAggregatesByUser(t *testing.T) {
	for _, target := range allTestTargets(t) {
		target := target
		t.Run(target.name, func(t *testing.T) {
			defer target.cleanup()
			ctx := context.Background()
			// suffix 保证每个方言测试创建的用户和账号标识唯一。
			suffix := fmt.Sprintf("%d", time.Now().UnixNano())
			// ownerName 是拥有用量记录的测试用户名称。
			ownerName := target.name + "_usage_owner_" + suffix
			if created, err := target.store.Users.Create(ctx, ownerName, ownerName+"@example.invalid", "test-password"); err != nil || !created {
				t.Fatalf("create usage owner: created=%v err=%v", created, err)
			}
			// owner 是当前用量记录的账号所有者。
			owner, err := target.store.Users.GetByUsername(ctx, ownerName)
			if err != nil {
				t.Fatal(err)
			}
			// ownerCookie 是当前用户拥有的模型调用账号。
			ownerCookie := "usage_owner_" + suffix
			if err := target.store.Cookies.Save(ctx, ownerCookie, "test-cookie", owner.ID); err != nil {
				t.Fatal(err)
			}
			// otherName 和 otherCookie 用于验证聚合查询不会泄露其他用户的 Token 用量。
			otherName := target.name + "_usage_other_" + suffix
			if created, err := target.store.Users.Create(ctx, otherName, otherName+"@example.invalid", "test-password"); err != nil || !created {
				t.Fatalf("create other user: created=%v err=%v", created, err)
			}
			other, err := target.store.Users.GetByUsername(ctx, otherName)
			if err != nil {
				t.Fatal(err)
			}
			otherCookie := "usage_other_" + suffix
			if err := target.store.Cookies.Save(ctx, otherCookie, "test-cookie", other.ID); err != nil {
				t.Fatal(err)
			}
			// successRecord 是本账号一次成功文本模型请求的用量。
			successRecord := AIUsageRecord{
				CookieID: ownerCookie, Provider: "api.example.com", Model: "model-text", TaskType: "text",
				PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140, DurationMS: 350, Status: "success",
			}
			if err := target.store.AIUsage.Record(ctx, successRecord); err != nil {
				t.Fatalf("record successful usage: %v", err)
			}
			// failedRecord 验证视觉请求失败也能记录调用次数，但不伪造 Token 消耗。
			failedRecord := AIUsageRecord{
				CookieID: ownerCookie, Provider: "api.example.com", Model: "model-vision", TaskType: "vision",
				DurationMS: 80, Status: "failed", ErrorKind: "request_error",
			}
			if err := target.store.AIUsage.Record(ctx, failedRecord); err != nil {
				t.Fatalf("record failed usage: %v", err)
			}
			if err := target.store.AIUsage.Record(ctx, AIUsageRecord{
				CookieID: otherCookie, Provider: "other.example.com", Model: "other-model", TaskType: "text",
				PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1000, Status: "success",
			}); err != nil {
				t.Fatalf("record other user's usage: %v", err)
			}
			// today 是按 UTC 生成的用量日期，查询边界与账本日期采用同一时区。
			today := time.Now().UTC().Format("2006-01-02")
			summaries, err := target.store.AIUsage.DailySummaryForUser(ctx, owner.ID, today, today)
			if err != nil {
				t.Fatalf("query daily usage: %v", err)
			}
			if len(summaries) != 2 {
				t.Fatalf("expected two model/task groups, got %+v", summaries)
			}
			var sawText, sawVision bool
			for _, summary := range summaries {
				if summary.Provider != "api.example.com" {
					t.Fatalf("provider mismatch: %+v", summary)
				}
				switch summary.TaskType {
				case "text":
					sawText = true
					if summary.Calls != 1 || summary.FailedCalls != 0 || summary.PromptTokens != 100 || summary.CompletionTokens != 40 || summary.TotalTokens != 140 || summary.DurationMS != 350 {
						t.Fatalf("text usage mismatch: %+v", summary)
					}
				case "vision":
					sawVision = true
					if summary.Calls != 1 || summary.FailedCalls != 1 || summary.TotalTokens != 0 {
						t.Fatalf("vision failure usage mismatch: %+v", summary)
					}
				default:
					t.Fatalf("unexpected task type: %+v", summary)
				}
			}
			if !sawText || !sawVision {
				t.Fatalf("missing text or vision usage: %+v", summaries)
			}
			if _, err := target.store.AIUsage.DailySummaryForUser(ctx, owner.ID, "2026-02-30", "2026-03-01"); err == nil {
				t.Fatal("invalid calendar date should be rejected")
			}
			if _, err := target.store.AIUsage.DailySummaryForUser(ctx, 0, today, today); err != ErrInvalidUserID {
				t.Fatalf("invalid user ID should be rejected, got %v", err)
			}
		})
	}
}
