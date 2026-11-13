package cached_test

import (
	"context"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
)

// NewRepository 构造期 fail-fast（#184）：repo / newCache 为 nil、工厂
// 产物为 nil 都在装配点 panic（配置错误启动即暴露的对齐纪律），不留
// 「nil 在使用期才 panic、距装配点远且无现场」的坑。
func TestNewRepositoryFailFast(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: 必须在装配期 fail-fast panic（而非使用期 nil panic）", name)
			}
		}()
		fn()
	}

	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)

	mustPanic("repo 为 nil", func() {
		cached.NewRepository[user, int64](nil, userCacheFactory)
	})
	mustPanic("newCache 工厂为 nil", func() {
		cached.NewRepository[user, int64](repo, nil)
	})
	mustPanic("工厂返回 nil 缓存", func() {
		cached.NewRepository[user, int64](repo,
			func(src dcache.SourceFunc[user]) dcache.CacheAside[user] { return nil })
	})

	// 合法装配不受 fail-fast 影响。
	_ = cached.NewRepository[user, int64](repo, userCacheFactory)
}

// noIDEntity 主键字段名不叫 ID、无 GetID() 方法——缺省探测解析不出 ID
// 访问器的典型模型形态（#210 的触发面）。
type noIDEntity struct {
	UserID int64
	Name   string
}

func noIDCacheFactory(src dcache.SourceFunc[noIDEntity]) dcache.CacheAside[noIDEntity] {
	return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[noIDEntity]{
		TTL:     time.Minute,
		Source:  src,
		NullTTL: time.Minute,
	}))
}

// #210：开启 WithBloomFilter 但实体 ID 访问器解析不出（无 WithID、无
// GetID() 方法、无名为 ID 的字段）——Create 落库成功后 entityIDs 恒空，
// bloom 补录零信号空转，新实体点读被 bloomGate 永久判「不存在」且无
// 自愈路径。构造期 fail-fast，不留「使用期才暴露、无任何信号」的坑。
func TestNewRepositoryBloomWithoutIDAccessorPanics(t *testing.T) {
	repo := newFakeRepo[noIDEntity](func(e *noIDEntity) int64 { return e.UserID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}

	defer func() {
		if recover() == nil {
			t.Fatal("bloom 开启但无 ID 访问器必须在装配期 panic（否则新实体点读永久 NotFound 且零信号）")
		}
	}()
	cached.NewRepository[noIDEntity, int64](repo, noIDCacheFactory,
		cached.WithBloomFilter[noIDEntity, int64](bf))
}

// 正面锚点（#210）：实体有名为 ID 的可导出字段（缺省探测命中）时，不注
// 入 WithID 也可与 bloom 组装；Create 落库 → bloom 补录 → 点读可达。
func TestNewRepositoryBloomWithDefaultProbedID(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithBloomFilter[user, int64](bf))

	ctx := context.Background()
	if err := cr.Create(ctx, &user{ID: 7, Name: "n"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("Create 后 GetByID(7) 必须可达（bloom 已补录）: %v", err)
	}
	if got == nil || got.Name != "n" {
		t.Fatalf("GetByID(7) = %v, want Name=n", got)
	}
	if !bf.items["7"] {
		t.Fatal("新实体 ID 必须已补录进 bloom")
	}
}

// 对照面（#210）：未开 bloom 时缺 ID 访问器是合法降级（写路径只 bump
// 查询版本、TTL 兜底），构造期不得误伤。
func TestNewRepositoryWithoutBloomToleratesMissingIDAccessor(t *testing.T) {
	repo := newFakeRepo[noIDEntity](func(e *noIDEntity) int64 { return e.UserID }, nil)
	repo.byID[7] = &noIDEntity{UserID: 7, Name: "legacy"}

	cr := cached.NewRepository[noIDEntity, int64](repo, noIDCacheFactory)

	ctx := context.Background()
	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if err := cr.Create(ctx, &noIDEntity{UserID: 9, Name: "n"}); err != nil {
		t.Fatalf("Create（未开 bloom 时缺 ID 访问器必须保持合法降级）: %v", err)
	}
}
