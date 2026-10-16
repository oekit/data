package cache

import src "github.com/oekit/pkg/cache"

// NewLocalCache 创建本地缓存（泛型函数无法 var re-export，经包装转发）。
func NewLocalCache[T any](cfg LocalConfig[T]) *LocalCache[T] { return src.NewLocalCache(cfg) }

// NewSingleFlightCache 创建并发回源合并装饰器。
func NewSingleFlightCache[T any](inner CacheAside[T]) *SingleFlightCache[T] {
	return src.NewSingleFlightCache(inner)
}
