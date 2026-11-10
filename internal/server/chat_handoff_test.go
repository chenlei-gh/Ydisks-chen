package server

import (
	"context"
	"net/http"
	"testing"

	"xianyu-go/internal/db"
)

// TestChatSessionReplyModeEndpoint 验证人工接管 API 的归属、持久化和恢复自动回复行为。
func TestChatSessionReplyModeEndpoint(t *testing.T) {
	srv, store, cleanup := newTestServerWithChat(t)
	defer cleanup()
	ctx := context.Background()
	var userID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	const accountID = "chat-handoff-account"
	if err := store.Cookies.Save(ctx, accountID, "test-cookie", userID); err != nil {
		t.Fatal(err)
	}
	session := db.ChatSession{CookieID: accountID, ChatID: "chat-handoff-session", BuyerID: "buyer-1"}
	if err := store.Chats.UpsertSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	handler := srv.Router()
	cookie := loginHelper(t, handler)
	body := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"manual"}`
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", body); status != http.StatusOK {
		t.Fatalf("manual handoff status=%d", status)
	}
	stored, err := store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
	if err != nil || stored.AutoReplyMode != "manual" {
		t.Fatalf("stored mode=%q err=%v", stored.AutoReplyMode, err)
	}
	if claimed, claimErr := store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || claimed {
		t.Fatalf("automatic claim during manual mode=%v err=%v", claimed, claimErr)
	}

	restoreBody := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"auto"}`
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", restoreBody); status != http.StatusOK {
		t.Fatalf("restore auto status=%d", status)
	}
	if claimed, claimErr := store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || !claimed {
		t.Fatalf("automatic claim after restore=%v err=%v", claimed, claimErr)
	}
	invalidBody := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"invalid"}`
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", invalidBody); status != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d", status)
	}
}
