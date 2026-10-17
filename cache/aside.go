package cache

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/logx"
)

// SourceFuncWithTTL 回源函数：TTL 由回源结果决定，而非构造期常量。
//
// 典型场景：IM 平台 access_token 的有效期来自响应体 expires_in（减安全
// 偏移），无法在构造缓存时固定；返回 ttl<=0 时回填回退实现默认 TTL
// （等价 Set 而非 SetWithTTL）。
//
// 值语义与 (nil, nil) 双关、null marker 防穿透同 SourceFunc：
// 返回 (nil, nil) 表示数据不存在，实现会缓存 null marker。
type SourceFuncWithTTL[T any] func(ctx context.Context, key string) (*T, time.Duration, error)

// SourceFrom 把固定语义的 SourceFunc 适配为 SourceFuncWithTTL
// （恒返回 ttl=0 → 回填走实现默认 TTL）。
func SourceFrom[T any](fn SourceFunc[T]) SourceFuncWithTTL[T] {
	return func(ctx context.Context, key string) (*T, time.Duration, error) {
		v, err := fn(ctx, key)
		return v, 0, err
	}
}

// TTLCacheAside 是 CacheAside 的动态 TTL 扩展：回填 TTL 由
// SourceFuncWithTTL 的返回值决定（ttl<=0 回退默认 TTL）。
//
// AsideCache 实现本接口；新增后端若声明本能力，应跑过
// github.com/oekit/data/cachetest 的 RunTTL / RunStaleIfError 场景。
type TTLCacheAside[T any] interface {
	CacheAside[T]

	// GetOrLoadWithTTL 获取缓存，未命中时经 SourceFuncWithTTL 回源，
	// 回填 TTL 取 source 返回值（<=0 回退默认 TTL）。语义同 GetOrLoad
	// （miss 非 error、null marker 防穿透、回填 best-effort）。
	GetOrLoadWithTTL(ctx context.Context, key string) (*T, error)
}

// OnStaleFunc stale 命中时的上报回调（日志/指标由调用方决定）。
type OnStaleFunc[T any] func(ctx context.Context, key string, value *T)

// AsideOption 配置 AsideCache（命名对齐仓内硬规则，避免裸 Option）。
type AsideOption[T any] func(*asideConfig[T])

type asideConfig[T any] struct {
	staleIfError bool
	onStale      OnStaleFunc[T]
	nullTTL      time.Duration
}

// WithStaleIfError 开关 fail-open stale-if-error：Source 失败且存在陈旧
// 真值时返回陈旧值（可经 WithOnStale 上报），仅当无陈旧值才上抛错误。
//
// 默认关闭（fail-closed）：Source 失败直接上抛，语义与裸 GetOrLoad 一致。
//
// # 与 null marker 的组合语义（实现契约）
//
//   - null marker **不参与** stale 兜底：marker 表示「已知不存在」，不是
//     陈旧真值；Source 成功返回 (nil, nil) 时会清除该键的 stale 影子，
//     此后 Source 失败只上抛、不复活旧值。
//   - stale 命中**不刷新** lastAccess、**不回填**缓存：影子只读兜底，
//     缓存层仍视为 miss，下次请求继续走回源（陈旧窗口不因读而延长）。
//   - Del/DelBatch **清除** stale 影子：写后失效不得被 stale 兜底复活
//     （否则删掉的数据会以旧值回归）。
func WithStaleIfError[T any](enabled bool) AsideOption[T] {
	return func(c *asideConfig[T]) { c.staleIfError = enabled }
}

// WithOnStale 注册 stale 命中上报回调（配合 WithStaleIfError 使用）。
// 回调在返回陈旧值之前同步调用，不得 panic。
func WithOnStale[T any](fn OnStaleFunc[T]) AsideOption[T] {
	return func(c *asideConfig[T]) { c.onStale = fn }
}

// WithAsideNullTTL 设置 null marker 的 TTL。
//   - 0（未设置）：默认 1 分钟；
//   - 负值：显式禁用空值缓存（每次 GetOrLoad 都重新调 Source）；
//   - 正值：按指定时长缓存 null marker。
//
// 与 pkg/cache LocalConfig.NullTTL 语义对齐。
func WithAsideNullTTL[T any](d time.Duration) AsideOption[T] {
	return func(c *asideConfig[T]) { c.nullTTL = d }
}

// defaultAsideNullTTL 是 null marker 的默认 TTL（与 pkg/cache 一致）。
const defaultAsideNullTTL = time.Minute

// asideEntry 单键的装饰器侧状态：null marker 有效期 + stale 影子。
type asideEntry[T any] struct {
	good      *T        // 最近一次成功回源/命中的真值（stale 兜底影子）
	hasGood   bool      // good 是否有效（区分「从未有过」与「已清除」）
	nullUntil time.Time // 非零且未到期 = null marker 有效
}

