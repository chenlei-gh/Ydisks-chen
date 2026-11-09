package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"xianyu-go/internal/db"
)

// TestOpenAPISinglePublishOperationStatus 验证真实操作状态查询响应符合 OpenAPI 契约。
func TestOpenAPISinglePublishOperationStatus(t *testing.T) {
	// srv、store、cleanup 保存真实 HTTP 服务、持久化仓储和资源释放函数。
	srv, store, cleanup := newTestServer(t)
	defer cleanup()
	// ctx 是创建测试操作记录时使用的数据库上下文。
	ctx := context.Background()
	// admin、adminErr 保存固定管理员用户及读取错误。
	admin, adminErr := store.Users.GetByUsername(ctx, "admin")
	if adminErr != nil {
		t.Fatalf("读取测试管理员失败: %v", adminErr)
	}
	// createErr 保存用于状态查询的 prepared 操作记录创建结果。
	if createErr := store.SinglePublishOperations.CreatePrepared(ctx, db.SinglePublishOperation{
		OperationID: "contract-publish-operation", UserID: admin.ID, CookieID: "acc1",
		RequestFingerprint: "contract-fingerprint", State: "prepared",
	}); createErr != nil {
		t.Fatalf("创建发布操作记录失败: %v", createErr)
	}
	// handler 是当前测试使用的完整真实路由树。
	handler := srv.Router()
	// sessionCookie 是管理员登录后访问受保护操作状态接口的认证会话。
	sessionCookie := loginHelper(t, handler)
	// request 是查询已创建操作状态的版本化 HTTP 请求。
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items/publish-operations/contract-publish-operation", nil)
	request.AddCookie(sessionCookie)
	// recorder 保存真实 handler 的状态、响应头和 JSON 响应体。
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assertOpenAPISuccessResponse(t, request, recorder)
}
