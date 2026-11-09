package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xianyu-go/internal/netguard"
)

const (
	buyerImageMaxBytes  int64 = 5 << 20
	buyerImageMaxPixels int64 = 16_000_000
	buyerImageTimeout         = 8 * time.Second
)

var newBuyerImageHTTPClient = func() *http.Client {
	return netguard.PublicHTTPClient(buyerImageTimeout)
}

// extractBuyerImageURL 仅从 contentType=2 的买家入站消息中提取第一张图片地址。
func extractBuyerImageURL(message ChatMessage) string {
	if message.Raw == nil {
		return ""
	}
	// messageBody 和 messageExtra 保存平台消息主体与扩展字段。
	messageBody, _ := message.Raw["1"].(map[string]any)
	messageExtra, _ := messageBody["10"].(map[string]any)
	if messageContentType(messageBody, messageExtra) != "2" {
		return ""
	}
	return extractImageObservationContent(message.Raw)
}

// downloadBuyerImageDataURL 下载并验证买家图片，返回只在当前 AI 请求中使用的 data URL。
func downloadBuyerImageDataURL(ctx context.Context, rawURL string) (string, error) {
	// parsedURL 是校验过协议、主机和 userinfo 的买家图片地址。
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Hostname() == "" || parsedURL.User != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return "", fmt.Errorf("买家图片地址无效")
	}
	// requestCtx 限制图片下载耗时，不改变后续模型请求的独立超时边界。
	requestCtx, cancel := context.WithTimeout(ctx, buyerImageTimeout)
	defer cancel()
	// request 是绑定消息生命周期的图片读取请求。
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("创建买家图片请求失败")
	}
	// client 强制使用公网访问策略，拒绝私网、回环地址和跨主机重定向。
	client := newBuyerImageHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("下载买家图片失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载买家图片返回 HTTP %d", response.StatusCode)
	}
	if response.ContentLength > buyerImageMaxBytes {
		return "", fmt.Errorf("买家图片超过 5 MiB 限制")
	}
	// data 是受硬上限约束的原始图片字节。
	data, err := io.ReadAll(io.LimitReader(response.Body, buyerImageMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取买家图片失败")
	}
	if len(data) == 0 || int64(len(data)) > buyerImageMaxBytes {
		return "", fmt.Errorf("买家图片大小必须在 1 B 到 5 MiB 之间")
	}
	// config 和 format 由图片字节本身解码，不信任远端 Content-Type 声明。
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("买家图片格式无法识别")
	}
	if format != "jpeg" && format != "png" {
		return "", fmt.Errorf("买家图片仅支持 JPEG 或 PNG")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > buyerImageMaxPixels {
		return "", fmt.Errorf("买家图片尺寸无效或超过 16 MP")
	}
	// mime 是按已验证图片格式生成的媒体类型。
	mime := "image/jpeg"
	if format == "png" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
