package cache

import (
	"context"
	"time"
)

// Cache 是泛型缓存接口，面向接口编程。
// 只定义通用的缓存操作，不绑定具体实现或模式。
type Cache[T any] interface {
	// Get 获取缓存。
	// 返回 (nil, nil) 表示数据不存在。
	Get(ctx context.Context, key string) (*T, error)

	// Set 设置缓存（使用默认 TTL）。
	Set(ctx context.Context, key string, value *T) error

	// SetWithTTL 设置缓存（自定义 TTL）。
	SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error

	// Del 删除缓存。
	Del(ctx context.Context, key string) error

	// DelBatch 批量删除缓存。
	DelBatch(ctx context.Context, keys ...string) error

	// Exists 检查缓存是否存在。
	Exists(ctx context.Context, key string) (bool, error)
}

// SourceFunc 缓存未命中时的数据获取函数。
// 返回 (nil, nil) 表示数据不存在 —— 实现会缓存 null marker 防穿透：
// GetOrLoad 命中 null marker 时直接返回 (nil, nil)，不再调用 Source。
// null marker 的 TTL 默认 1 分钟，可通过各实现的 NullTTL 配置调整
// （0 = 默认 1 分钟，负值 = 显式禁用空值缓存）。
type SourceFunc[T any] func(ctx context.Context, key string) (*T, error)

// CacheAside 是 Cache-Aside 模式扩展接口。
// 在 Cache 基础上增加数据源回填、刷新等能力。
type CacheAside[T any] interface {
	Cache[T]

	// GetOrLoad 获取缓存，未命中时通过 Source 加载并回填。
	// Source 返回 (nil, nil) 时缓存 null marker（默认 1 分钟，防穿透）；
	// 后续调用命中 null marker 直接返回 (nil, nil)，不再调用 Source。
	GetOrLoad(ctx context.Context, key string) (*T, error)

	// Refresh 刷新缓存（删除后重新从 Source 加载）。
	Refresh(ctx context.Context, key string) (*T, error)
}
