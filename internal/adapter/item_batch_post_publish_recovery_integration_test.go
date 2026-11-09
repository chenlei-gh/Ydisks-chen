package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	itemapp "xianyu-go/internal/application/items"
	"xianyu-go/internal/db"
	"xianyu-go/internal/xianyu/mtop"
)

// batchCheckpointRecoveryLocalPublisher 将恢复后的本地收口写入真实批次仓储。
type batchCheckpointRecoveryLocalPublisher struct {
	// store 保存本次测试使用的真实 SQLite 数据库。
	store *db.Store
	// calls 记录 worker 调用本地收口的次数。
	calls int
	// workerToken 保存本地收口收到的新 worker 租约令牌。
	workerToken string
	// itemID 保存从远端检查点恢复出的商品标识。
	itemID string
}

// Complete 将恢复出的远端商品结果写入当前 worker 持有的成功检查点。
func (publisher *batchCheckpointRecoveryLocalPublisher) Complete(ctx context.Context, _ int64, row itemapp.BatchRow, workerToken string, result *itemapp.BatchPublishResult) error {
	publisher.calls++
	publisher.workerToken = workerToken
	if result == nil {
		return errors.New("恢复结果为空")
	}
	publisher.itemID = result.ItemID
	rawJSON, err := json.Marshal(result.RawData)
	if err != nil {
		return err
	}
	marked, err := publisher.store.PublishBatches.MarkClaimedRowSuccess(ctx, row.ID, workerToken, result.ItemID, result.ItemURL, string(rawJSON))
	if err != nil {
		return err
	}
	if !marked {
		return itemapp.ErrBatchLeaseLost
	}
	return nil
}

