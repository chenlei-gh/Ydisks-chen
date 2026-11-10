package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if _, _, err := store.Chats.SaveMessage(ctx, session, db.ChatMessage{MessageKey: "handoff-incoming", Direction: "incoming", SenderID: "buyer-1", SenderName: "买家", MessageType: "text", Content: "你好", Status: "received", SentAt: 1}, true); err != nil {
		t.Fatal(err)
	}
	handler := srv.Router()
	cookie := loginHelper(t, handler)
	body := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"manual"}`
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", body); status != http.StatusOK {
		t.Fatalf("manual handoff status=%d", status)
	}
	stored, err := store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
	if err != nil {
		t.Fatalf("find session after handoff: %v", err)
	}
	if stored.AutoReplyMode != "manual" {
		t.Fatalf("stored mode=%q, want manual", stored.AutoReplyMode)
	}
	if claimed, claimErr := store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || claimed {
		t.Fatalf("automatic claim during manual mode=%v err=%v", claimed, claimErr)
	}


	// 会话列表 DTO 必须返回持久化模式，供聊天页面按钮准确显示当前状态。
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions?account_id="+accountID, nil)
	listRequest.AddCookie(cookie)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list sessions status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResponse struct {
		Sessions []struct {
			AutoReplyMode string `json:"auto_reply_mode"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listResponse); err != nil {
		t.Fatal(err)
	}
	if len(listResponse.Sessions) != 1 || listResponse.Sessions[0].AutoReplyMode != "manual" {
		t.Fatalf("session DTO mode response=%+v", listResponse.Sessions)
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
