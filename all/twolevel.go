package all

import (
	"context"
	"errors"
	"time"

	datacache "github.com/oekit/data/cache"
	"github.com/oekit/logx"
)

// twoLevelLog 懒解析 logger：SDK 基础包禁止 WithService 硬编码 service name。
var twoLevelLog = logx.Delegating("cache-twolevel")

// TwoLevelCache 是两级缓存装饰器：L1 本地缓存（data/cache.LocalCache）+
// L2 远端缓存（redisx/cache.RedisCache 等任意 CacheAside 实现）读穿写透组合。
//
// 语义分层：
//   - L1 只缓存真值（命中即返回，不打 L2）；**null 防穿透完全委托 L2 的
//     null marker**（Source 返回 (nil, nil) 时 marker 落在 L2，后续
//     GetOrLoad 每次 L1 miss 后会查一跳 L2 并被 marker 挡住，不回源）；
//   - GetOrLoad/Get 在 L2 命中后回填 L1（TTL 归 L1 自身的 LocalConfig）；
//   - Set/SetWithTTL 写穿两级；SetWithTTL 的显式 TTL 同时作用于 L1 与 L2
//     （否则 L1 长驻会吞掉显式短 TTL 的过期语义）；
//   - Del/DelBatch 双删两级（先远端后本地：远端失败时本地已删，读路径
//     回填的是远端旧值，短窗口不一致；反序则本地旧值会活过整个 L1 TTL）；
//   - Refresh 走 L2 删除重载（marker 一并清除），新值回填 L1。
//
// 值语义沿用 data/cache 的实现契约：返回值一律只读对待（L1 返回内部共享
// 引用，L2 每次反序列化返回新副本）；回填 best-effort，失败记日志不阻断。
//
// 接口语义（miss 非 error、null marker、Del 幂等）由
// github.com/oekit/data/cachetest 契约套件固化（contract_test.go）。
type TwoLevelCache[T any] struct {
	local  *datacache.LocalCache[T]
	remote datacache.CacheAside[T]
}

// 编译期接口契约。
var _ datacache.CacheAside[int] = (*TwoLevelCache[int])(nil)

// NewTwoLevelCache 创建两级缓存。
// local 与 remote 均必填，nil 在构造期 fail-fast（而非首个操作时深层 panic）；
// remote 必须自带 Source（Cache-Aside 回源配置在远端实现上，如
// redisxcache.RedisConfig.Source），本类型不重复持有回源函数。
func NewTwoLevelCache[T any](local *datacache.LocalCache[T], remote datacache.CacheAside[T]) *TwoLevelCache[T] {
	if local == nil {
		panic("all: NewTwoLevelCache local 不得为 nil")
	}
	if remote == nil {
		panic("all: NewTwoLevelCache remote 不得为 nil")
	}
	return &TwoLevelCache[T]{local: local, remote: remote}
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

// SetWithTTL 写穿两级，显式 TTL 同时作用于 L1 与 L2。
func (c *TwoLevelCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	if err := c.remote.SetWithTTL(ctx, key, value, ttl); err != nil {
		return err
	}
	return c.local.SetWithTTL(ctx, key, value, ttl)
}

// Del 双删两级（先远端后本地，理由见类型文档）；key 不存在时幂等不报错。
func (c *TwoLevelCache[T]) Del(ctx context.Context, key string) error {
	return errors.Join(c.remote.Del(ctx, key), c.local.Del(ctx, key))
}

// DelBatch 双删两级（批量口径同 Del）。空 keys 为幂等 no-op。
func (c *TwoLevelCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	return errors.Join(c.remote.DelBatch(ctx, keys...), c.local.DelBatch(ctx, keys...))
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
// (nil, nil)，L1 不缓存 null（见类型文档的语义分层）。
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

// Close 停止 L1 本地缓存的后台清理 goroutine（透传 LocalCache 的
// Close(ctx) error；Once 幂等，重复调用安全）。
func (c *TwoLevelCache[T]) Close(ctx context.Context) error {
	return c.local.Close(ctx)
}

// backfillLocal 回填 L1（best-effort，契约要求失败不阻断返回已取到的数据）。
// TTL 使用 L1 自身 LocalConfig 的默认策略——L1 比 L2 短寿是两级缓存的
// 预期形态，由构造 LocalCache 时配置。
func (c *TwoLevelCache[T]) backfillLocal(ctx context.Context, key string, v *T) {
	if err := c.local.Set(ctx, key, v); err != nil {
		twoLevelLog.Warn("two-level cache local backfill failed", "key", key, "err", err)
	}
}
