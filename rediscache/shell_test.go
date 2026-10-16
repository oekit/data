package rediscache_test

import (
	"testing"

	"github.com/oekit/data/internal/testsame"
	"github.com/oekit/data/rediscache"
	pkgcache "github.com/oekit/pkg/cache"
	redisxcache "github.com/oekit/redisx/cache"
)

// TestTypeIdentity 构造（nil client 不触网）+ 双向赋值 + 接口满足。
func TestTypeIdentity(t *testing.T) {
	c := rediscache.NewRedisCache(nil, redisxcache.RedisConfig[int]{Prefix: "shell:"})

	testsame.Assert[*redisxcache.RedisCache[int]](c) // 门面 → 源

	bwd := redisxcache.NewRedisCache(nil, redisxcache.RedisConfig[int]{Prefix: "shell:"})
	testsame.Assert[*rediscache.RedisCache[int]](bwd) // 源 → 门面
	testsame.Assert[pkgcache.Cache[int]](bwd)         // 接口满足
}
