package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// SinglePublishOperations 保存单商品发布的稳定操作标识与远端阶段检查点。
type SinglePublishOperations struct {
	DB *sql.DB
}

// ErrSinglePublishOperationNotFound 表示当前用户不可见该发布操作。
var ErrSinglePublishOperationNotFound = errors.New("单商品发布操作不存在")

// ErrSinglePublishOperationChanged 表示操作状态已被其他请求推进，当前迁移未生效。
var ErrSinglePublishOperationChanged = errors.New("单商品发布操作状态已变化")

// SinglePublishOperation 是单商品发布操作的持久化快照。
type SinglePublishOperation struct {
	OperationID       string
	UserID            int64
	CookieID          string
	RequestFingerprint string
	State             string
	ItemID            string
	ItemURL           string
	RawJSON           string
	ErrorMessage      string
	CreatedAt         string
	UpdatedAt         string
}

// CreatePrepared 创建 prepared 状态操作；operation_id 唯一约束负责跨进程去重。
func (repository *SinglePublishOperations) CreatePrepared(ctx context.Context, operation SinglePublishOperation) error {
	if repository == nil || repository.DB == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	operation.OperationID = strings.TrimSpace(operation.OperationID)
	operation.CookieID = strings.TrimSpace(operation.CookieID)
	operation.RequestFingerprint = strings.TrimSpace(operation.RequestFingerprint)
	if operation.OperationID == "" || operation.UserID <= 0 || operation.CookieID == "" || operation.RequestFingerprint == "" {
		return errors.New("单商品发布操作参数无效")
	}
	if operation.State == "" {
		operation.State = "prepared"
	}
	if operation.State != "prepared" {
		return errors.New("新建单商品发布操作必须处于 prepared 状态")
	}
	// err 保存创建 prepared 操作记录的数据库错误。
	_, err := repository.DB.ExecContext(ctx,
		`INSERT INTO single_publish_operations
		 (operation_id,user_id,cookie_id,request_fingerprint,state,item_id,item_url,raw_json,error_message)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		operation.OperationID, operation.UserID, operation.CookieID, operation.RequestFingerprint,
		operation.State, "", "", "{}", "")
	return err
}

// GetForUser 按操作 ID 和用户归属读取操作，不向其他用户泄露记录是否存在。
func (repository *SinglePublishOperations) GetForUser(ctx context.Context, userID int64, operationID string) (SinglePublishOperation, error) {
	if repository == nil || repository.DB == nil {
		return SinglePublishOperation{}, errors.New("单商品发布操作存储未初始化")
	}
	// operation 保存当前用户可见的单商品发布操作快照。
	var operation SinglePublishOperation
	// err 保存按用户和操作 ID 查询记录的数据库错误。
	err := repository.DB.QueryRowContext(ctx,
		`SELECT operation_id,user_id,cookie_id,request_fingerprint,state,item_id,item_url,raw_json,error_message,created_at,updated_at
		 FROM single_publish_operations WHERE operation_id=? AND user_id=?`,
		strings.TrimSpace(operationID), userID).Scan(
		&operation.OperationID, &operation.UserID, &operation.CookieID, &operation.RequestFingerprint,
		&operation.State, &operation.ItemID, &operation.ItemURL, &operation.RawJSON, &operation.ErrorMessage,
		&operation.CreatedAt, &operation.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SinglePublishOperation{}, ErrSinglePublishOperationNotFound
	}
	return operation, err
}

// MarkStarted 在最终平台创建请求发出前原子地推进 prepared -> started。
func (repository *SinglePublishOperations) MarkStarted(ctx context.Context, userID int64, operationID string) error {
	return repository.transition(ctx, userID, operationID, "prepared", "started", "", "", "{}", "")
}

// MarkRemoteConfirmed 保存平台已确认的商品结果，供进程重启后只执行本地收尾。
func (repository *SinglePublishOperations) MarkRemoteConfirmed(ctx context.Context, userID int64, operationID, itemID, itemURL, rawJSON string) error {
	if strings.TrimSpace(itemID) == "" {
		return errors.New("平台确认结果缺少商品 ID")
	}
	if strings.TrimSpace(rawJSON) == "" {
		rawJSON = "{}"
	}
	return repository.transition(ctx, userID, operationID, "started", "remote_confirmed", itemID, itemURL, rawJSON, "")
}

// MarkLocalConfirmed 标记本地商品记录已成功收口。
func (repository *SinglePublishOperations) MarkLocalConfirmed(ctx context.Context, userID int64, operationID string) error {
	return repository.transition(ctx, userID, operationID, "remote_confirmed", "local_confirmed", "", "", "{}", "")
}

// MarkUnknown 隔离无法确认远端结果的 started 操作，禁止自动重新发布。
func (repository *SinglePublishOperations) MarkUnknown(ctx context.Context, userID int64, operationID, message string) error {
	return repository.transition(ctx, userID, operationID, "started", "unknown", "", "", "{}", message)
}

// transition 使用状态条件更新实现跨进程 compare-and-swap。
func (repository *SinglePublishOperations) transition(ctx context.Context, userID int64, operationID, from, to, itemID, itemURL, rawJSON, message string) error {
	if repository == nil || repository.DB == nil {
		return errors.New("单商品发布操作存储未初始化")
	}
	// result、err 保存状态条件更新结果及数据库错误。
	result, err := repository.DB.ExecContext(ctx,
		`UPDATE single_publish_operations
		 SET state=?,item_id=CASE WHEN ?='' THEN item_id ELSE ? END,
		     item_url=CASE WHEN ?='' THEN item_url ELSE ? END,
		     raw_json=CASE WHEN ?='{}' THEN raw_json ELSE ? END,
		     error_message=?,updated_at=CURRENT_TIMESTAMP
		 WHERE operation_id=? AND user_id=? AND state=?`,
		to, itemID, itemID, itemURL, itemURL, rawJSON, rawJSON, message,
		strings.TrimSpace(operationID), userID, from)
	if err != nil {
		return err
	}
	// affected、err 保存实际迁移行数及读取行数失败原因。
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSinglePublishOperationChanged
	}
	return nil
}
