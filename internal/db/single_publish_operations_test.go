package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// TestSinglePublishOperationsStateTransitions 验证单商品发布操作只能按安全状态顺序推进。
func TestSinglePublishOperationsStateTransitions(t *testing.T) {
	// database、dialect 和 openErr 保存测试数据库及迁移结果。
	database, dialect, openErr := Open(context.Background(), filepath.Join(t.TempDir(), "single-publish.db"))
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	defer database.Close()
	if dialect != DialectSQLite {
		t.Fatalf("dialect=%q want sqlite", dialect)
	}
	// repository 是待验证的单商品发布操作仓储。
	repository := &SinglePublishOperations{DB: database}
	// operation 是新建的发布操作快照。
	operation := SinglePublishOperation{
		OperationID: "single-publish-state-transition-1", UserID: 7, CookieID: "account-7",
		RequestFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		State:              "prepared",
	}
	if createErr := repository.CreatePrepared(context.Background(), operation); createErr != nil {
		t.Fatalf("CreatePrepared: %v", createErr)
	}
	// duplicateErr 验证唯一操作 ID 阻止相同请求创建第二条操作记录。
	duplicateErr := repository.CreatePrepared(context.Background(), operation)
	if duplicateErr == nil {
		t.Fatal("duplicate operation ID unexpectedly accepted")
	}
	if startErr := repository.MarkStarted(context.Background(), operation.UserID, operation.OperationID); startErr != nil {
		t.Fatalf("MarkStarted: %v", startErr)
	}
	// duplicateStartErr 验证 started 状态不能再次通过 prepared 闸门。
	duplicateStartErr := repository.MarkStarted(context.Background(), operation.UserID, operation.OperationID)
	if !errors.Is(duplicateStartErr, ErrSinglePublishOperationChanged) {
		t.Fatalf("second MarkStarted error=%v", duplicateStartErr)
	}
	if remoteErr := repository.MarkRemoteConfirmed(context.Background(), operation.UserID, operation.OperationID, "remote-item-7", "https://example.invalid/item/7", `{"item_id":"remote-item-7"}`); remoteErr != nil {
		t.Fatalf("MarkRemoteConfirmed: %v", remoteErr)
	}
	// localErr 表示远端确认后的本地收尾状态迁移结果。
	if localErr := repository.MarkLocalConfirmed(context.Background(), operation.UserID, operation.OperationID); localErr != nil {
		t.Fatalf("MarkLocalConfirmed: %v", localErr)
	}
	// stored、readErr 保存最终持久化快照及查询错误。
	stored, readErr := repository.GetForUser(context.Background(), operation.UserID, operation.OperationID)
	if readErr != nil {
		t.Fatalf("GetForUser: %v", readErr)
	}
	if stored.State != "local_confirmed" || stored.ItemID != "remote-item-7" || stored.ItemURL != "https://example.invalid/item/7" || stored.RawJSON != `{"item_id":"remote-item-7"}` {
		t.Fatalf("unexpected stored operation: %+v", stored)
	}
	// otherUserErr 验证操作 ID 不会泄露给其他用户。
	_, otherUserErr := repository.GetForUser(context.Background(), operation.UserID+1, operation.OperationID)
	if !errors.Is(otherUserErr, ErrSinglePublishOperationNotFound) {
		t.Fatalf("other user lookup error=%v", otherUserErr)
	}
}

// TestSinglePublishOperationsUnknownCannotBeStartedAgain 验证未知远端结果被隔离后不能重新打开发布闸门。
func TestSinglePublishOperationsUnknownCannotBeStartedAgain(t *testing.T) {
	// database、_ 和 openErr 保存测试数据库及迁移结果。
	database, _, openErr := Open(context.Background(), filepath.Join(t.TempDir(), "single-publish-unknown.db"))
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	defer database.Close()
	// repository 是待验证的单商品发布操作仓储。
	repository := &SinglePublishOperations{DB: database}
	// operation 是待隔离的发布操作。
	operation := SinglePublishOperation{
		OperationID: "single-publish-unknown-1", UserID: 9, CookieID: "account-9",
		RequestFingerprint: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		State:              "prepared",
	}
	if createErr := repository.CreatePrepared(context.Background(), operation); createErr != nil {
		t.Fatalf("CreatePrepared: %v", createErr)
	}
	if startErr := repository.MarkStarted(context.Background(), operation.UserID, operation.OperationID); startErr != nil {
		t.Fatalf("MarkStarted: %v", startErr)
	}
	if unknownErr := repository.MarkUnknown(context.Background(), operation.UserID, operation.OperationID, "平台响应丢失，结果待人工核对"); unknownErr != nil {
		t.Fatalf("MarkUnknown: %v", unknownErr)
	}
	// restartErr 验证 unknown 状态不能再次推进到 started。
	restartErr := repository.MarkStarted(context.Background(), operation.UserID, operation.OperationID)
	if !errors.Is(restartErr, ErrSinglePublishOperationChanged) {
		t.Fatalf("MarkStarted after unknown error=%v", restartErr)
	}
	// stored、readErr 保存未知操作的最终状态及查询错误。
	stored, readErr := repository.GetForUser(context.Background(), operation.UserID, operation.OperationID)
	if readErr != nil {
		t.Fatalf("GetForUser: %v", readErr)
	}
	if stored.State != "unknown" || stored.ErrorMessage != "平台响应丢失，结果待人工核对" {
		t.Fatalf("unexpected unknown operation: %+v", stored)
	}
}
