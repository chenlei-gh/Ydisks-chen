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
	// srv、store 和 cleanup 提供真实聊天应用、测试数据库及资源回收。
	srv, store, cleanup := newTestServerWithChat(t)
	defer cleanup()
	// ctx 为本次 API 与数据库断言提供统一上下文。
	ctx := context.Background()
	// userID 保存登录测试账号的本地所有者标识。
	var userID int64
	// err 保存测试账号所有者查询结果。
	if err := store.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// accountID 是本次测试创建并归属 admin 的聊天账号。
	const accountID = "chat-handoff-account"
	// err 保存测试账号 Cookie 的创建结果。
	if err := store.Cookies.Save(ctx, accountID, "test-cookie", userID); err != nil {
		t.Fatal(err)
	}
	// session 是需要在 API 中切换人工接管状态的可见会话。
	session := db.ChatSession{CookieID: accountID, ChatID: "chat-handoff-session", BuyerID: "buyer-1"}
	// err 保存会话初始记录写入结果。
	if err := store.Chats.UpsertSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	// err 保存一条入站消息写入结果，避免会话清理逻辑删除空会话。
	if _, _, err := store.Chats.SaveMessage(ctx, session, db.ChatMessage{MessageKey: "handoff-incoming", Direction: "incoming", SenderID: "buyer-1", SenderName: "买家", MessageType: "text", Content: "你好", Status: "received", SentAt: 1}, true); err != nil {
		t.Fatal(err)
	}
	// handler 是挂载版本化聊天 API 的真实 HTTP 路由器。
	handler := srv.Router()
	// cookie 保存已认证 admin 用户的登录 Cookie。
	cookie := loginHelper(t, handler)
	// body 请求将该会话切换为人工接管模式。
	body := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"manual"}`
	// status 验证人工接管 API 返回成功并符合 OpenAPI 契约。
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", body); status != http.StatusOK {
		t.Fatalf("manual handoff status=%d", status)
	}
	// stored 和 err 保存 API 更新后的会话持久化状态。
	stored, err := store.Chats.FindSession(ctx, userID, accountID, session.ChatID)
	if err != nil {
		t.Fatalf("find session after handoff: %v", err)
	}
	if stored.AutoReplyMode != "manual" {
		t.Fatalf("stored mode=%q, want manual", stored.AutoReplyMode)
	}
	// claimed 和 claimErr 验证人工接管后自动回复条件 claim 被拒绝。
	if claimed, claimErr := store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || claimed {
		t.Fatalf("automatic claim during manual mode=%v err=%v", claimed, claimErr)
	}

	// 会话列表 DTO 必须返回持久化模式，供聊天页面按钮准确显示当前状态。
	// listRequest 请求会话列表，以验证 REST DTO 返回当前持久化模式。
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions?account_id="+accountID, nil)
	listRequest.AddCookie(cookie)
	// listRecorder 捕获会话列表 API 的响应状态和 JSON 内容。
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list sessions status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	// listResponse 解码会话列表中的人工接管模式字段。
	var listResponse struct {
		Sessions []struct {
			AutoReplyMode string `json:"auto_reply_mode"`
		} `json:"sessions"`
	}
	// err 保存会话列表 JSON 的反序列化结果。
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listResponse); err != nil {
		t.Fatal(err)
	}
	if len(listResponse.Sessions) != 1 || listResponse.Sessions[0].AutoReplyMode != "manual" {
		t.Fatalf("session DTO mode response=%+v", listResponse.Sessions)
	}

	// restoreBody 请求将会话恢复为自动回复模式。
	restoreBody := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"auto"}`
	// status 验证恢复自动回复模式的 API 请求成功。
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", restoreBody); status != http.StatusOK {
		t.Fatalf("restore auto status=%d", status)
	}
	// claimed 和 claimErr 验证恢复自动模式后自动回复 claim 重新成功。
	if claimed, claimErr := store.Chats.ClaimAutoReply(ctx, accountID, session.ChatID); claimErr != nil || !claimed {
		t.Fatalf("automatic claim after restore=%v err=%v", claimed, claimErr)
	}
	// invalidBody 请求提交非法模式值，验证 API 输入校验。
	invalidBody := `{"account_id":"chat-handoff-account","chat_id":"chat-handoff-session","mode":"invalid"}`
	// status 验证非法模式值被 API 以 400 拒绝。
	if status := requestChatTaskRoute(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/reply-mode", invalidBody); status != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d", status)
	}
}
