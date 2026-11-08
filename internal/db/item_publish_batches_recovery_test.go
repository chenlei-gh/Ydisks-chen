package db

import (
	"context"
	"testing"
	"time"
)

// TestPublishBatchLeaseTakeoverPreservesRemoteOutcome 验证租约接管时未知远端结果被隔离，而已保存 ItemID 的行可安全恢复。
func TestPublishBatchLeaseTakeoverPreservesRemoteOutcome(t *testing.T) {
	// store 和 cleanup 保存隔离数据库及其资源清理函数。
	store, cleanup := newTestDB(t)
	defer cleanup()
	// ctx 为本测试中的数据库操作提供统一生命周期。
	ctx := context.Background()
	// userID 保存测试批次所属用户的数据库标识。
	userID, _ := seedAccount(t, store)

	// batch 保存需要模拟 worker 崩溃与接管的批次。
	batch := makePublishBatch(userID, "lease-recovery")
	// rows 分别模拟远端结果未知和已保存 ItemID 的两条明细。
	rows := []ItemPublishBatchRow{
		{RowNo: 1, CookieID: "acc1", Title: "结果未知", Price: "10", Status: "pending"},
		{RowNo: 2, CookieID: "acc1", Title: "已有远端 ID", Price: "20", Status: "pending"},
	}
	// createErr 保存批次和明细初始化失败的原因。
	if createErr := store.PublishBatches.Create(ctx, batch, rows); createErr != nil {
		t.Fatal(createErr)
	}

	// oldToken 标识模拟崩溃前持有租约的旧 worker。
	oldToken := "worker-before-crash"
	// claimed 和 claimErr 保存旧 worker 的批次领取结果。
	claimed, claimErr := store.PublishBatches.ClaimBatch(ctx, batch.ID, oldToken, time.Now().Add(-time.Minute).Unix())
	if claimErr != nil || !claimed {
		t.Fatalf("旧 worker 领取批次失败: claimed=%v err=%v", claimed, claimErr)
	}
	// stored 和 rowsErr 保存旧 worker 领取后的数据库明细快照。
	stored, rowsErr := store.PublishBatches.Rows(ctx, batch.ID)
	if rowsErr != nil || len(stored) != 2 {
		t.Fatalf("读取批次行失败: rows=%+v err=%v", stored, rowsErr)
	}
	// row 表示当前由旧 worker 领取的批次明细。
	for _, row := range stored {
		// ok 和 claimErr 保存旧 worker 领取当前明细的结果。
		ok, claimErr := store.PublishBatches.ClaimRow(ctx, row.ID, oldToken)
		if claimErr != nil || !ok {
			t.Fatalf("旧 worker 领取行 %d 失败: claimed=%v err=%v", row.ID, ok, claimErr)
		}
	}

	// unknownRowID 标识远端请求已开始但尚未保存 ItemID 的明细。
	unknownRowID := stored[0].ID
	// started 和 startErr 保存远端开始检查点的写入结果。
	started, startErr := store.PublishBatches.MarkClaimedRemoteStarted(ctx, unknownRowID, oldToken)
	if startErr != nil || !started {
		t.Fatalf("写入远端开始检查点失败: started=%v err=%v", started, startErr)
	}

	// checkpointRowID 标识已经持久化远端商品 ID 的明细。
	checkpointRowID := stored[1].ID
	// saved 和 saveErr 保存远端商品结果检查点的写入结果。
	saved, saveErr := store.PublishBatches.SaveClaimedRemoteResult(ctx, checkpointRowID, oldToken,
		"remote-item-2", "https://www.goofish.com/item?id=remote-item-2", `{"itemId":"remote-item-2"}`)
	if saveErr != nil || !saved {
		t.Fatalf("保存远端结果检查点失败: saved=%v err=%v", saved, saveErr)
	}

	// 模拟旧 worker 进程退出后租约到期。
	// expireErr 保存将旧批次租约设置为已过期状态时的数据库错误。
	if _, expireErr := store.DB.ExecContext(ctx,
		"UPDATE item_publish_batches SET lease_expires_at=0 WHERE id=?", batch.ID); expireErr != nil {
		t.Fatal(expireErr)
	}
	// newToken 标识进程重启后接管批次的新 worker。
	newToken := "worker-after-restart"
	// claimed 和 takeoverErr 保存新 worker 的批次接管结果。
	claimed, takeoverErr := store.PublishBatches.ClaimBatch(ctx, batch.ID, newToken, time.Now().Add(time.Minute).Unix())
	if takeoverErr != nil || !claimed {
		t.Fatalf("新 worker 接管批次失败: claimed=%v err=%v", claimed, takeoverErr)
	}

	// recovered 和 recoveryErr 保存新 worker 接管后的明细状态。
	recovered, recoveryErr := store.PublishBatches.Rows(ctx, batch.ID)
	if recoveryErr != nil || len(recovered) != 2 {
		t.Fatalf("读取接管后的批次行失败: rows=%+v err=%v", recovered, recoveryErr)
	}
	// unknown 保存未取得远端 ItemID 的明细，必须进入人工核对状态。
	unknown := recovered[0]
	if unknown.Status != "failed" || unknown.FailureKind != "uncertain_remote" || unknown.WorkerToken != "" {
		t.Fatalf("远端结果未知的行未被隔离: %+v", unknown)
	}
	// checkpoint 保存远端 ItemID 已落盘、等待本地后处理的明细。
	checkpoint := recovered[1]
	if checkpoint.Status != "pending" || checkpoint.FailureKind != "interrupted" ||
		checkpoint.ItemID != "remote-item-2" || checkpoint.ItemURL == "" || checkpoint.RawJSON == "" {
		t.Fatalf("已保存 ItemID 的行未保留恢复检查点: %+v", checkpoint)
	}

	// uncertain_remote 不得被普通失败重试入口重新置为 pending。
	// resetErr 保存普通失败重试入口的执行结果。
	if resetErr := store.PublishBatches.ResetFailed(ctx, batch.ID); resetErr != nil {
		t.Fatal(resetErr)
	}
	// afterReset 和 resetRowsErr 保存普通重试入口执行后的明细快照。
	afterReset, resetRowsErr := store.PublishBatches.Rows(ctx, batch.ID)
	if resetRowsErr != nil {
		t.Fatal(resetRowsErr)
	}
	if afterReset[0].Status != "failed" || afterReset[0].FailureKind != "uncertain_remote" {
		t.Fatalf("普通失败重试错误地重放未知远端行: %+v", afterReset[0])
	}

	// 已有 ItemID 的恢复行可以由新 worker 领取并完成本地后处理收口。
	// claimed 和 rowClaimErr 保存新 worker 对已有远端结果明细的领取结果。
	claimed, rowClaimErr := store.PublishBatches.ClaimRow(ctx, checkpointRowID, newToken)
	if rowClaimErr != nil || !claimed {
		t.Fatalf("新 worker 无法领取已有远端结果的恢复行: claimed=%v err=%v", claimed, rowClaimErr)
	}
	// marked 和 markErr 保存恢复行最终成功状态的写入结果。
	marked, markErr := store.PublishBatches.MarkClaimedRowSuccess(ctx, checkpointRowID, newToken,
		checkpoint.ItemID, checkpoint.ItemURL, checkpoint.RawJSON)
	if markErr != nil || !marked {
		t.Fatalf("恢复行最终收口失败: marked=%v err=%v", marked, markErr)
	}
	// finalRows 和 finalRowsErr 保存恢复收口后的最终明细状态。
	finalRows, finalRowsErr := store.PublishBatches.Rows(ctx, batch.ID)
	if finalRowsErr != nil {
		t.Fatal(finalRowsErr)
	}
	if finalRows[1].Status != "success" || finalRows[1].ItemID != "remote-item-2" || finalRows[1].FailureKind != "" {
		t.Fatalf("恢复行最终状态异常: %+v", finalRows[1])
	}
}
