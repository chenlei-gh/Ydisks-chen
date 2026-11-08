package adapter

import (
	"context"
	"errors"

	itemapp "xianyu-go/internal/application/items"
	"xianyu-go/internal/db"
)

// SinglePublishOperationRepository 将数据库检查点仓储适配为应用层发布账本端口。
type SinglePublishOperationRepository struct {
	// store 提供数据库发布操作仓储。
	store *db.Store
}

// NewSinglePublishOperationRepository 创建单商品发布操作账本适配器。
func NewSinglePublishOperationRepository(store *db.Store) *SinglePublishOperationRepository {
	return &SinglePublishOperationRepository{store: store}
}

// CreatePrepared 创建待发布操作检查点。
func (r *SinglePublishOperationRepository) CreatePrepared(ctx context.Context, operation itemapp.PublishOperation) error {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	return r.store.SinglePublishOperations.CreatePrepared(ctx, db.SinglePublishOperation{
		OperationID: operation.OperationID, UserID: operation.UserID, CookieID: operation.CookieID,
		RequestFingerprint: operation.RequestFingerprint, State: operation.State,
	})
}

// GetForUser 按用户归属读取发布操作快照。
func (r *SinglePublishOperationRepository) GetForUser(ctx context.Context, userID int64, operationID string) (itemapp.PublishOperation, error) {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return itemapp.PublishOperation{}, errors.New("单商品发布操作存储未初始化")
	}
	// operation 保存数据库返回的持久化检查点。
	operation, err := r.store.SinglePublishOperations.GetForUser(ctx, userID, operationID)
	if err != nil {
		return itemapp.PublishOperation{}, err
	}
	return itemapp.PublishOperation{
		OperationID: operation.OperationID, UserID: operation.UserID, CookieID: operation.CookieID,
		RequestFingerprint: operation.RequestFingerprint, State: operation.State,
		ItemID: operation.ItemID, ItemURL: operation.ItemURL, RawJSON: operation.RawJSON,
		ErrorMessage: operation.ErrorMessage,
	}, nil
}

// MarkStarted 在最终平台创建请求发出前推进状态。
func (r *SinglePublishOperationRepository) MarkStarted(ctx context.Context, userID int64, operationID string) error {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	return r.store.SinglePublishOperations.MarkStarted(ctx, userID, operationID)
}

// MarkRemoteConfirmed 保存平台确认的商品结果。
func (r *SinglePublishOperationRepository) MarkRemoteConfirmed(ctx context.Context, userID int64, operationID, itemID, itemURL, rawJSON string) error {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	return r.store.SinglePublishOperations.MarkRemoteConfirmed(ctx, userID, operationID, itemID, itemURL, rawJSON)
}

// MarkLocalConfirmed 标记本地商品记录收口完成。
func (r *SinglePublishOperationRepository) MarkLocalConfirmed(ctx context.Context, userID int64, operationID string) error {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	return r.store.SinglePublishOperations.MarkLocalConfirmed(ctx, userID, operationID)
}

// MarkUnknown 隔离平台结果未知的操作。
func (r *SinglePublishOperationRepository) MarkUnknown(ctx context.Context, userID int64, operationID, message string) error {
	if r == nil || r.store == nil || r.store.SinglePublishOperations == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	return r.store.SinglePublishOperations.MarkUnknown(ctx, userID, operationID, message)
}

// 确保适配器实现应用层发布账本端口。
var _ itemapp.PublishOperationLedger = (*SinglePublishOperationRepository)(nil)
