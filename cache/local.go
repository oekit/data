package cache

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/logx"
)

var cacheLog = logx.Delegating("cache")

// defaultNullTTL 是 NullTTL 未设置（0）时的空值缓存默认 TTL。
// redis 子包的 RedisCache 与 LocalCache 保持一致的空值缓存默认语义。
const defaultNullTTL = 1 * time.Minute

// localEntry 本地缓存条目。
// isNull=true 表示 null marker（数据不存在标记，防穿透），value 恒为 nil。
type localEntry[T any] struct {
	value     *T
	isNull    bool
	expiresAt time.Time
}

// isExpired 检查是否过期。
func (e *localEntry[T]) isExpired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// LocalConfig 配置本地缓存实例。
type LocalConfig[T any] struct {
	// TTL 缓存过期时间。0 表示不过期。
	TTL time.Duration

	// MaxSize 最大缓存条目数。0 表示不限制。
	MaxSize int

	// Source 缓存未命中时的数据获取函数（Cache-Aside 模式）。
	Source SourceFunc[T]

	// NullTTL 空值缓存过期时间（防穿透）。
	//   - 0（未设置）：使用默认值 1 分钟；
	//   - 负值：显式禁用空值缓存（Source 返回 nil 时不写 null marker，
	//     下次 GetOrLoad 会重新调用 Source）；
	//   - 正值：按指定时长缓存 null marker。
	NullTTL time.Duration
}

// LocalCache 是基于内存的本地缓存实现。
// 适用于单实例场景或作为二级缓存。
// 淘汰策略为写序淘汰（FIFO + 重写提升，读命中不提升——非严格 LRU）：
// 达到 MaxSize 后淘汰最早写入（且未被重写刷新）的条目。
// 使用 Close(ctx) 方法停止后台清理 goroutine；Close 可安全重复调用，
// 签名对齐仓库硬规则 Close(ctx context.Context) error。
type LocalCache[T any] struct {
	mu        sync.RWMutex
	data      map[string]*localEntry[T]
	cfg       LocalConfig[T]
	order     []string       // 写序淘汰队列（尾部最新；重写提升，读命中不提升）
	orderIdx  map[string]int // key → order 切片索引，O(1) 移除
	done      chan struct{}
	closeOnce sync.Once
}

// NewLocalCache 创建一个本地缓存实例。
func NewLocalCache[T any](cfg LocalConfig[T]) *LocalCache[T] {
	c := &LocalCache[T]{
		data:     make(map[string]*localEntry[T]),
		cfg:      cfg,
		orderIdx: make(map[string]int),
		done:     make(chan struct{}),
	}

	// 启动过期清理
	if cfg.TTL > 0 {
		async.GoSafe(func() { c.cleanup() })
	}

	return c
}

// Close 停止后台清理 goroutine（TTL>0 时构造启动的那个）。
// sync.Once 保证幂等：重复 Close 不 panic、不返回错误。
//
// ctx 预留：当前实现同步返回 nil，ctx 不阻塞——本实现无外部资源需要
// 排空，仅关闭内部 done channel 通知 cleanup goroutine 退出。
func (c *LocalCache[T]) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		close(c.done)
	})
	return nil
}

// Get 获取缓存。
// 返回 (nil, nil) 表示数据不存在 —— 包括 key 不存在和命中 null marker
// 两种情况；GetOrLoad 会区分二者实现防穿透。
//
// ⚠️ 返回的是缓存内部的 *T（零拷贝，热路径设计）：调用方不得修改指向的
// 值——会污染缓存并与并发读形成 data race。需要可变副本时自行拷贝。
// （RedisCache 每次反序列化返回新副本，两实现语义差异见各自身文档。）
func (c *LocalCache[T]) Get(ctx context.Context, key string) (*T, error) {
	entry, ok := c.getEntry(key)
	if !ok || entry.isNull {
		return nil, nil
	}
	return entry.value, nil
}

