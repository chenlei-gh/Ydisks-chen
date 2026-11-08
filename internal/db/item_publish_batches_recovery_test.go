package db

import (
	"context"
	"testing"
	"time"
)

// TestPublishBatchLeaseTakeoverPreservesRemoteOutcome 验证租约接管时未知远端结果被隔离，而已保存 ItemID 的行可安全恢复。
func TestPublishBatchLeaseTakeoverPreservesRemoteOutcome(t *testing.T) {
	store, cleanup := newTestDB(t)
	defer cleanup()
	ctx := context.Background()
	userID, _ := seedAccount(t, store)

	batch := makePublishBatch(userID, "lease-recovery")
	rows := []ItemPublishBatchRow{
		{RowNo: 1, CookieID: "acc1", Title: "结果未知", Price: "10", Status: "pending"},
		{RowNo: 2, CookieID: "acc1", Title: "已有远端 ID", Price: "20", Status: "pending"},
	}
	if err := store.PublishBatches.Create(ctx, batch, rows); err != nil {
		t.Fatal(err)
	}

	oldToken := "worker-before-crash"
	claimed, err := store.PublishBatches.ClaimBatch(ctx, batch.ID, oldToken, time.Now().Add(-time.Minute).Unix())
	if err != nil || !claimed {
		t.Fatalf("旧 worker 领取批次失败: claimed=%v err=%v", claimed, err)
	}
	stored, err := store.PublishBatches.Rows(ctx, batch.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("读取批次行失败: rows=%+v err=%v", stored, err)
	}
	for _, row := range stored {
		ok, claimErr := store.PublishBatches.ClaimRow(ctx, row.ID, oldToken)
		if claimErr != nil || !ok {
			t.Fatalf("旧 worker 领取行 %d 失败: claimed=%v err=%v", row.ID, ok, claimErr)
		}
	}

	unknownRowID := stored[0].ID
	started, err := store.PublishBatches.MarkClaimedRemoteStarted(ctx, unknownRowID, oldToken)
	if err != nil || !started {
		t.Fatalf("写入远端开始检查点失败: started=%v err=%v", started, err)
	}

	checkpointRowID := stored[1].ID
	saved, err := store.PublishBatches.SaveClaimedRemoteResult(ctx, checkpointRowID, oldToken,
		"remote-item-2", "https://www.goofish.com/item?id=remote-item-2", `{"itemId":"remote-item-2"}`)
	if err != nil || !saved {
		t.Fatalf("保存远端结果检查点失败: saved=%v err=%v", saved, err)
	}

	// 模拟旧 worker 进程退出后租约到期。
	if _, err := store.DB.ExecContext(ctx,
		"UPDATE item_publish_batches SET lease_expires_at=0 WHERE id=?", batch.ID); err != nil {
		t.Fatal(err)
	}
	newToken := "worker-after-restart"
	claimed, err = store.PublishBatches.ClaimBatch(ctx, batch.ID, newToken, time.Now().Add(time.Minute).Unix())
	if err != nil || !claimed {
		t.Fatalf("新 worker 接管批次失败: claimed=%v err=%v", claimed, err)
	}

	recovered, err := store.PublishBatches.Rows(ctx, batch.ID)
	if err != nil || len(recovered) != 2 {
		t.Fatalf("读取接管后的批次行失败: rows=%+v err=%v", recovered, err)
	}
	unknown := recovered[0]
	if unknown.Status != "failed" || unknown.FailureKind != "uncertain_remote" || unknown.WorkerToken != "" {
		t.Fatalf("远端结果未知的行未被隔离: %+v", unknown)
	}
	checkpoint := recovered[1]
	if checkpoint.Status != "pending" || checkpoint.FailureKind != "interrupted" ||
		checkpoint.ItemID != "remote-item-2" || checkpoint.ItemURL == "" || checkpoint.RawJSON == "" {
		t.Fatalf("已保存 ItemID 的行未保留恢复检查点: %+v", checkpoint)
	}

	// uncertain_remote 不得被普通失败重试入口重新置为 pending。
	if err := store.PublishBatches.ResetFailed(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	afterReset, err := store.PublishBatches.Rows(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReset[0].Status != "failed" || afterReset[0].FailureKind != "uncertain_remote" {
		t.Fatalf("普通失败重试错误地重放未知远端行: %+v", afterReset[0])
	}

	// 已有 ItemID 的恢复行可以由新 worker 领取并完成本地后处理收口。
	claimed, err = store.PublishBatches.ClaimRow(ctx, checkpointRowID, newToken)
	if err != nil || !claimed {
		t.Fatalf("新 worker 无法领取已有远端结果的恢复行: claimed=%v err=%v", claimed, err)
	}
	marked, err := store.PublishBatches.MarkClaimedRowSuccess(ctx, checkpointRowID, newToken,
		checkpoint.ItemID, checkpoint.ItemURL, checkpoint.RawJSON)
	if err != nil || !marked {
		t.Fatalf("恢复行最终收口失败: marked=%v err=%v", marked, err)
	}
	finalRows, err := store.PublishBatches.Rows(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalRows[1].Status != "success" || finalRows[1].ItemID != "remote-item-2" || finalRows[1].FailureKind != "" {
		t.Fatalf("恢复行最终状态异常: %+v", finalRows[1])
	}
}