// TestBatchRunnerCompletesPostPublishCheckpointAfterLeaseTakeover 验证 worker 接管已有远端结果后只执行本地收口，不重复调用平台。
func TestBatchRunnerCompletesPostPublishCheckpointAfterLeaseTakeover(t *testing.T) {
	// store、cleanup 保存本测试使用的隔离 SQLite 数据库及关闭责任。
	store, cleanup := newAdapterTestStore(t)
	defer cleanup()
	// ctx 是本测试共用的非取消数据库上下文。
	ctx := context.Background()
	// admin、adminErr 保存测试批次所属用户及读取错误。
	admin, adminErr := store.Users.GetByUsername(ctx, "admin")
	if adminErr != nil {
		t.Fatalf("读取测试用户失败: %v", adminErr)
	}
	// batch 保存模拟 worker 中断后待恢复的批次。
	batch := &db.ItemPublishBatch{
		ID: "batch-post-publish-recovery", UserID: admin.ID, DefaultCookieID: "cid",
		UploadDir: t.TempDir(), Status: "pending",
	}
	// rows 保存唯一一条将由旧 worker 发布成功的商品明细。
	rows := []db.ItemPublishBatchRow{{
		RowNo: 1, CookieID: "cid", Title: "恢复商品", Price: "12.50",
		Quantity: 1, Status: "pending",
	}}
	// createErr 保存批次和明细的初始化结果。
	if createErr := store.PublishBatches.Create(ctx, batch, rows); createErr != nil {
		t.Fatalf("创建测试批次失败: %v", createErr)
	}
	// oldToken 标识模拟中断前持有批次的旧 worker。
	oldToken := "worker-before-crash"
	// oldClaimed、oldClaimErr 保存旧 worker 的过期租约领取结果。
	oldClaimed, oldClaimErr := store.PublishBatches.ClaimBatch(ctx, batch.ID, oldToken, time.Now().Add(-time.Minute).Unix())
	if oldClaimErr != nil || !oldClaimed {
		t.Fatalf("旧 worker 领取批次失败: claimed=%v err=%v", oldClaimed, oldClaimErr)
	}
	// storedRows、rowsErr 保存旧 worker 领取后的数据库明细。
	storedRows, rowsErr := store.PublishBatches.Rows(ctx, batch.ID)
	if rowsErr != nil || len(storedRows) != 1 {
		t.Fatalf("读取批次明细失败: rows=%+v err=%v", storedRows, rowsErr)
	}
	// oldRowClaimed、oldRowClaimErr 保存旧 worker 对商品行的领取结果。
	oldRowClaimed, oldRowClaimErr := store.PublishBatches.ClaimRow(ctx, storedRows[0].ID, oldToken)
	if oldRowClaimErr != nil || !oldRowClaimed {
		t.Fatalf("旧 worker 领取商品行失败: claimed=%v err=%v", oldRowClaimed, oldRowClaimErr)
	}
	// remoteStarted、remoteStartedErr 保存最终远端创建请求开始前的持久化检查点。
	remoteStarted, remoteStartedErr := store.PublishBatches.MarkClaimedRemoteStarted(ctx, storedRows[0].ID, oldToken)
	if remoteStartedErr != nil || !remoteStarted {
		t.Fatalf("写入远端开始检查点失败: started=%v err=%v", remoteStarted, remoteStartedErr)
	}
	// saved、saveErr 保存远端成功返回的商品身份和原始结果。
	saved, saveErr := store.PublishBatches.SaveClaimedRemoteResult(ctx, storedRows[0].ID, oldToken,
		"recovered-item", "https://example.test/item/recovered-item", `{"itemId":"recovered-item"}`)
	if saveErr != nil || !saved {
		t.Fatalf("保存远端结果检查点失败: saved=%v err=%v", saved, saveErr)
	}
	// expireErr 将旧 worker 的批次租约设置为过期，模拟进程硬中断。
	if _, expireErr := store.DB.ExecContext(ctx, "UPDATE item_publish_batches SET lease_expires_at=0 WHERE id=?", batch.ID); expireErr != nil {
		t.Fatalf("使旧 worker 租约过期失败: %v", expireErr)
	}
	// newToken 标识重启后接管批次的新 worker。
	newToken := "worker-after-restart"
	// newClaimed、newClaimErr 保存新 worker 的租约接管结果。
	newClaimed, newClaimErr := store.PublishBatches.ClaimBatch(ctx, batch.ID, newToken, time.Now().Add(time.Minute).Unix())
	if newClaimErr != nil || !newClaimed {
		t.Fatalf("新 worker 接管批次失败: claimed=%v err=%v", newClaimed, newClaimErr)
	}
	// recoveredRows、recoveryErr 保存接管后保留远端商品身份的待收口行。
	recoveredRows, recoveryErr := store.PublishBatches.Rows(ctx, batch.ID)
	if recoveryErr != nil || len(recoveredRows) != 1 {
		t.Fatalf("读取接管后的批次明细失败: rows=%+v err=%v", recoveredRows, recoveryErr)
	}
	if recoveredRows[0].Status != "pending" || recoveredRows[0].FailureKind != "interrupted" || recoveredRows[0].ItemID != "recovered-item" {
		t.Fatalf("接管后未保留可恢复的 post_publish 检查点: %+v", recoveredRows[0])
	}
	// publishCalls 记录远端平台发布调用次数；恢复路径不应触发该调用。
	publishCalls := 0
	// client 是一旦触发远端创建就立即返回错误的平台替身。
	client := batchPublishClientStub{publish: func(context.Context, string, mtop.PublishItemRequest) (*mtop.PublishItemResult, error) {
		publishCalls++
		return nil, errors.New("恢复检查点时不应再次调用平台")
	}}
	// remotePort 使用真实批次数据库读取已保存的 ItemID 检查点。
	remotePort := NewItemBatchPublishPort(store, func() mtop.Client { return client }, nil, nil, nil, nil, nil)
	// localPublisher 通过真实数据库完成恢复行的本地成功状态收口。
	localPublisher := &batchCheckpointRecoveryLocalPublisher{store: store}
	// publisher 组合真实远端检查点适配器和本地收口替身。
	publisher := itemBatchPublisher{remotePort: remotePort, localService: localPublisher}
	// repository 使用真实数据库批次仓储适配器驱动完整 worker 流程。
	repository := NewItemBatchRepository(store)
	// runner、runnerErr 保存使用新 worker 租约执行的批量 worker。
	runner, runnerErr := itemapp.NewBatchRunner(repository, publisher, itemapp.BatchRunOptions{
		LeaseDuration: time.Minute,
		Wait:          func(context.Context, time.Duration) error { return nil },
		ClassifyFailure: func(err error, _ string) (string, string) {
			return err.Error(), "publish"
		},
	})
	if runnerErr != nil {
		t.Fatalf("构造批量 worker 失败: %v", runnerErr)
	}
	// runErr 保存新 worker 从远端检查点恢复并完成批次的结果。
	if runErr := runner.Run(ctx, admin.ID, batch.ID, newToken, false); runErr != nil {
		t.Fatalf("新 worker 恢复 post_publish 检查点失败: %v", runErr)
	}
	if publishCalls != 0 {
		t.Fatalf("恢复路径重复调用平台发布: calls=%d", publishCalls)
	}
	if localPublisher.calls != 1 || localPublisher.workerToken != newToken || localPublisher.itemID != "recovered-item" {
		t.Fatalf("本地收口未使用新 worker 和已保存远端结果: calls=%d token=%q itemID=%q",
			localPublisher.calls, localPublisher.workerToken, localPublisher.itemID)
	}
	// finalRows、finalRowsErr 保存 worker 最终收口后的批次明细。
	finalRows, finalRowsErr := store.PublishBatches.Rows(ctx, batch.ID)
	if finalRowsErr != nil || len(finalRows) != 1 || finalRows[0].Status != "success" || finalRows[0].ItemID != "recovered-item" {
		t.Fatalf("恢复行最终状态异常: rows=%+v err=%v", finalRows, finalRowsErr)
	}
	// finalBatch、finalBatchErr 保存 worker 最终收口后的批次状态。
	finalBatch, finalBatchErr := store.PublishBatches.Get(ctx, admin.ID, batch.ID)
	if finalBatchErr != nil || finalBatch.Status != "completed" || finalBatch.SuccessCount != 1 || finalBatch.FailedCount != 0 {
		t.Fatalf("恢复批次最终状态异常: batch=%+v err=%v", finalBatch, finalBatchErr)
	}
}