// nullActive 判断 null marker 是否仍在有效期。
func (e *asideEntry[T]) nullActive() bool {
	return !e.nullUntil.IsZero() && time.Now().Before(e.nullUntil)
}

// AsideCache 是 Cache-Aside 装饰器：在任意 Cache[T] 存储之上提供
// 回源动态 TTL（SourceFuncWithTTL）与可选 fail-open stale-if-error。
//
// 职责分层：
//   - inner 只承担值存储（Get/Set/SetWithTTL/Del/Exists）；
//   - 回源、null marker、stale 影子由本装饰器负责（backend 无关，
//     不依赖 inner 是否区分 null marker）。
//
// # 实现契约（stale-if-error × null marker 组合语义）
//
// 与 pkg/cache 包级「实现契约」对齐，并补齐 stale 维度：
//
//   - null marker **不参与** stale 兜底：marker 表示「已知不存在」，
//     不是陈旧真值。Source 成功返回 (nil, nil) 时清除该键 stale 影子
//     并写 marker；marker 命中短路返回 (nil, nil)（防穿透，不回源），
//     绝不返回旧真值。
//   - stale 命中**不刷新 lastAccess**、**不回填**缓存：影子只读兜底，
//     缓存层仍视为 miss，下次请求继续走回源（陈旧窗口不因读而延长）。
//   - Del/DelBatch **清除** stale 影子：写后失效不得被 stale 兜底复活
//     （否则删掉的数据会以旧值回归）。
//   - stale-if-error 默认关闭（fail-closed）：Source 失败直接上抛。
//   - 回填 best-effort：SetWithTTL 失败不影响已取到的返回值。
//   - 值语义别名警告同 pkg/cache 包级契约——调用方一律只读对待返回的 *T。
//
// 实现 CacheAside[T] 与 TTLCacheAside[T]。
type AsideCache[T any] struct {
	inner  Cache[T]
	source SourceFuncWithTTL[T]
	cfg    asideConfig[T]
	log    logx.Logger

	mu      sync.RWMutex
	entries map[string]*asideEntry[T]
}

// 编译期接口契约：AsideCache 必须完整满足 CacheAside 与 TTLCacheAside。
var (
	_ CacheAside[int]    = (*AsideCache[int])(nil)
	_ TTLCacheAside[int] = (*AsideCache[int])(nil)
)

// NewAsideCache 构造带回源动态 TTL 的 Cache-Aside 装饰器。
//
// inner 必填（存储后端，如 NewLocalCache 产物，**不要**在其上再配
// Source——回源统一由本装饰器持有）；source 必填。
// 默认 TTL 取 inner 的 Set 语义（ttl<=0 时回填走 Set 而非 SetWithTTL）。
//
//	opts := []cache.AsideOption[Token]{cache.WithStaleIfError[Token](true)}
//	aside := cache.NewAsideCache(inner, loadTokenWithExpiry, opts...)
func NewAsideCache[T any](inner Cache[T], source SourceFuncWithTTL[T], opts ...AsideOption[T]) *AsideCache[T] {
	cfg := asideConfig[T]{nullTTL: 0}
	for _, o := range opts {
		o(&cfg)
	}
	return &AsideCache[T]{
		inner:   inner,
		source:  source,
		cfg:     cfg,
		log:     logx.Delegating("cache-aside"),
		entries: make(map[string]*asideEntry[T]),
	}
}

// --- 装饰器侧状态访问（一律在 a.mu 下读写 entry 字段，禁外泄裸指针） ---

// nullActive 查询键的 null marker 是否有效。
func (a *AsideCache[T]) nullActive(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.entries[key]
	return ok && e.nullActive()
}

// loadGood 取 stale 影子（第二个返回值表示是否存在有效影子）。
func (a *AsideCache[T]) loadGood(key string) (*T, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.entries[key]
	if !ok || !e.hasGood {
		return nil, false
	}
	return e.good, true
}

// storeGood 写 stale 影子并清除 null marker。
func (a *AsideCache[T]) storeGood(key string, v *T) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entries[key]
	if e == nil {
		e = &asideEntry[T]{}
		a.entries[key] = e
	}
	if v == nil {
		e.hasGood = false
		e.good = nil
	} else {
		e.hasGood = true
		e.good = v
	}
	e.nullUntil = time.Time{}
}

// storeNull 写 null marker 并清除 stale 影子（数据已不存在，不得复活）。
func (a *AsideCache[T]) storeNull(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entries[key]
	if e == nil {
		e = &asideEntry[T]{}
		a.entries[key] = e
	}
	e.hasGood = false
	e.good = nil
	if nullTTL := a.effectiveNullTTL(); nullTTL > 0 {
		e.nullUntil = time.Now().Add(nullTTL)
	} else {
		e.nullUntil = time.Time{}
	}
}

