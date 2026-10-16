package twolevel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/oekit/pkg/cache"
)

// TwoLevelCache 是两级缓存装饰器：L1 本地缓存 + L2 远端 CacheAside
// （如 redisx/cache.RedisCache）读穿写透组合。实现 cache.CacheAside[T]，
// 可作为 cached.NewRepository 的后端工厂产物，也可独立使用。
//
// 语义分层见包文档；Del 先远后本的理由、null marker 委托 L2 的机制、
// SetWithTTL 双层传导的必要性均在类型与包文档中固化。
type TwoLevelCache[T any] struct {
	local  cache.Cache[T]
	remote cache.CacheAside[T]
	bus    *Bus

	closeOnce sync.Once
}

// 编译期接口契约：组合体必须完整满足 CacheAside。
var _ cache.CacheAside[int] = (*TwoLevelCache[int])(nil)

// CachedOption 配置 TwoLevelCache（命名对齐仓内硬规则，避免裸 Option）。
type CachedOption[T any] func(*config[T])

type config[T any] struct {
	bus *Bus
}

// WithBus 挂接失效广播：Del/DelBatch 时向 Redis 频道发布失效消息，
// Start 后订阅循环驱逐本实例（及其他实例）的 L1 副本。多实例部署必备；
// 单实例可不挂。
func WithBus[T any](bus *Bus) CachedOption[T] {
	return func(c *config[T]) { c.bus = bus }
}

// NewTwoLevelCache 创建两级缓存。local/remote 均必填，构造期 nil 检查
// fail-fast（而非首个操作时深层 panic）；remote 必须自带 Source
// （Cache-Aside 回源配置在远端实现上，本类型不重复持有回源函数）。
func NewTwoLevelCache[T any](
	local cache.Cache[T],
	remote cache.CacheAside[T],
	opts ...CachedOption[T],
) (*TwoLevelCache[T], error) {
	if local == nil {
		return nil, errors.New("twolevel: local 不得为 nil")
	}
	if remote == nil {
		return nil, errors.New("twolevel: remote 不得为 nil")
	}
	cfg := config[T]{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.bus != nil && cfg.bus.client == nil {
		return nil, errors.New("twolevel: bus 未初始化（NewBus 的 client 不得为 nil）")
	}
	return &TwoLevelCache[T]{local: local, remote: remote, bus: cfg.bus}, nil
}

// Start 启动失效广播订阅循环（无 bus 时为 no-op）。调用方负责在应用
// 生命周期内持有 ctx；Close 或 ctx 取消都会停止循环。
func (c *TwoLevelCache[T]) Start(ctx context.Context) error {
	if c.bus != nil {
		c.bus.start(ctx, c.evictLocal)
	}
	return nil
}

// Close 停止订阅循环（若有）并透传 L1 的 Close(ctx) error（Once 幂等，
// 重复调用安全；L1 不实现 Close 时为 no-op）。
func (c *TwoLevelCache[T]) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		if c.bus != nil {
			c.bus.stop()
		}
	})
	if lc, ok := c.local.(interface{ Close(context.Context) error }); ok {
		return lc.Close(ctx)
	}
	return nil
}

// evictLocal 广播订阅回调：驱逐本实例 L1 副本（含发布者自身消息，幂等）。
func (c *TwoLevelCache[T]) evictLocal(key string) {
	// 订阅循环生命周期独立于请求 ctx，驱逐用背景上下文
	_ = c.local.Del(context.Background(), key)
}

// Get 读穿获取：L1 命中直接返回；L2 命中后回填 L1 并返回。
func (c *TwoLevelCache[T]) Get(ctx context.Context, key string) (*T, error) {
	v, err := c.local.Get(ctx, key)
	if err != nil || v != nil {
		return v, err
	}

	v, err = c.remote.Get(ctx, key)
	if err != nil || v == nil {
		return v, err
	}
	c.backfillLocal(ctx, key, v)
	return v, nil
}

// Set 写穿两级（各自使用实现的默认 TTL）。
func (c *TwoLevelCache[T]) Set(ctx context.Context, key string, value *T) error {
	if err := c.remote.Set(ctx, key, value); err != nil {
		return err
	}
	return c.local.Set(ctx, key, value)
}

// SetWithTTL 写穿两级，显式 TTL 同时作用于 L1 与 L2（否则 L1 长驻会
// 吞掉显式短 TTL 的过期语义）。
func (c *TwoLevelCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	if err := c.remote.SetWithTTL(ctx, key, value, ttl); err != nil {
		return err
	}
	return c.local.SetWithTTL(ctx, key, value, ttl)
}

// Del 双删两级（先远端后本地，理由见包文档）；挂 bus 时发布失效消息
// （best-effort，发布失败由远端实例 TTL 兜底）。key 不存在时幂等不报错。
func (c *TwoLevelCache[T]) Del(ctx context.Context, key string) error {
	// errors.Join 的参数按从左到右求值：远端先删（见包文档的顺序论证）
	err := errors.Join(c.remote.Del(ctx, key), c.local.Del(ctx, key))
	if c.bus != nil {
		c.bus.publish(ctx, []string{key})
	}
	return err
}

// DelBatch 双删两级（批量口径同 Del）。空 keys 为幂等 no-op。
func (c *TwoLevelCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	err := errors.Join(c.remote.DelBatch(ctx, keys...), c.local.DelBatch(ctx, keys...))
	if c.bus != nil {
		c.bus.publish(ctx, keys)
	}
	return err
}

// Exists 存在性检查：L1 命中为 true，否则查 L2（L2 的 null marker 计存在）。
func (c *TwoLevelCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := c.local.Exists(ctx, key)
	if err != nil || ok {
		return ok, err
	}
	return c.remote.Exists(ctx, key)
}

// GetOrLoad 读穿回源：L1 命中直接返回；L1 miss 时 L2 负责回源与 null
// marker 防穿透，拿到真值后回填 L1。Source 返回 (nil, nil) 时透传
// (nil, nil)，L1 不缓存 null。
func (c *TwoLevelCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	v, err := c.local.Get(ctx, key)
	if err != nil || v != nil {
		return v, err
	}

	v, err = c.remote.GetOrLoad(ctx, key)
	if err != nil || v == nil {
		return v, err
	}
	c.backfillLocal(ctx, key, v)
	return v, nil
}

// Refresh 刷新：清 L1 后走 L2 的删除重载（null marker 一并清除），新值回填 L1。
func (c *TwoLevelCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	if err := c.local.Del(ctx, key); err != nil {
		twoLevelLog.Warn("two-level cache refresh local del failed", "key", key, "err", err)
	}

	v, err := c.remote.Refresh(ctx, key)
	if err != nil || v == nil {
		return v, err
	}
	c.backfillLocal(ctx, key, v)
	return v, nil
}

// backfillLocal 回填 L1（best-effort，契约要求失败不阻断返回已取到的
// 数据）。TTL 使用 L1 自身配置的默认策略——L1 比 L2 短寿是两级缓存的
// 预期形态，由构造 LocalCache 时配置。
func (c *TwoLevelCache[T]) backfillLocal(ctx context.Context, key string, v *T) {
	if err := c.local.Set(ctx, key, v); err != nil {
		twoLevelLog.Warn("two-level cache local backfill failed", "key", key, "err", err)
	}
}
