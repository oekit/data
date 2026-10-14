package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	cache "github.com/yi-nology/ekit-data/data/cache"
	"github.com/yi-nology/ekit-observability/observability/logx"
)

var redisLog = logx.Delegating("cache-redis")

// defaultNullTTL 是 NullTTL 未设置（0）时的空值缓存默认 TTL。
const defaultNullTTL = 1 * time.Minute

// RedisConfig 配置 Redis 缓存实例。
type RedisConfig[T any] struct {
	// Prefix key 前缀，如 "app:user:"。
	Prefix string

	// TTL 缓存过期时间。默认 24 小时。
	TTL time.Duration

	// Source 缓存未命中时的数据获取函数（Cache-Aside 模式）。
	// 返回 (nil, nil) 表示数据不存在（会缓存空值防穿透）。
	Source cache.SourceFunc[T]

	// Serializer 自定义序列化函数。默认使用 JSON。
	Serializer func(*T) ([]byte, error)

	// Deserializer 自定义反序列化函数。默认使用 JSON。
	Deserializer func([]byte, *T) error

	// NullTTL 空值缓存过期时间（防穿透）。
	//   - 0（未设置）：使用默认值 1 分钟；
	//   - 负值：显式禁用空值缓存（Source 返回 nil 时不写 null marker，
	//     下次 GetOrLoad 会重新调用 Source）；
	//   - 正值：按指定时长缓存 null marker。
	NullTTL time.Duration
}

// RedisCache 是基于 Redis 的泛型缓存实现。
// 实现 Cache[T] 接口，当配置了 Source 时还实现 CacheAside[T] 接口。
type RedisCache[T any] struct {
	client  redis.UniversalClient
	cfg     RedisConfig[T]
	nullTTL time.Duration
}

// NewRedisCache 创建一个基于 Redis 的泛型缓存实例。
func NewRedisCache[T any](client redis.UniversalClient, cfg RedisConfig[T]) *RedisCache[T] {
	nullTTL := cfg.NullTTL
	if nullTTL == 0 {
		// 0 = 未设置 → 使用默认 1 分钟（防穿透默认开启）；
		// 负值保留原值（显式禁用），写入 null marker 前以 nullTTL > 0 判断。
		nullTTL = defaultNullTTL
	}

	c := &RedisCache[T]{
		client:  client,
		cfg:     cfg,
		nullTTL: nullTTL,
	}

	if c.cfg.Serializer == nil {
		c.cfg.Serializer = defaultSerializer[T]
	}
	if c.cfg.Deserializer == nil {
		c.cfg.Deserializer = defaultDeserializer[T]
	}

	return c
}

// Get 获取缓存（纯缓存查询，不自动回填）。
// 返回 (nil, nil) 表示数据不存在 — 包括 key 不存在和命中 null marker 两种情况。
// 调用方需要 null marker 区分时使用 Exists 或 GetRaw。
func (c *RedisCache[T]) Get(ctx context.Context, key string) (*T, error) {
	val, _, err := c.getInternal(ctx, key)
	return val, err
}

// getInternal 内部方法，返回 (值, 是否命中 null marker, error)。
// 命中 null marker 时返回 (nil, true, nil)；
// key 不存在返回 (nil, false, nil)；
// 命中有效值返回 (&T, false, nil)。
// 用于 GetOrLoad 区分"未命中"和"命中 null marker"，避免防穿透失效。
func (c *RedisCache[T]) getInternal(ctx context.Context, key string) (*T, bool, error) {
	fullKey := c.fullKey(key)

	data, err := c.client.Get(ctx, fullKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache get: %w", err)
	}

	// 空值标记命中（仅认精确哨兵）：ekit 写入的 null marker 是 nullValue
	// 字符串。len==0 曾一并当作 null——SetRaw/损坏写入的空字节会被永久
	// 伪造成"数据不存在"，GetOrLoad 不再调 Source（防穿透变永久伪 miss），
	// 且 GetBytes 只认哨兵，两个读入口口径不一致。
	// 空字节按"条目损坏"处理：视为未命中走回源覆盖，既不伪 null 也不反序列化报错。
	if len(data) == 0 {
		return nil, false, nil
	}
	if string(data) == nullValue {
		return nil, true, nil
	}

	var result T
	if err := c.cfg.Deserializer(data, &result); err != nil {
		return nil, false, fmt.Errorf("cache deserialize: %w", err)
	}
	return &result, false, nil
}

