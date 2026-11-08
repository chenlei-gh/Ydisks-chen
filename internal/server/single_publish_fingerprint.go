package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	itemapp "xianyu-go/internal/application/items"
)

// newSinglePublishOperationID 为未升级的旧客户端生成一次性兼容标识；新版客户端应持久化并复用自己的 ID。
func newSinglePublishOperationID() (string, error) {
	// random 保存本次操作 ID 的密码学随机字节。
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("生成商品发布操作 ID 失败: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

// fingerprintSinglePublishRequest 对商品字段、规格和图片内容摘要计算服务端请求指纹。
func fingerprintSinglePublishRequest(input itemapp.PublishInput) (string, error) {
	// imageHashes 保存商品图片的内容哈希，不保存图片内容到操作账本。
	imageHashes := make([]string, 0, len(input.Images))
	for _, image := range input.Images {
		// digest 保存单张图片内容的 SHA-256。
		digest := sha256.Sum256(image.Data)
		imageHashes = append(imageHashes, image.Filename+"|"+image.ContentType+"|"+hex.EncodeToString(digest[:]))
	}
	// specImageHashes 保存规格图片内容哈希。
	specImageHashes := make([]string, 0, len(input.SpecImages))
	for _, image := range input.SpecImages {
		// digest 保存当前规格图片内容的 SHA-256。
		digest := sha256.Sum256(image.Data)
		specImageHashes = append(specImageHashes, image.Filename+"|"+image.ContentType+"|"+hex.EncodeToString(digest[:]))
	}
	// canonical 保存会影响远端商品创建结果的规范化请求字段。
	canonical := struct {
		CookieID           string
		Title              string
		Description        string
		PriceCents         int64
		OriginalPriceCents int64
		Quantity           int
		PostageMode        string
		PostageCents       int64
		Location           *itemapp.Location
		Category           *itemapp.PublishCategory
		Images             []string
		Specs              []itemapp.PublishSpec
		SKUs               []itemapp.PublishSKU
		SpecImages         []string
	}{
		CookieID: input.CookieID, Title: input.Title, Description: input.Description,
		PriceCents: input.PriceCents, OriginalPriceCents: input.OriginalPriceCents,
		Quantity: input.Quantity, PostageMode: input.PostageMode, PostageCents: input.PostageCents,
		Location: input.Location, Category: input.Category, Images: imageHashes,
		Specs: input.Specs, SKUs: input.SKUs, SpecImages: specImageHashes,
	}
	// encoded 保存规范化请求的 JSON 字节。
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("规范化商品发布请求失败: %w", err)
	}
	// fingerprint 保存最终请求指纹。
	fingerprint := sha256.Sum256(encoded)
	return hex.EncodeToString(fingerprint[:]), nil
}
