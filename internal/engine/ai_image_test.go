package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xianyu-go/internal/netguard"
)

// buyerPNGForTest 构造指定尺寸的 PNG 图片，供下载和像素上限测试复用。
func buyerPNGForTest(t *testing.T, width, height int) []byte {
	t.Helper()
	// source 是指定尺寸的灰度测试图片，零值像素可获得较高压缩率。
	source := image.NewGray(image.Rect(0, 0, width, height))
	// encoded 保存 PNG 编码结果。
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("编码测试 PNG: %v", err)
	}
	return encoded.Bytes()
}

// withBuyerImageHTTPClient 在单个测试中替换下载客户端，并在测试结束后恢复公网安全客户端。
func withBuyerImageHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	// original 保存生产默认客户端工厂。
	original := newBuyerImageHTTPClient
	newBuyerImageHTTPClient = func() *http.Client { return client }
	t.Cleanup(func() { newBuyerImageHTTPClient = original })
}

// TestExtractBuyerImageURLOnlyAcceptsImageMessages 验证只从 contentType=2 的消息中提取图片地址。
func TestExtractBuyerImageURLOnlyAcceptsImageMessages(t *testing.T) {
	// message 是带图片正文的买家入站消息。
	message := ChatMessage{Raw: map[string]any{
		"1":     map[string]any{"10": map[string]any{"extJson": `{"contentType":"2"}`}},
		"image": map[string]any{"pics": []any{map[string]any{"url": "https://images.example/buyer.png"}}},
	}}
	if got := extractBuyerImageURL(message); got != "https://images.example/buyer.png" {
		t.Fatalf("图片消息 URL=%q", got)
	}
	// 非图片消息即使包含形似图片的 URL，也不得进入图片下载链。
	message.Raw["1"].(map[string]any)["10"].(map[string]any)["extJson"] = `{"contentType":"1"}`
	if got := extractBuyerImageURL(message); got != "" {
		t.Fatalf("文本消息不应提取图片 URL: %q", got)
	}
}

// TestExtractChatMessageRetainsImageOnlyBuyerMessage 验证无提醒文本的买家图片仍进入消息分发模型。
func TestExtractChatMessageRetainsImageOnlyBuyerMessage(t *testing.T) {
	// raw 是仅包含图片正文、没有 reminderContent 的平台入站帧。
	raw := map[string]any{
		"1": map[string]any{
			"2": "chat-image-only@goofish",
			"10": map[string]any{
				"extJson":      `{"contentType":"2"}`,
				"senderUserId": "buyer-1",
				"reminderUrl":  "https://www.goofish.com/item?id=item-1",
			},
		},
		"image": map[string]any{"pics": []any{map[string]any{"url": "https://images.example/buyer.png"}}},
	}
	// message 是平台帧归一后的买家消息。
	message := extractChatMessage(raw, "account-1", "unb=seller-1;")
	if message == nil || message.Text != "" || message.ChatID != "chat-image-only" {
		t.Fatalf("图片消息未保留或字段错误: %+v", message)
	}
	if !isBuyerImageMessage(*message) || extractBuyerImageURL(*message) != "https://images.example/buyer.png" {
		t.Fatalf("图片消息类型或 URL 提取错误: %+v", message)
	}
}

// TestDownloadBuyerImageDataURLValidatesPNG 验证 PNG 按真实字节识别并转换为 data URL。
func TestDownloadBuyerImageDataURLValidatesPNG(t *testing.T) {
	// data 是小尺寸合法 PNG 的编码字节。
	data := buyerPNGForTest(t, 2, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	withBuyerImageHTTPClient(t, server.Client())
	// result 是通过图片格式、字节数和像素数校验后的 data URL。
	result, err := downloadBuyerImageDataURL(context.Background(), server.URL+"/buyer")
	if err != nil {
		t.Fatalf("下载 PNG: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Fatalf("PNG data URL 前缀错误: %q", result[:min(len(result), 40)])
	}
	// decoded 验证 data URL 中的字节仍与原始 PNG 完全一致。
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("data URL 图片字节不一致: err=%v", err)
	}
}

// TestDownloadBuyerImageDataURLRejectsInvalidAndOversizedImages 验证无法识别的图片和超大响应会被拒绝。
func TestDownloadBuyerImageDataURLRejectsInvalidAndOversizedImages(t *testing.T) {
	// cases 保存远端响应体及其预期拒绝原因。
	cases := []struct {
		name string
		body []byte
	}{
		{name: "invalid format", body: []byte("not an image")},
		{name: "oversized", body: bytes.Repeat([]byte("x"), int(buyerImageMaxBytes+1))},
		{name: "pixel limit", body: buyerPNGForTest(t, 4001, 4000)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(testCase.body)
			}))
			defer server.Close()
			withBuyerImageHTTPClient(t, server.Client())
			if _, err := downloadBuyerImageDataURL(context.Background(), server.URL); err == nil {
				t.Fatal("无效或超限图片应被拒绝")
			}
		})
	}
}

// TestDownloadBuyerImageDataURLRejectsLoopback 验证买家图片下载始终使用公网访问策略。
func TestDownloadBuyerImageDataURLRejectsLoopback(t *testing.T) {
	// data 是有效图片字节；若公网策略失效，测试服务器会成功返回该图片。
	data := buyerPNGForTest(t, 1, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()
	// newBuyerImageHTTPClient 使用生产公网客户端，真实验证回环目标在有服务监听时仍被拒绝。
	withBuyerImageHTTPClient(t, netguard.PublicHTTPClient(buyerImageTimeout))
	if _, err := downloadBuyerImageDataURL(context.Background(), server.URL); err == nil {
		t.Fatal("回环地址必须被公网策略拒绝")
	}
}

// TestBuyerImageFallbackTextPreventsGuessing 验证视觉能力不可用时明确禁止文本模型猜测图片。
func TestBuyerImageFallbackTextPreventsGuessing(t *testing.T) {
	// fallback 是视觉模型缺失时附加到当前用户消息的安全说明。
	fallback := buyerImageFallbackText("帮我看看", "尚未配置视觉模型")
	if !strings.Contains(fallback, "帮我看看") || !strings.Contains(fallback, "请不要猜测图片内容") {
		t.Fatalf("图片降级说明不完整: %q", fallback)
	}
}