// GetOrLoad 获取缓存，未命中时通过 Source 加载并回填（Cache-Aside 模式）。
// 必须配置 Source 函数，否则等同于 Get。
//
// 命中 null marker 时直接返回 (nil, nil)，**不会**再调 Source，
// 避免对不存在的 key 形成 thundering-herd 击穿。
func (c *RedisCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	// 1. 先查缓存
	result, isNullMarker, err := c.getInternal(ctx, key)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}
	if isNullMarker {
		// 命中 null marker：直接返回，不调 Source（防穿透）
		return nil, nil
	}

	// 2. 缓存未命中，调用 source
	if c.cfg.Source == nil {
		return nil, nil
	}

	source, err := c.cfg.Source(ctx, key)
	if err != nil {
		return nil, err
	}

	// 3. 回填缓存
	if source == nil {
		// 缓存空值防穿透
		if c.nullTTL > 0 {
			if setErr := c.client.Set(ctx, c.fullKey(key), nullValue, c.nullTTL).Err(); setErr != nil {
				redisLog.Error("set null value error", "key", key, "err", setErr)
			}
		}
		return nil, nil
	}

	data, err := c.cfg.Serializer(source)
	if err != nil {
		redisLog.Error("serialize error", "key", key, "err", err)
		return source, nil // 序列化失败仍返回数据
	}

	ttl := c.cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	if err := c.client.Set(ctx, c.fullKey(key), data, ttl).Err(); err != nil {
		redisLog.Error("set error", "key", key, "err", err)
	}

	return source, nil
}

// GetBytes 获取原始字节数据（不经过序列化）。
func (c *RedisCache[T]) GetBytes(ctx context.Context, key string) ([]byte, error) {
	fullKey := c.fullKey(key)
	data, err := c.client.Get(ctx, fullKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if string(data) == nullValue {
		return nil, nil
	}
	return data, nil
}

// Set 设置缓存。
func (c *RedisCache[T]) Set(ctx context.Context, key string, value *T) error {
	fullKey := c.fullKey(key)

	data, err := c.cfg.Serializer(value)
	if err != nil {
		return fmt.Errorf("cache serialize: %w", err)
	}

	ttl := c.cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	return c.client.Set(ctx, fullKey, data, ttl).Err()
}

// SetWithTTL 设置缓存（自定义 TTL）。
func (c *RedisCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	fullKey := c.fullKey(key)

	data, err := c.cfg.Serializer(value)
	if err != nil {
		return fmt.Errorf("cache serialize: %w", err)
	}

	return c.client.Set(ctx, fullKey, data, ttl).Err()
}

// SetRaw 设置原始字节数据（不经过序列化）。
func (c *RedisCache[T]) SetRaw(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	return c.client.Set(ctx, c.fullKey(key), data, ttl).Err()
}

// Del 删除缓存。
func (c *RedisCache[T]) Del(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.fullKey(key)).Err()
}

// DelBatch 批量删除缓存。
func (c *RedisCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.fullKey(key)
	}
	return c.client.Del(ctx, fullKeys...).Err()
}

// Exists 检查缓存是否存在。
func (c *RedisCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, c.fullKey(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Refresh 刷新缓存（删除后重新从 Source 加载）。
// 必须配置 Source 函数。
func (c *RedisCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	if err := c.Del(ctx, key); err != nil {
		redisLog.Error("refresh del error", "key", key, "err", err)
	}
	return c.GetOrLoad(ctx, key)
}

func (c *RedisCache[T]) fullKey(key string) string {
	return c.cfg.Prefix + key
}

const nullValue = "__NULL__"

func defaultSerializer[T any](v *T) ([]byte, error) {
	return json.Marshal(v)
}

func defaultDeserializer[T any](data []byte, v *T) error {
	return json.Unmarshal(data, v)
}
