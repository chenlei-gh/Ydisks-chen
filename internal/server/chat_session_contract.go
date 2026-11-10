package server

import chatapp "xianyu-go/internal/application/chat"

// chatSessionDTO 是聊天会话 HTTP 响应使用的具名 DTO。
type chatSessionDTO struct {
	// AccountID 是账号稳定标识。
	AccountID string `json:"account_id"`
	// ChatID 是平台聊天会话标识。
	ChatID string `json:"chat_id"`
	// PeerUserID 是当前账号之外的会话对端平台标识。
	PeerUserID string `json:"peer_user_id"`
	// PeerName 是会话对端昵称。
	PeerName string `json:"peer_name"`
	// PeerAvatar 是会话对端头像地址。
	PeerAvatar string `json:"peer_avatar_url"`
	// AccountRole 是当前账号在角色商品中的 seller、buyer 或 unknown。
	AccountRole string `json:"account_role"`
	// AutoReplyMode 是当前会话的自动回复模式。
	AutoReplyMode string `json:"auto_reply_mode,omitempty"`
	// BuyerUserID 是已确认的买家平台标识。
	BuyerUserID string `json:"buyer_user_id"`
	// SellerUserID 是已确认的卖家平台标识。
	SellerUserID string `json:"seller_user_id"`
	// RoleItemID 是角色结论绑定的商品标识。
	RoleItemID string `json:"role_item_id"`
	// RoleSource 是角色结论的非敏感证据来源。
	RoleSource string `json:"role_source"`
	// ItemID 是会话关联商品标识。
	ItemID string `json:"item_id"`
	// ItemTitle 是会话关联商品标题。
	ItemTitle string `json:"item_title"`
	// ItemImageURL 是会话关联商品主图的公开地址。
	ItemImageURL string `json:"item_image_url"`
	// LastMessage 是最近一条消息摘要。
	LastMessage string `json:"last_message"`
	// LastMessageAt 是最近消息时间的 Unix 秒。
	LastMessageAt int64 `json:"last_message_at"`
	// UnreadCount 是当前会话未读消息数量。
	UnreadCount int `json:"unread_count"`
}

// newChatSessionDTOFromApplication 将应用层聊天会话转换为 HTTP DTO。
func newChatSessionDTOFromApplication(session chatapp.Session) chatSessionDTO {
	// accountRole 将尚未识别的零值统一输出为契约允许的 unknown。
	accountRole := session.AccountRole
	if accountRole != "seller" && accountRole != "buyer" {
		accountRole = "unknown"
	}
	return chatSessionDTO{
		AccountID: session.AccountID, ChatID: session.ChatID, PeerUserID: session.PeerUserID,
		PeerName: session.PeerName, PeerAvatar: session.PeerAvatar, AccountRole: accountRole, AutoReplyMode: session.AutoReplyMode,
		BuyerUserID: session.BuyerUserID, SellerUserID: session.SellerUserID, RoleItemID: session.RoleItemID, RoleSource: session.RoleSource, ItemID: session.ItemID,
		ItemTitle: session.ItemTitle, ItemImageURL: session.ItemImageURL, LastMessage: session.LastMessage,
		LastMessageAt: session.LastMessageAt, UnreadCount: session.UnreadCount,
	}
}

// newChatSessionDTOsFromApplication 批量转换应用层聊天会话，保持响应不暴露数据库模型。
func newChatSessionDTOsFromApplication(sessions []chatapp.Session) []chatSessionDTO {
	// result 是转换后的聊天会话 DTO 列表。
	result := make([]chatSessionDTO, 0, len(sessions))
	// session 表示当前待转换的应用层会话。
	for _, session := range sessions {
		result = append(result, newChatSessionDTOFromApplication(session))
	}
	return result
}