// getEntry 内部方法：返回有效 entry 及其是否存在（含 null marker）。
// 过期条目会被顺带删除（双检模式，避免误删并发 Set 的新值）。
func (c *LocalCache[T]) getEntry(key string) (*localEntry[T], bool) {
	c.mu.RLock()
	entry, ok := c.data[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if entry.isExpired() {
		c.mu.Lock()
		// 二次检查：在 RUnlock 和 Lock 之间可能有别的 goroutine Set 了新值，
		// 只删除仍然是同一个过期 entry 的情况，避免误删新值。
		if cur, exists := c.data[key]; exists && cur == entry {
			delete(c.data, key)
			c.removeFromOrder(key)
		}
		c.mu.Unlock()
		return nil, false
	}

	return entry, true
}

// GetOrLoad 获取缓存，未命中时通过 Source 加载（Cache-Aside 模式）。
// Source 返回 (nil, nil) 时缓存 null marker（默认 1 分钟，可用 NullTTL
// 调整，负值禁用）；命中 null marker 时直接返回 (nil, nil)，**不会**再调
// Source，避免对不存在的 key 形成 thundering-herd 击穿。
func (c *LocalCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	// 1. 先查缓存（区分普通未命中与 null marker 命中）
	entry, ok := c.getEntry(key)
	if ok {
		if entry.isNull {
			// 命中 null marker：直接返回，不调 Source（防穿透）
			return nil, nil
		}
		return entry.value, nil
	}

	// 2. 缓存未命中，调用 source
	if c.cfg.Source == nil {
		return nil, nil
	}

	source, err := c.cfg.Source(ctx, key)
	if err != nil {
		return nil, err
	}

	// 3. 回填缓存（best-effort，失败仅记录不影响主流程）
	if source == nil {
		// 缓存空值防穿透
		if nullTTL := c.effectiveNullTTL(); nullTTL > 0 {
			c.setNullMarker(key, nullTTL)
		}
		return nil, nil
	}

	if e := c.Set(ctx, key, source); e != nil {
		cacheLog.Warn("local cache backfill failed", "key", key, "err", e)
	}

	return source, nil
}

// effectiveNullTTL 返回生效的空值缓存 TTL：
// 0（未设置）→ 默认 1 分钟；负值 → 原样返回（<=0 表示禁用）。
func (c *LocalCache[T]) effectiveNullTTL() time.Duration {
	if c.cfg.NullTTL == 0 {
		return defaultNullTTL
	}
	return c.cfg.NullTTL
}

// setNullMarker 写入 null marker 条目（与 Set 相同的锁纪律与淘汰逻辑）。
func (c *LocalCache[T]) setNullMarker(key string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// 检查是否需要淘汰（同 SetWithTTL）
	if c.cfg.MaxSize > 0 && len(c.data) >= c.cfg.MaxSize {
		if _, exists := c.data[key]; !exists {
			c.evictOldest()
		}
	}

	c.data[key] = &localEntry[T]{
		isNull:    true,
		expiresAt: expiresAt,
	}
	c.moveToFront(key)
}

// Set 设置缓存（使用默认 TTL）。
func (c *LocalCache[T]) Set(ctx context.Context, key string, value *T) error {
	return c.SetWithTTL(ctx, key, value, c.cfg.TTL)
}

// SetWithTTL 设置缓存（自定义 TTL）。
func (c *LocalCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// 检查是否需要淘汰
	if c.cfg.MaxSize > 0 && len(c.data) >= c.cfg.MaxSize {
		if _, exists := c.data[key]; !exists {
			c.evictOldest()
		}
	}

	c.data[key] = &localEntry[T]{
		value:     value,
		expiresAt: expiresAt,
	}

	// 更新访问顺序
	c.moveToFront(key)
	return nil
}

// Del 删除缓存。
func (c *LocalCache[T]) Del(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, key)
	c.removeFromOrder(key)
	return nil
}

// DelBatch 批量删除缓存。
func (c *LocalCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range keys {
		delete(c.data, key)
		c.removeFromOrder(key)
	}
	return nil
}

// Exists 检查缓存是否存在。
func (c *LocalCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	c.mu.RLock()
	entry, ok := c.data[key]
	c.mu.RUnlock()

	if !ok {
		return false, nil
	}

	if entry.isExpired() {
		c.mu.Lock()
		// 二次检查：同 Get，避免误删并发 Set 的新值。
		if cur, exists := c.data[key]; exists && cur == entry {
			delete(c.data, key)
			c.removeFromOrder(key)
		}
		c.mu.Unlock()
		return false, nil
	}

	return true, nil
}

// Refresh 刷新缓存（删除后重新从 Source 加载）。
func (c *LocalCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	if e := c.Del(ctx, key); e != nil {
		cacheLog.Warn("local cache refresh del failed", "key", key, "err", e)
	}
	return c.GetOrLoad(ctx, key)
}

// Size 返回当前缓存条目数。
func (c *LocalCache[T]) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// Clear 清空缓存。
func (c *LocalCache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]*localEntry[T])
	c.order = nil
	c.orderIdx = make(map[string]int)
}

// cleanup 定期清理过期条目。
// 分两阶段：读锁收集过期 key，写锁批量删除，减少写锁持有时间。
func (c *LocalCache[T]) cleanup() {
	// TTL/2 可能整除为 0（如 TTL=1ns），time.NewTicker(0) 会 panic
	// （同型问题 KeyedLimiter 已修，此处补齐）；下限 1s 保底。
	interval := c.cfg.TTL / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case now := <-ticker.C:
			// 阶段 1：读锁扫描，收集过期 key
			c.mu.RLock()
			var expired []string
			for key, entry := range c.data {
				if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
					expired = append(expired, key)
				}
			}
			c.mu.RUnlock()

			if len(expired) == 0 {
				continue
			}

			// 阶段 2：写锁批量删除
			c.mu.Lock()
			for _, key := range expired {
				// 二次检查：可能已被其他 goroutine 更新
				if entry, ok := c.data[key]; ok && entry.isExpired() {
					delete(c.data, key)
					c.removeFromOrder(key)
				}
			}
			c.mu.Unlock()
		}
	}
}

// evictOldest 淘汰最旧的条目（调用者需持有锁）。
func (c *LocalCache[T]) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	delete(c.data, oldest)
	c.removeFromOrder(oldest)
}

// moveToFront 将 key 移到队列尾部（最新，调用者需持有锁）。
func (c *LocalCache[T]) moveToFront(key string) {
	c.removeFromOrder(key)
	c.orderIdx[key] = len(c.order)
	c.order = append(c.order, key)
}

// removeFromOrder 从队列中移除 key（O(1)，调用者需持有锁）。
func (c *LocalCache[T]) removeFromOrder(key string) {
	idx, ok := c.orderIdx[key]
	if !ok {
		return
	}
	delete(c.orderIdx, key)
	// 用最后一个元素填补空位，保持切片紧凑
	last := len(c.order) - 1
	if idx < last {
		c.order[idx] = c.order[last]
		c.orderIdx[c.order[idx]] = idx
	}
	c.order = c.order[:last]
}
