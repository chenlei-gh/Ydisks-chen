package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"xianyu-go/internal/application/items"
	"xianyu-go/internal/auth"
)

// singlePublishOperationResponse 是单商品发布状态查询的稳定 HTTP 响应 DTO。
type singlePublishOperationResponse struct {
	// OperationID 是当前用户查询的发布操作标识。
	OperationID string `json:"operation_id"`
	// State 是持久化账本中的当前发布阶段。
	State string `json:"state"`
	// ItemID 是平台确认成功后返回的商品 ID。
	ItemID string `json:"item_id"`
	// ItemURL 是平台确认成功后返回的商品链接。
	ItemURL string `json:"item_url"`
}

// prepareSinglePublishOperation 为单商品发布请求补齐稳定操作 ID 和服务端内容指纹。
func (s *Server) prepareSinglePublishOperation(r *http.Request, input *items.PublishInput) (int, string, error) {
	// operationID 保存客户端复用的稳定标识；旧客户端缺失时由服务端生成兼容标识。
	operationID := strings.TrimSpace(r.FormValue("operation_id"))
	if operationID == "" {
		// generatedID、idErr 保存旧客户端兼容使用的随机操作 ID 及生成错误。
		generatedID, idErr := newSinglePublishOperationID()
		if idErr != nil {
			return http.StatusInternalServerError, "生成发布操作标识失败", idErr
		}
		operationID = generatedID
	}
	// fingerprint、fingerprintErr 保存规范化商品请求指纹及计算错误。
	fingerprint, fingerprintErr := fingerprintSinglePublishRequest(*input)
	if fingerprintErr != nil {
		return http.StatusBadRequest, "计算发布请求指纹失败", fingerprintErr
	}
	input.OperationID = operationID
	input.RequestFingerprint = fingerprint
	return 0, "", nil
}

// getSinglePublishOperation 返回当前登录用户拥有的单商品发布操作状态。
func (s *Server) getSinglePublishOperation(w http.ResponseWriter, r *http.Request) {
	// session 保存认证中间件提供的当前用户会话。
	session := auth.SessionFromContext(r.Context())
	// operationID 是待查询的稳定发布操作标识。
	operationID := strings.TrimSpace(chi.URLParam(r, "operation_id"))
	if operationID == "" {
		writeErr(w, http.StatusBadRequest, "缺少发布操作 ID")
		return
	}
	// statusService 是应用服务提供的用户隔离操作状态查询能力。
	statusService, ok := s.itemSinglePublishApplication().(interface {
		GetPublishOperation(context.Context, int64, string) (items.PublishOperation, error)
	})
	if !ok {
		writeErr(w, http.StatusInternalServerError, "发布操作状态查询未初始化")
		return
	}
	// operation、err 保存按用户归属查询的持久化状态及查询错误。
	operation, err := statusService.GetPublishOperation(r.Context(), session.UserID, operationID)
	if err != nil {
		// 不区分不存在和其他用户的记录，避免通过状态接口探测他人操作。
		writeErr(w, http.StatusNotFound, "发布操作不存在")
		return
	}
	writeJSON(w, http.StatusOK, singlePublishOperationResponse{
		OperationID: operation.OperationID,
		State:       operation.State,
		ItemID:      operation.ItemID,
		ItemURL:     operation.ItemURL,
	})
}

// writeSinglePublishCallError 将应用层单商品发布错误映射为稳定的 HTTP 错误响应。
func (s *Server) writeSinglePublishCallError(w http.ResponseWriter, cookieID string, callErr error) {
	// publishErr 保存可映射为专用 HTTP 状态码的应用层发布错误。
	var publishErr *items.PublishError
	if errors.Is(callErr, items.ErrPublishResultMissingItemID) {
		writeErrCode(w, http.StatusBadGateway, "publish_result_missing_item_id", callErr.Error(), "")
		return
	}
	if errors.Is(callErr, items.ErrPublishOperationUncertain) {
		writeErrCode(w, http.StatusBadGateway, "publish_result_uncertain", callErr.Error(), "")
		return
	}
	if errors.As(callErr, &publishErr) {
		// status 保存专用发布错误对应的 HTTP 状态码。
		status := http.StatusBadGateway
		// message 保存面向用户的发布错误说明。
		message := publishErr.Error()
		if publishErr.Code == items.PublishErrorStockPermissionMissing {
			status = http.StatusForbidden
			message = "该账号没有库存发布权限，无法按库存数量发布商品"
		}
		writeErrCode(w, status, string(publishErr.Code), message, "")
		return
	}
	if strings.Contains(callErr.Error(), "账号凭证已变化") {
		writeErr(w, http.StatusConflict, callErr.Error())
		return
	}
	// message 保存未包装的平台或基础设施错误，避免前端误报发布成功。
	message := strings.TrimSpace(callErr.Error())
	if message == "" {
		message = "商品发布失败"
	}
	if s.Logger != nil {
		s.Logger.Error("商品发布失败", "cookie_id", cookieID, "err", callErr)
	}
	writeErrCode(w, http.StatusBadGateway, "publish_failed", message, "")
}
