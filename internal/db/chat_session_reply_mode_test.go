package db

import (
	"context"
	"errors"
	"testing"
)

// TestMultiDBChatSessionReplyModeAndClaim 验证会话模式、用户归属与自动回复条件 claim 在三种数据库方言中一致。
func TestMultiDBChatSessionReplyModeAndClaim(t *testing.T) {
	for _, target := range allTestTargets(t) {
		target := target
		t.Run(target.name, func(t *testing.T) {
			defer target.cleanup()
			ctx := context.Background()
			userID, accountID := seedAccount(t, target.store)
			session := ChatSession{CookieID: accountID, ChatID: "reply-mode-chat", BuyerID: "peer-1"}
			if err := target.store.Chats.UpsertSession(ctx, session); err != nil {
				t.Fatalf("upsert session: %v", err)
			}

			// 新会话及迁移后的旧会话均应以 auto 为安全默认值。
			stored, err := target.store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
			if err != nil || stored.AutoReplyMode != "auto" {
				t.Fatalf("initial mode=%q err=%v", stored.AutoReplyMode, err)
			}
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID+1000, accountID, session.ChatID, "manual"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign owner update err=%v, want ErrNotFound", err)
			}
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "manual"); err != nil {
				t.Fatalf("set manual: %v", err)
			}
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || claimed {
				t.Fatalf("manual mode claim=%v err=%v, want false", claimed, claimErr)
			}

			// 恢复 auto 后 claim 应成功；版本递增确保 MySQL 将条件 UPDATE 计为受影响行。
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "auto"); err != nil {
				t.Fatalf("restore auto: %v", err)
			}
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || !claimed {
				t.Fatalf("auto mode claim=%v err=%v, want true", claimed, claimErr)
			}
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "invalid"); err == nil {
				t.Fatal("invalid reply mode must be rejected")
			}

			// 列表、分页和精确读取必须返回同一持久化模式。
			rows, err := target.store.Chats.ListSessions(ctx, userID, accountID, 20)
			if err != nil || len(rows) != 1 || rows[0].AutoReplyMode != "auto" {
				t.Fatalf("list mode rows=%+v err=%v", rows, err)
			}
			page, err := target.store.Chats.ListSessionPage(ctx, userID, accountID, nil, 20)
			if err != nil || len(page.Sessions) != 1 || page.Sessions[0].AutoReplyMode != "auto" {
				t.Fatalf("page mode=%+v err=%v", page.Sessions, err)
			}
			var version int64
			if err := target.store.DB.QueryRowContext(ctx, `SELECT reply_gate_version FROM chat_sessions WHERE cookie_id=? AND chat_id=?`, accountID, session.ChatID).Scan(&version); err != nil || version < 4 {
				t.Fatalf("gate version=%d err=%v, want at least 4", version, err)
			}
		})
	}
}
