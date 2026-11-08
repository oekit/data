package cached_test

// #141 回归钉：写已提交后的缓存失效不得被请求 ctx 取消殃及。
//
// 缺陷：非事务写路径（Create/Update/...）的失效用原始请求 ctx——请求在
// 提交瞬间被取消（客户端断开 / deadline 到期 / defer cancel）时，DelBatch
// 与 versions.Set 立即返回 context.Canceled，失效静默丢失：缓存残留写前
// 旧值直到 TTL，且查询版本未 bump ⇒ 整个 scope 的查询缓存一起脏到 TTL，
// 默认配置下完全无感知。修法：WithoutCancel 收进 invalidate/bloomAdd 内部。

import (
	"context"
	"sync"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// ctxAwareCache 模拟真实远端缓存（如 redis）：操作前检查 ctx，已取消即
// 返回 ctx.Err()。本地内存缓存不检查 ctx，取消在纯本地 fixture 上不可
// 观测——没有它本钉测不出缺陷。
type ctxAwareCache[T any] struct {
	inner dcache.CacheAside[T]
}

func (c *ctxAwareCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return c.inner.Get(ctx, key)
}
func (c *ctxAwareCache[T]) Set(ctx context.Context, key string, value *T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.inner.Set(ctx, key, value)
}
func (c *ctxAwareCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.inner.SetWithTTL(ctx, key, value, ttl)
}
func (c *ctxAwareCache[T]) Del(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.inner.Del(ctx, key)
}
func (c *ctxAwareCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.inner.DelBatch(ctx, keys...)
}
func (c *ctxAwareCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return c.inner.Exists(ctx, key)
}
func (c *ctxAwareCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	return c.inner.GetOrLoad(ctx, key)
}
func (c *ctxAwareCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	return c.inner.Refresh(ctx, key)
}

// ctxAwareVersions 查询缓存版本号后端：Set 前检查 ctx（同真实远端）。
type ctxAwareVersions struct {
	inner dcache.Cache[string]
}

func (v *ctxAwareVersions) Get(ctx context.Context, key string) (*string, error) {
	return v.inner.Get(ctx, key)
}
func (v *ctxAwareVersions) Set(ctx context.Context, key string, value *string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.inner.Set(ctx, key, value)
}
func (v *ctxAwareVersions) SetWithTTL(ctx context.Context, key string, value *string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.inner.SetWithTTL(ctx, key, value, ttl)
}
func (v *ctxAwareVersions) Del(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.inner.Del(ctx, key)
}
func (v *ctxAwareVersions) DelBatch(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.inner.DelBatch(ctx, keys...)
}
func (v *ctxAwareVersions) Exists(ctx context.Context, key string) (bool, error) {
	return v.inner.Exists(ctx, key)
}

// 写已提交后请求 ctx 被取消：实体键失效与查询版本 bump 都必须照常发生。
// 时序：Update 底层写成功 ⇒ fake 钩子立即 cancel（模拟客户端断开 /
// defer cancel 落在提交瞬间）⇒ 装饰器带着已取消 ctx 进入失效。修复前
// 两次失效都被 ctx 敏感后端拒绝（静默），修复后 invalidate 内部
// WithoutCancel，失效照常。
func TestInvalidateSurvivesCancelledRequestCtx(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo.onWriteCommitted = cancel // 写已提交 ⇒ 请求 ctx 取消

	// 按本钉关注面装配：实体删键（DelBatch）与版本 bump（versions.Set）
	// 都经 ctx 敏感后端，page/实体读缓存保持本地。
	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := &ctxAwareVersions{inner: dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})}
	qc, qcErr := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if qcErr != nil {
		t.Fatalf("NewQueryCache: %v", qcErr)
	}
	cr := cached.NewRepository[user, int64](repo,
		func(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
			return &ctxAwareCache[user]{inner: userCacheFactory(src)}
		},
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithQueryCache[user, int64](qc),
	)

	bg := context.Background()
	// 预热：点读与查询缓存都装上 alice
	if got, err := cr.GetByID(bg, 7); err != nil || got.Name != "alice" {
		t.Fatalf("warm GetByID: %v %+v", err, got)
	}
	if items, _, err := cr.List(bg, crud.NewQuery()); err != nil || len(items) != 1 || items[0].Name != "alice" {
		t.Fatalf("warm List: %v", err)
	}

	// 另一个写者改了库（缓存尚未感知）
	repo.byID[7] = &user{ID: 7, Name: "bob"}

	// 本请求的 Update：写提交瞬间 ctx 被取消，失效必须照样落地
	if _, err := cr.Update(ctx, &user{ID: 7, Name: "bob"}); err != nil {
		t.Fatalf("Update（写已提交必须成功）: %v", err)
	}

	// 点读：必须拿到 bob——修复前 DelBatch 在已取消 ctx 上被拒，残留 alice
	got, err := cr.GetByID(bg, 7)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("name = %q, want bob——写已提交后的失效不得被请求 ctx 取消吞掉（#141）", got.Name)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo.GetByID calls = %d, want 2（失效未生效则仍命中旧缓存）", n)
	}

	// 查询缓存：版本未 bump 则整体继续命中旧结果集（整个 scope 一起脏）
	items, _, err := cr.List(bg, crud.NewQuery())
	if err != nil {
		t.Fatalf("List after update: %v", err)
	}
	if len(items) != 1 || items[0].Name != "bob" {
		t.Fatalf("List after update = %+v, want [bob]——查询版本 bump 不得被请求 ctx 取消吞掉（#141）", items)
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（版本未 bump 则查询缓存继续旧命中）", n)
	}
}

// recordingBloom 记录 Add 的 bloom 假实现：已取消 ctx 上的 Add 被拒
// （同真实远端 bloom 的行为），用于观测补录是否被请求取消吞掉。
type recordingBloom struct {
	mu   sync.Mutex
	adds []string
}

func (b *recordingBloom) Exists(ctx context.Context, item string) (bool, error) {
	return true, nil // 可能存在 ⇒ 永远放行仓储，不参与本钉的关注面
}
func (b *recordingBloom) Add(ctx context.Context, item string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	b.adds = append(b.adds, item)
	b.mu.Unlock()
	return nil
}
func (b *recordingBloom) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.adds...)
}

// Create 提交后请求 ctx 被取消：bloom 补录必须照常——丢失 Add 会让后续
// 点读把存在的 ID 误判为「绝对不存在」而多打 DB（#141，性能退化面）。
func TestBloomAddSurvivesCancelledRequestCtx(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo.onWriteCommitted = cancel

	bloom := &recordingBloom{}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bloom),
	)

	if err := cr.Create(ctx, &user{ID: 3, Name: "n"}); err != nil {
		t.Fatalf("Create（写已提交必须成功）: %v", err)
	}
	adds := bloom.snapshot()
	if len(adds) != 1 || adds[0] != "3" {
		t.Fatalf("bloom adds = %v, want [3]——写已提交后的 bloom 补录不得被请求 ctx 取消吞掉（#141）", adds)
	}
}
