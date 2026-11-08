package items

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PublishOperation 是应用层可见的单商品发布检查点，不暴露数据库模型。
type PublishOperation struct {
	OperationID        string
	UserID             int64
	CookieID           string
	RequestFingerprint string
	State              string
	ItemID             string
	ItemURL            string
	RawJSON            string
	ErrorMessage       string
}

// PublishOperationLedger 是单商品发布所需的最小持久化端口。
type PublishOperationLedger interface {
	CreatePrepared(context.Context, PublishOperation) error
	GetForUser(context.Context, int64, string) (PublishOperation, error)
	MarkStarted(context.Context, int64, string) error
	MarkRemoteConfirmed(context.Context, int64, string, string, string, string, ...string) error
	MarkLocalConfirmed(context.Context, int64, string, ...string) error
	MarkUnknown(context.Context, int64, string, string) error
}

// ErrPublishOperationUncertain 表示远端结果不明确，禁止再次发送创建请求。
var ErrPublishOperationUncertain = errors.New("商品发布结果尚未确认，请查询操作状态或人工核对，禁止自动重发")

// ErrPublishOperationFingerprintMismatch 表示同一操作 ID 被不同发布内容复用。
var ErrPublishOperationFingerprintMismatch = errors.New("发布操作 ID 已绑定其他商品内容")

// publishSingleWithLedger 按持久化状态决定执行远端发布、恢复本地收尾或返回已确认结果。
func (svc *Service) publishSingleWithLedger(ctx context.Context, input PublishInput) (PublishOutcome, error) {
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.RequestFingerprint = strings.TrimSpace(input.RequestFingerprint)
	if input.OperationID == "" || input.RequestFingerprint == "" {
		return PublishOutcome{}, errors.New("缺少单商品发布操作 ID 或请求指纹")
	}
	if len(input.SKUs) > 0 {
		// totalQuantity 汇总 SKU 库存，保证恢复路径与首次发布使用相同数量。
		totalQuantity := 0
		// sku 表示当前待汇总的规格组合。
		for _, sku := range input.SKUs {
			totalQuantity += sku.Quantity
		}
		input.Quantity = totalQuantity
	}
	// createErr 保存首次创建检查点的错误；重复 ID 时通过读取现有记录判断是否为安全重试。
	createErr := svc.operations.CreatePrepared(ctx, PublishOperation{
		OperationID: input.OperationID, UserID: input.UserID, CookieID: input.CookieID,
		RequestFingerprint: input.RequestFingerprint, State: "prepared",
	})
	// operation 保存本用户当前操作 ID 对应的持久化快照。
	operation, readErr := svc.operations.GetForUser(ctx, input.UserID, input.OperationID)
	if readErr != nil {
		if createErr != nil {
			return PublishOutcome{}, errors.Join(createErr, readErr)
		}
		return PublishOutcome{}, fmt.Errorf("读取新建的发布操作失败: %w", readErr)
	}
	if operation.CookieID != input.CookieID || operation.RequestFingerprint != input.RequestFingerprint {
		return PublishOutcome{}, ErrPublishOperationFingerprintMismatch
	}
	switch operation.State {
	case "local_confirmed":
		// result 保存上次已经完成本地收口的发布结果。
		result, decodeErr := decodePublishOperationResult(operation)
		if decodeErr != nil {
			return PublishOutcome{}, decodeErr
		}
		// outcome 保存已完成发布结果及需要继续提示的 Cookie 错误。
		// outcome 保存远端已确认后恢复本地写入的结果及错误状态。
		outcome := PublishOutcome{Result: result}
		if operation.ErrorMessage != "" {
			outcome.ResponseCookieErr = errors.New(operation.ErrorMessage)
		}
		return outcome, nil
	case "remote_confirmed":
		// result 保存远端已确认、只需恢复本地商品写入的结果。
		result, decodeErr := decodePublishOperationResult(operation)
		if decodeErr != nil {
			return PublishOutcome{}, decodeErr
		}
		// outcome 保存远端已确认后恢复本地写入的结果及错误状态。
		outcome := PublishOutcome{Result: result}
		if operation.ErrorMessage != "" {
			outcome.ResponseCookieErr = errors.New(operation.ErrorMessage)
		}
		outcome.LocalSaveErr = svc.savePublishedItem(ctx, input, result)
		if outcome.LocalSaveErr != nil {
			return outcome, nil
		}
		// err 保存远端确认后本地收口状态写入结果。
		if err := svc.operations.MarkLocalConfirmed(ctx, input.UserID, input.OperationID, operation.ErrorMessage); err != nil {
			return outcome, fmt.Errorf("本地商品已保存，但操作账本收口失败: %w", err)
		}
		return outcome, nil
	case "started", "unknown":
		return PublishOutcome{}, ErrPublishOperationUncertain
	case "prepared":
		// 只有尚未通过最终创建请求闸门的 prepared 状态允许再次执行发布。
	default:
		return PublishOutcome{}, fmt.Errorf("未知的单商品发布操作状态: %s", operation.State)
	}
	// gatePassed 记录最终商品创建请求闸门是否已将操作持久化为 started。
	gatePassed := false
	// publishInput 将持久化闸门与既有平台回调串联，确保账本先于最终创建请求提交。
	publishInput := input
	publishInput.BeforePublish = func(hookCtx context.Context) error {
		// err 保存持久化发布闸门推进结果。
		if err := svc.operations.MarkStarted(hookCtx, input.UserID, input.OperationID); err != nil {
			return err
		}
		if input.BeforePublish != nil {
			// err 保存原有平台创建请求前置校验结果。
			if err := input.BeforePublish(hookCtx); err != nil {
				// MarkStarted 已成功但下游闸门拒绝；回滚 started 不安全，转为 unknown 隔离。
				_ = svc.operations.MarkUnknown(hookCtx, input.UserID, input.OperationID, err.Error())
				return err
			}
		}
		gatePassed = true
		return nil
	}
	// outcome、publishErr 保存平台发布结果及调用错误。
	outcome, publishErr := svc.publisher.Publish(ctx, publishInput)
	if publishErr != nil {
		if gatePassed {
			// unknownErr 保存最终创建请求已发出但平台调用未能确认结果时的隔离错误。
			unknownErr := svc.operations.MarkUnknown(ctx, input.UserID, input.OperationID, publishErr.Error())
			if unknownErr != nil {
				return outcome, errors.Join(publishErr, fmt.Errorf("隔离未知远端结果失败: %w", unknownErr))
			}
		}
		return outcome, publishErr
	}
	if outcome.Result == nil || strings.TrimSpace(outcome.Result.ItemID) == "" {
		if gatePassed {
			// unknownErr 保存平台响应缺少商品 ID 时的隔离错误。
			unknownErr := svc.operations.MarkUnknown(ctx, input.UserID, input.OperationID, "平台响应缺少商品 ID")
			if unknownErr != nil {
				return outcome, fmt.Errorf("平台结果缺少商品 ID，且隔离操作失败: %w", unknownErr)
			}
		}
		return outcome, ErrPublishOperationUncertain
	}
	// resultJSON 序列化完整应用层结果，使进程重启后可以只恢复本地收尾。
	resultJSON, marshalErr := json.Marshal(outcome.Result)
	if marshalErr != nil {
		_ = svc.operations.MarkUnknown(ctx, input.UserID, input.OperationID, marshalErr.Error())
		return outcome, fmt.Errorf("序列化平台发布结果失败: %w", marshalErr)
	}
	// remoteErr 保存远端成功结果检查点写入失败原因；此时不得再发起新的远端创建请求。
	// cookieWarning 保存平台成功后 Cookie 更新未能持久化的提示，恢复时仍需返回给用户。
	cookieWarning := ""
	if outcome.ResponseCookieErr != nil {
		cookieWarning = outcome.ResponseCookieErr.Error()
	}
	// remoteErr 保存平台成功结果检查点写入失败原因。
	if remoteErr := svc.operations.MarkRemoteConfirmed(ctx, input.UserID, input.OperationID, outcome.Result.ItemID, outcome.Result.ItemURL, string(resultJSON), cookieWarning); remoteErr != nil {
		return outcome, fmt.Errorf("平台已返回商品 ID %s，但持久化远端结果失败，禁止重发: %w", outcome.Result.ItemID, remoteErr)
	}
	outcome.LocalSaveErr = svc.savePublishedItem(ctx, input, outcome.Result)
	if outcome.LocalSaveErr != nil {
		return outcome, nil
	}
	// err 保存本地商品写入完成后的账本收口结果。
	if err := svc.operations.MarkLocalConfirmed(ctx, input.UserID, input.OperationID, cookieWarning); err != nil {
		return outcome, fmt.Errorf("本地商品已保存，但操作账本收口失败: %w", err)
	}
	return outcome, nil
}

