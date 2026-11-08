package items

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ledgerTestFake 提供应用层发布账本的状态条件更新测试替身。
type ledgerTestFake struct {
	// mu 保护并发测试访问的操作快照。
	mu         sync.Mutex
	// operations 保存测试用户的发布操作快照。
	operations map[string]PublishOperation
}

func newLedgerTestFake() *ledgerTestFake {
	return &ledgerTestFake{operations: make(map[string]PublishOperation)}
}

func (l *ledgerTestFake) CreatePrepared(_ context.Context, operation PublishOperation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.operations[operation.OperationID]; exists {
		return errors.New("duplicate operation")
	}
	l.operations[operation.OperationID] = operation
	return nil
}

func (l *ledgerTestFake) GetForUser(_ context.Context, userID int64, operationID string) (PublishOperation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	operation, exists := l.operations[operationID]
	if !exists || operation.UserID != userID {
		return PublishOperation{}, errors.New("not found")
	}
	return operation, nil
}

func (l *ledgerTestFake) MarkStarted(_ context.Context, userID int64, operationID string) error {
	return l.transition(userID, operationID, "prepared", "started", nil)
}

func (l *ledgerTestFake) MarkRemoteConfirmed(_ context.Context, userID int64, operationID, itemID, itemURL, rawJSON string) error {
	return l.transition(userID, operationID, "started", "remote_confirmed", func(operation *PublishOperation) {
		operation.ItemID, operation.ItemURL, operation.RawJSON = itemID, itemURL, rawJSON
	})
}

func (l *ledgerTestFake) MarkLocalConfirmed(_ context.Context, userID int64, operationID string) error {
	return l.transition(userID, operationID, "remote_confirmed", "local_confirmed", nil)
}

func (l *ledgerTestFake) MarkUnknown(_ context.Context, userID int64, operationID, message string) error {
	return l.transition(userID, operationID, "started", "unknown", func(operation *PublishOperation) {
		operation.ErrorMessage = message
	})
}

func (l *ledgerTestFake) transition(userID int64, operationID, from, to string, update func(*PublishOperation)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	operation, exists := l.operations[operationID]
	if !exists || operation.UserID != userID || operation.State != from {
		return errors.New("state changed")
	}
	if update != nil {
		update(&operation)
	}
	operation.State = to
	l.operations[operationID] = operation
	return nil
}

// ledgerPublisherFake 记录平台发布次数，并按测试场景模拟闸门和远端结果。
type ledgerPublisherFake struct {
	// calls 记录实际进入平台端口的调用次数。
	calls      int
	// result 是模拟的平台发布成功结果。
	result     *PublishResult
	// publishErr 是模拟的平台调用错误。
	publishErr error
	// skipGate 表示模拟图片准备阶段失败，尚未通过最终创建请求闸门。
	skipGate   bool
}

func (p *ledgerPublisherFake) Publish(ctx context.Context, input PublishInput) (PublishOutcome, error) {
	if !p.skipGate && input.BeforePublish != nil {
		if err := input.BeforePublish(ctx); err != nil {
			return PublishOutcome{}, err
		}
	}
	p.calls++
	if p.publishErr != nil {
		return PublishOutcome{}, p.publishErr
	}
	return PublishOutcome{Result: p.result}, nil
}

// ledgerRepositoryFake 模拟本地商品 Upsert，并允许指定次数失败。
type ledgerRepositoryFake struct {
	// calls 记录本地商品 Upsert 次数。
	calls    int
	// failures 保存接下来需要模拟的 Upsert 失败次数。
	failures int
}

func (r *ledgerRepositoryFake) Upsert(_ context.Context, _ ItemRecord) error {
	r.calls++
	if r.failures > 0 {
		r.failures--
		return errors.New("local persistence failed")
	}
	return nil
}

// TestSinglePublishLedgerUnknownNeverReplays 验证最终创建闸门通过后的未知结果不会自动重发。
func TestSinglePublishLedgerUnknownNeverReplays(t *testing.T) {
	// ledger 保存持久化操作测试替身。
	ledger := newLedgerTestFake()
	// publisher 保存会在最终创建闸门后返回错误的平台替身。
	publisher := &ledgerPublisherFake{publishErr: errors.New("connection lost")}
	// repository 保存本地商品仓储替身。
	repository := &ledgerRepositoryFake{}
	// service 保存启用持久化操作账本的应用服务。
	service, err := NewServiceWithOperationLedger(publisher, repository, ledger)
	if err != nil {
		t.Fatalf("NewServiceWithOperationLedger error: %v", err)
	}
	// input 保存本次测试使用的稳定操作标识和指纹。
	input := PublishInput{UserID: 7, CookieID: "account-7", OperationID: "op-unknown", RequestFingerprint: "fp-a", Title: "test"}
	_, firstErr := service.PublishSingle(context.Background(), input)
	if firstErr == nil {
		t.Fatal("first publish should report the remote error")
	}
	// operation 保存首次调用后的持久化状态。
	operation, err := ledger.GetForUser(context.Background(), 7, "op-unknown")
	if err != nil || operation.State != "unknown" {
		t.Fatalf("operation=%+v err=%v want unknown", operation, err)
	}
	_, secondErr := service.PublishSingle(context.Background(), input)
	if !errors.Is(secondErr, ErrPublishOperationUncertain) {
		t.Fatalf("second error=%v want ErrPublishOperationUncertain", secondErr)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls=%d want 1", publisher.calls)
	}
}