// clearEntry 清除键的装饰器侧状态（null marker + stale 影子）。
func (a *AsideCache[T]) clearEntry(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.entries, key)
}

// effectiveNullTTL 返回生效的 null marker TTL：
// 0（未设置）→ 默认 1 分钟；负值 → 原样返回（<=0 表示禁用）。
func (a *AsideCache[T]) effectiveNullTTL() time.Duration {
	if a.cfg.nullTTL == 0 {
		return defaultAsideNullTTL
	}
	return a.cfg.nullTTL
}

// Get 获取缓存。命中 null marker 返回 (nil, nil)（同包级双关契约）。
func (a *AsideCache[T]) Get(ctx context.Context, key string) (*T, error) {
	if a.nullActive(key) {
		return nil, nil
	}
	return a.inner.Get(ctx, key)
}

// Set 设置缓存（inner 默认 TTL），并刷新 stale 影子。
func (a *AsideCache[T]) Set(ctx context.Context, key string, value *T) error {
	a.storeGood(key, value)
	return a.inner.Set(ctx, key, value)
}

// SetWithTTL 设置缓存（显式 TTL），并刷新 stale 影子。
func (a *AsideCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	a.storeGood(key, value)
	return a.inner.SetWithTTL(ctx, key, value, ttl)
}

// Del 删除缓存，并清除 stale 影子与 null marker
// （写后失效不得被 stale 兜底复活）。
func (a *AsideCache[T]) Del(ctx context.Context, key string) error {
	a.clearEntry(key)
	return a.inner.Del(ctx, key)
}

// DelBatch 批量删除缓存。同 Del 清除装饰器侧状态，幂等。
func (a *AsideCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		a.clearEntry(key)
	}
	return a.inner.DelBatch(ctx, keys...)
}

// Exists 检查是否存在。对 null marker 返回 true（"缓存知道该 key 不存在"
// 也是一种存在），与 Cache 契约一致。
func (a *AsideCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	if a.nullActive(key) {
		return true, nil
	}
	return a.inner.Exists(ctx, key)
}

// GetOrLoad 获取缓存，未命中经 SourceFuncWithTTL 回源。
// 回填 TTL 取 source 返回值（<=0 回退默认 TTL）。
func (a *AsideCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	return a.GetOrLoadWithTTL(ctx, key)
}

// GetOrLoadWithTTL 获取缓存，未命中经 SourceFuncWithTTL 回源并按返回
// TTL 回填（ttl<=0 回退默认 TTL）。
//
// 流程：
//  1. null marker 有效 → (nil, nil)，不回源（防穿透）；
//  2. inner 命中真值 → 返回并刷新 stale 影子；
//  3. 回源：成功 (nil, nil) → 写 null marker 并清除 stale 影子；
//     成功真值 → 回填（best-effort）并记 stale 影子；
//     失败 → stale-if-error 开启且有陈旧真值时返回陈旧值（不回填、
//     不刷新 lastAccess），否则上抛。
func (a *AsideCache[T]) GetOrLoadWithTTL(ctx context.Context, key string) (*T, error) {
	// 1. null marker 短路
	if a.nullActive(key) {
		return nil, nil
	}

	// 2. 存储命中
	if v, err := a.inner.Get(ctx, key); err == nil && v != nil {
		a.storeGood(key, v)
		return v, nil
	}
	// inner 读失败按 miss 处理（fail-open 到回源，存储故障不阻断读路径）

	if a.source == nil {
		return nil, nil
	}

	// 3. 回源
	v, ttl, err := a.source(ctx, key)
	if err != nil {
		stale, staleOK := a.loadGood(key)
		if a.cfg.staleIfError && staleOK && stale != nil {
			a.log.CtxWarn(ctx, "cache-aside stale-if-error serve",
				"key", key, "err", err)
			if a.cfg.onStale != nil {
				a.cfg.onStale(ctx, key, stale)
			}
			return stale, nil
		}
		return nil, err
	}

	if v == nil {
		// 数据不存在：写 null marker，清除 stale 影子（不得复活旧值）
		a.storeNull(key)
		return nil, nil
	}

	a.storeGood(key, v)

	// 回填 best-effort：失败仅记录，不影响已取到的返回值
	var fill error
	if ttl <= 0 {
		fill = a.inner.Set(ctx, key, v)
	} else {
		fill = a.inner.SetWithTTL(ctx, key, v, ttl)
	}
	if fill != nil {
		a.log.CtxWarn(ctx, "cache-aside backfill failed", "key", key, "err", fill)
	}
	return v, nil
}

// Refresh 刷新缓存（清除后重新回源，走 GetOrLoadWithTTL 语义）。
func (a *AsideCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	a.clearEntry(key)
	if err := a.inner.Del(ctx, key); err != nil {
		a.log.CtxWarn(ctx, "cache-aside refresh del failed", "key", key, "err", err)
	}
	return a.GetOrLoadWithTTL(ctx, key)
}