// decodePublishOperationResult 从远端检查点恢复应用层发布结果。
func decodePublishOperationResult(operation PublishOperation) (*PublishResult, error) {
	// result 保存从检查点恢复的应用层商品结果。
	var result PublishResult
	// err 保存远端检查点 JSON 反序列化错误。
	if err := json.Unmarshal([]byte(operation.RawJSON), &result); err != nil {
		return nil, fmt.Errorf("解析已确认的商品发布结果失败: %w", err)
	}
	if result.ItemID == "" {
		result.ItemID = operation.ItemID
	}
	if result.ItemURL == "" {
		result.ItemURL = operation.ItemURL
	}
	if strings.TrimSpace(result.ItemID) == "" {
		return nil, ErrPublishOperationUncertain
	}
	return &result, nil
}

// savePublishedItem 只执行本地 Upsert；重复执行不会再次触发远端商品创建。
func (svc *Service) savePublishedItem(ctx context.Context, input PublishInput, result *PublishResult) error {
	// detail 是供商品详情页使用的本地扩展信息。
	detail := map[string]any{
		"item_image": result.ImageURL, "web_url": result.ItemURL,
		"category_name": result.CategoryName, "quantity": result.Quantity,
		"publish_raw": result.RawData,
	}
	// detailJSON 保存本地商品扩展信息的 JSON 表示。
	detailJSON, _ := json.Marshal(detail)
	return svc.repository.Upsert(ctx, ItemRecord{
		CookieID: input.CookieID, ItemID: result.ItemID, ItemTitle: result.Title,
		ItemDescription: input.Description, ItemCategory: result.CategoryID,
		ItemPrice: result.PriceText, ItemDetail: string(detailJSON),
		MultiQuantityDelivery: input.Quantity > 1, IsMultiSpec: len(input.Specs) > 0,
	})
}

// GetPublishOperation 读取当前用户可见的单商品发布操作状态。
func (svc *Service) GetPublishOperation(ctx context.Context, userID int64, operationID string) (PublishOperation, error) {
	if svc == nil || svc.operations == nil {
		return PublishOperation{}, errors.New("单商品发布操作账本未初始化")
	}
	return svc.operations.GetForUser(ctx, userID, operationID)
}
