package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Key 用冒号连接各部分生成缓存 key。
//
//	cache.Key("user", "123", "profile") // "user:123:profile"
func Key(parts ...string) string {
	return strings.Join(parts, ":")
}

// HashKey 基于参数生成 hash key，适用于复杂参数的缓存 key。
// 哈希取 SHA-256 前 16 位 hex（2026-09-15 治理轮：md5 → sha256，缓存 key
// 属非加密场景但无需为弱哈希留误用空间；摘要值较 v0.28.x 变化，key 为
// 进程/缓存生命周期内临时值，无持久化契约）。
//
//	cache.HashKey("search", "keyword", 1, 20) // "search:a1b2c3d4e5f6g7h8"
func HashKey(prefix string, args ...any) string {
	h := sha256.New()
	for _, arg := range args {
		fmt.Fprintf(h, "%v:", arg)
	}
	return prefix + ":" + hex.EncodeToString(h.Sum(nil))[:16]
}

// UserKey 生成用户维度的缓存 key。
//
//	cache.UserKey(123, "profile") // "user:123:profile"
func UserKey(userID int64, suffix string) string {
	return "user:" + strconv.FormatInt(userID, 10) + ":" + suffix
}

// TenantKey 生成租户维度的缓存 key。
//
//	cache.TenantKey("tenant1", "config") // "tenant:tenant1:config"
func TenantKey(tenantID, suffix string) string {
	return "tenant:" + tenantID + ":" + suffix
}
