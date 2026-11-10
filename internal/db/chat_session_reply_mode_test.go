package db

import (
	"context"
	"errors"
	"testing"
)

// TestMultiDBChatSessionReplyModeAndClaim 验证会话模式、用户归属与自动回复条件 claim 在三种数据库方言中一致。
func TestMultiDBChatSessionReplyModeAndClaim(t *testing.T) {
	// target 表示本轮需要验证的数据库方言及其独立测试存储。
	for _, target := range allTestTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			defer target.cleanup()
			// ctx 为本轮数据库状态迁移与查询提供统一上下文。
			ctx := context.Background()
			// userID 和 accountID 标识本轮测试创建的账号所有者及账号。
			userID, accountID := seedAccount(t, target.store)
			// session 是用于验证会话模式持久化的测试会话。
			session := ChatSession{CookieID: accountID, ChatID: "reply-mode-chat", BuyerID: "peer-1"}
			// err 保存测试会话的初始写入结果。
			if err := target.store.Chats.UpsertSession(ctx, session); err != nil {
				t.Fatalf("upsert session: %v", err)
			}

			// 尚未持久化会话行的旧调用路径没有机会设置人工模式，继续保持历史 auto 默认行为。
			// claimed 和 claimErr 验证缺少会话行时仍沿用历史自动回复默认值。
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, "not-yet-persisted"); claimErr != nil || !claimed {
				t.Fatalf("missing session default claim=%v err=%v, want true", claimed, claimErr)
			}
			// 新会话及迁移后的旧会话均应以 auto 为安全默认值。
			// stored 和 err 保存初始会话读取结果及其默认模式。
			stored, err := target.store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
			if err != nil {
				t.Fatalf("find initial session: %v", err)
			}
			if stored.AutoReplyMode != "auto" {
				t.Fatalf("initial mode=%q, want auto", stored.AutoReplyMode)
			}
			// err 验证其他用户不能修改该会话的接管模式。
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID+1000, accountID, session.ChatID, "manual"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign owner update err=%v, want ErrNotFound", err)
			}
			// err 保存合法用户启用人工接管的数据库结果。
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "manual"); err != nil {
				t.Fatalf("set manual: %v", err)
			}
			// claimed 和 claimErr 验证人工模式下自动回复 claim 被拒绝。
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || claimed {
				t.Fatalf("manual mode claim=%v err=%v, want false", claimed, claimErr)
			}

			// 恢复 auto 后 claim 应成功；版本递增确保 MySQL 将条件 UPDATE 计为受影响行。
			// err 保存用户恢复自动回复模式的数据库结果。
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "auto"); err != nil {
				t.Fatalf("restore auto: %v", err)
			}
			// claimed 和 claimErr 验证恢复自动模式后的第一次 claim 成功。
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || !claimed {
				t.Fatalf("auto mode claim=%v err=%v, want true", claimed, claimErr)
			}
			// claimed 和 claimErr 验证同一自动模式允许后续消息继续 claim。
			if claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || !claimed {
				t.Fatalf("second auto mode claim=%v err=%v, want true", claimed, claimErr)
			}
			// err 验证未知模式值会被数据库仓储拒绝。
			if err := target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "invalid"); err == nil {
				t.Fatal("invalid reply mode must be rejected")
			}

			// 列表、分页和精确读取必须返回同一持久化模式。
			// rows 和 err 保存非分页会话列表的持久化模式读取结果。
			rows, err := target.store.Chats.ListSessions(ctx, userID, accountID, 20)
			if err != nil || len(rows) != 1 || rows[0].AutoReplyMode != "auto" {
				t.Fatalf("list mode rows=%+v err=%v", rows, err)
			}
			// page 和 err 保存分页会话列表的持久化模式读取结果。
			page, err := target.store.Chats.ListSessionPage(ctx, userID, accountID, nil, 20)
			if err != nil || len(page.Sessions) != 1 || page.Sessions[0].AutoReplyMode != "auto" {
				t.Fatalf("page mode=%+v err=%v", page.Sessions, err)
			}
			// version 保存自动回复模式切换和 claim 共同递增的门禁版本。
			var version int64
			// err 验证数据库中的门禁版本已随模式切换和成功 claim 递增。
			if err := target.store.DB.QueryRowContext(ctx, `SELECT reply_gate_version FROM chat_sessions WHERE cookie_id=? AND chat_id=?`, accountID, session.ChatID).Scan(&version); err != nil || version < 4 {
				t.Fatalf("gate version=%d err=%v, want at least 4", version, err)
			}
		})
	}
}

// TestMultiDBChatSessionReplyModeClaimRace 验证人工接管更新与自动回复 claim 在数据库行级串行化。
func TestMultiDBChatSessionReplyModeClaimRace(t *testing.T) {
	// target 表示本轮需要验证的数据库方言及其独立测试存储。
	for _, target := range allTestTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			// ctx 为并发模式切换和 claim 操作提供统一上下文。
			ctx := context.Background()
			// userID 和 accountID 标识本轮并发测试创建的会话所有者。
			userID, accountID := seedAccount(t, target.store)
			// session 是两个数据库操作共同竞争的目标会话。
			session := ChatSession{CookieID: accountID, ChatID: "reply-mode-race", BuyerID: "peer-1"}
			// err 保存会话初始化写入结果。
			if err := target.store.Chats.UpsertSession(ctx, session); err != nil {
				t.Fatalf("upsert race session: %v", err)
			}

			// start 同时释放两个操作，制造模式切换与 claim 的真实竞争。
			start := make(chan struct{})
			// claimResult 接收自动回复 claim 的结果与错误。
			claimResult := make(chan struct {
				claimed bool
				err     error
			}, 1)
			// modeResult 接收人工接管更新的错误。
			modeResult := make(chan error, 1)
			go func() {
				<-start
				// claimed 和 claimErr 描述本次并发自动回复 claim 的提交结果。
				claimed, claimErr := target.store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID)
				claimResult <- struct {
					claimed bool
					err     error
				}{claimed: claimed, err: claimErr}
			}()
			go func() {
				<-start
				// modeErr 保存人工接管更新的提交结果。
				modeResult <- target.store.Chats.SetAutoReplyMode(ctx, userID, accountID, session.ChatID, "manual")
			}()
			close(start)

			// claim 保存并发自动回复操作的最终结果。
			claim := <-claimResult
			if claim.err != nil {
				t.Fatalf("claim during mode race: %v", claim.err)
			}
			// modeErr 保存并发人工接管操作的最终结果。
			if modeErr := <-modeResult; modeErr != nil {
				t.Fatalf("manual mode during claim race: %v", modeErr)
			}
			// stored 和 err 用于确认两个竞争操作结束后人工模式最终持久化。
			stored, err := target.store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
			if err != nil {
				t.Fatalf("find session after mode race: %v", err)
			}
			if stored.AutoReplyMode != "manual" {
				t.Fatalf("final mode=%q, want manual (claim won race: %v)", stored.AutoReplyMode, claim.claimed)
			}
			// claim.claimed 为 true 表示 claim 先提交、该回复可完成；false 表示人工接管先提交并阻止回复。
		})
	}
}
