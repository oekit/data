package rediscache

import (
	"github.com/redis/go-redis/v9"

	src "github.com/oekit/redisx/cache"
)

// NewRedisCache 创建 Redis 分布式缓存。
func NewRedisCache[T any](client redis.UniversalClient, cfg RedisConfig[T]) *RedisCache[T] {
	return src.NewRedisCache(client, cfg)
}