// TestSinglePublishLedgerRecoversLocalSave 验证远端确认后本地失败可恢复，且不会再次调用平台。
func TestSinglePublishLedgerRecoversLocalSave(t *testing.T) {
	// ledger 保存持久化操作测试替身。
	ledger := newLedgerTestFake()
	// publisher 保存返回固定远端商品结果的平台替身。
	publisher := &ledgerPublisherFake{result: &PublishResult{ItemID: "item-123", ItemURL: "https://example.invalid/item-123", Title: "test"}}
	// repository 首次失败、第二次成功，用于模拟本地数据库短暂故障。
	repository := &ledgerRepositoryFake{failures: 1}
	// service 保存启用持久化操作账本的应用服务。
	service, err := NewServiceWithOperationLedger(publisher, repository, ledger)
	if err != nil {
		t.Fatalf("NewServiceWithOperationLedger error: %v", err)
	}
	// input 保存本次测试使用的稳定操作标识和指纹。
	input := PublishInput{UserID: 7, CookieID: "account-7", OperationID: "op-recover", RequestFingerprint: "fp-a", Title: "test"}
	first, err := service.PublishSingle(context.Background(), input)
	if err != nil || first.LocalSaveErr == nil {
		t.Fatalf("first outcome=%+v err=%v want local save error", first, err)
	}
	// firstState 保存首次发布后已确认的远端状态。
	firstState, err := ledger.GetForUser(context.Background(), 7, "op-recover")
	if err != nil || firstState.State != "remote_confirmed" {
		t.Fatalf("operation=%+v err=%v want remote_confirmed", firstState, err)
	}
	second, err := service.PublishSingle(context.Background(), input)
	if err != nil || second.LocalSaveErr != nil || second.Result == nil || second.Result.ItemID != "item-123" {
		t.Fatalf("second outcome=%+v err=%v", second, err)
	}
	// finalState 保存本地收口后的操作状态。
	finalState, err := ledger.GetForUser(context.Background(), 7, "op-recover")
	if err != nil || finalState.State != "local_confirmed" {
		t.Fatalf("operation=%+v err=%v want local_confirmed", finalState, err)
	}
	if publisher.calls != 1 || repository.calls != 2 {
		t.Fatalf("publisher calls=%d repository calls=%d want 1 and 2", publisher.calls, repository.calls)
	}
}

// TestSinglePublishLedgerRejectsFingerprintReuse 验证同一操作 ID 不允许绑定不同商品内容。
func TestSinglePublishLedgerRejectsFingerprintReuse(t *testing.T) {
	// ledger 保存持久化操作测试替身。
	ledger := newLedgerTestFake()
	// publisher 保存平台发布替身。
	publisher := &ledgerPublisherFake{result: &PublishResult{ItemID: "item-123"}}
	// repository 保存本地商品仓储替身。
	repository := &ledgerRepositoryFake{}
	// service 保存启用持久化操作账本的应用服务。
	service, err := NewServiceWithOperationLedger(publisher, repository, ledger)
	if err != nil {
		t.Fatalf("NewServiceWithOperationLedger error: %v", err)
	}
	// input 保存首次请求的稳定操作标识和内容指纹。
	input := PublishInput{UserID: 7, CookieID: "account-7", OperationID: "op-fingerprint", RequestFingerprint: "fp-a"}
	if _, err := service.PublishSingle(context.Background(), input); err != nil {
		t.Fatalf("first publish error: %v", err)
	}
	// changedInput 保存复用同一操作 ID 但改变内容指纹的请求。
	changedInput := input
	changedInput.RequestFingerprint = "fp-b"
	_, err = service.PublishSingle(context.Background(), changedInput)
	if !errors.Is(err, ErrPublishOperationFingerprintMismatch) {
		t.Fatalf("error=%v want ErrPublishOperationFingerprintMismatch", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls=%d want 1", publisher.calls)
	}
}
