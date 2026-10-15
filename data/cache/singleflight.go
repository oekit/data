package cache

import (
	"context"
	"time"

	"github.com/oekit/concurrency/singleflight"
)

// SingleFlightCache 是带 singleflight 保护的缓存装饰器。
// 同一个 key 的并发请求只会有一个实际调用 Source，其他请求等待共享结果。
// 防止缓存击穿（热点 key 过期时的雷群效应）。
//
// 可以包装任何 CacheAside[T] 实现（Redis、本地缓存等）。
type SingleFlightCache[T any] struct {
	inner  CacheAside[T]
	flight *singleflight.Flight[*T]
}

// NewSingleFlightCache 创建带 singleflight 保护的缓存装饰器。
// inner 可以是 RedisCache、LocalCache 或任何实现 CacheAside[T] 的缓存。
func NewSingleFlightCache[T any](inner CacheAside[T]) *SingleFlightCache[T] {
	return &SingleFlightCache[T]{
		inner:  inner,
		flight: singleflight.New[*T](),
	}
}

// Get 获取缓存（纯查询，不触发 Source）。
func (sf *SingleFlightCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return sf.inner.Get(ctx, key)
}

// Set 设置缓存。
func (sf *SingleFlightCache[T]) Set(ctx context.Context, key string, value *T) error {
	return sf.inner.Set(ctx, key, value)
}

// SetWithTTL 设置缓存（自定义 TTL）。
func (sf *SingleFlightCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	return sf.inner.SetWithTTL(ctx, key, value, ttl)
}

// Del 删除缓存。
func (sf *SingleFlightCache[T]) Del(ctx context.Context, key string) error {
	return sf.inner.Del(ctx, key)
}

// DelBatch 批量删除缓存。
func (sf *SingleFlightCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	return sf.inner.DelBatch(ctx, keys...)
}

// Exists 检查缓存是否存在。
func (sf *SingleFlightCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return sf.inner.Exists(ctx, key)
}

// GetOrLoad 获取缓存，带 singleflight 保护。
// 同一个 key 的并发请求只会有一个实际调用 Source。
//
// 流程：
//  1. 查缓存，命中则直接返回
//  2. 未命中时通过 singleflight 去重，只有一个 goroutine 调用 Source
//  3. Source 返回后回填缓存
//  4. 其他 goroutine 共享同一份结果
func (sf *SingleFlightCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	// 1. 先查缓存
	result, err := sf.inner.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	// 2. singleflight 去重，调用 inner 的 GetOrLoad
	val, err := sf.flight.Do(ctx, key, func(ctx context.Context) (*T, error) {
		return sf.inner.GetOrLoad(ctx, key)
	})
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Refresh 刷新缓存（带 singleflight 保护）。
func (sf *SingleFlightCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	val, err := sf.flight.Do(ctx, key, func(ctx context.Context) (*T, error) {
		return sf.inner.Refresh(ctx, key)
	})
	if err != nil {
		return nil, err
	}
	return val, nil
}
