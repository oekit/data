package cached_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// newQueryCachedRepo 组装开启查询缓存的装饰器（版本/整页/单条三个键空间）。
func newQueryCachedRepo(repo *fakeRepo[user]) *cached.CachedRepository[user, int64] {
	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})

	qc, err := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if err != nil {
		panic(err) // 测试装配，参数固定合法
	}
	return cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc))
}

// List 走查询缓存：第二次命中；写操作 bump 版本后重建并拿到新数据。
func TestListCachedUntilWrite(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := 0; i < 2; i++ {
		items, total, err := cr.List(ctx, crud.NewQuery())
		if err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
		if len(items) != 1 || total != 1 {
			t.Fatalf("List #%d = %d items/%d total, want 1/1", i+1, len(items), total)
		}
	}
	if n := repo.count("List"); n != 1 {
		t.Fatalf("repo.List calls = %d, want 1（第二次必须命中查询缓存）", n)
	}

	if err := cr.Update(ctx, &user{ID: 1, Name: "a2"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	items, _, err := cr.List(ctx, crud.NewQuery())
	if err != nil {
		t.Fatalf("List after write: %v", err)
	}
	if items[0].Name != "a2" {
		t.Fatalf("name after write = %q, want a2（版本 bump 必须让列表失效）", items[0].Name)
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls after write = %d, want 2", n)
	}
}

// 不同 Query 指纹不同键，各自回源一次。
func TestListDistinctQueriesDistinctKeys(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	q1 := crud.NewQuery().Where("name", "=", "x")
	q2 := crud.NewQuery().Where("name", "=", "y")

	for i := 0; i < 2; i++ {
		if _, _, err := cr.List(ctx, q1); err != nil {
			t.Fatalf("List q1 #%d: %v", i+1, err)
		}
		if _, _, err := cr.List(ctx, q2); err != nil {
			t.Fatalf("List q2 #%d: %v", i+1, err)
		}
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（每个查询指纹各回源一次）", n)
	}
}

// GetOne 同样走查询缓存并在写后失效。
func TestGetOneCachedUntilWrite(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := 0; i < 2; i++ {
		got, err := cr.GetOne(ctx, crud.NewQuery().Where("id", "=", 1))
		if err != nil {
			t.Fatalf("GetOne #%d: %v", i+1, err)
		}
		if got.Name != "a" {
			t.Fatalf("GetOne #%d name = %q", i+1, got.Name)
		}
	}
	if n := repo.count("GetOne"); n != 1 {
		t.Fatalf("repo.GetOne calls = %d, want 1", n)
	}

	if err := cr.Update(ctx, &user{ID: 1, Name: "a2"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := cr.GetOne(ctx, crud.NewQuery().Where("id", "=", 1))
	if err != nil {
		t.Fatalf("GetOne after write: %v", err)
	}
	if got.Name != "a2" {
		t.Fatalf("name after write = %q, want a2", got.Name)
	}
}

// GetOne 负结果不缓存：两次 miss 都回源（普通 Cache 接口无 null marker 通道）。
func TestGetOneNegativeNotCached(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := 0; i < 2; i++ {
		if _, err := cr.GetOne(ctx, crud.NewQuery().Where("id", "=", 99)); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetOne #%d err = %v, want ErrNotFound", i+1, err)
		}
	}
	if n := repo.count("GetOne"); n != 2 {
		t.Fatalf("repo.GetOne calls = %d, want 2（负结果不缓存）", n)
	}
}

// ForUpdate 读永不缓存：每次都回源（锁读必须拿最权威的数据）。
func TestForUpdateNeverCached(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := 0; i < 3; i++ {
		if _, _, err := cr.List(ctx, crud.NewQuery().ForUpdate()); err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
	}
	if n := repo.count("List"); n != 3 {
		t.Fatalf("repo.List calls = %d, want 3（ForUpdate 不缓存）", n)
	}
}

// 指纹失败：降级直通，不把错误当结果。
func TestFingerprintFailurePassesThrough(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()

	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})
	qc, err := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewQueryCache: %v", err)
	}
	qc.WithFingerprint(func(spec crud.QuerySpec) (string, error) {
		return "", errors.New("fingerprint unavailable")
	})
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc))

	for i := 0; i < 2; i++ {
		if _, _, err := cr.List(ctx, crud.NewQuery()); err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（指纹失败必须直通）", n)
	}
}

// GetOne 的 ForUpdate 读同样永不缓存。
func TestGetOneForUpdateNeverCached(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := 0; i < 3; i++ {
		if _, err := cr.GetOne(ctx, crud.NewQuery().ForUpdate()); err != nil {
			t.Fatalf("GetOne #%d: %v", i+1, err)
		}
	}
	if n := repo.count("GetOne"); n != 3 {
		t.Fatalf("repo.GetOne calls = %d, want 3（ForUpdate 不缓存）", n)
	}
}

// 版本键读失败：降级以空版本建键，列表缓存仍可用且不报错。
func TestVersionReadFailureFallsBack(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	ctx := context.Background()

	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := &flakyGetCache[string]{inner: dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})}
	qc, err := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewQueryCache: %v", err)
	}
	var reported int
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			if op == "read" {
				reported++
			}
		}))

	for i := 0; i < 2; i++ {
		if _, _, err := cr.List(ctx, crud.NewQuery()); err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
	}
	if n := repo.count("List"); n != 1 {
		t.Fatalf("repo.List calls = %d, want 1（空版本键必须仍可命中缓存）", n)
	}
	if reported == 0 {
		t.Fatal("版本读失败必须经 onError 上报")
	}
}

// TTL 必须为正：无 TTL 意味着失效失败时可能永久脏读。
func TestQueryCacheRejectsNonPositiveTTL(t *testing.T) {
	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})

	for _, ttl := range []time.Duration{0, -1 * time.Second} {
		if _, err := cached.NewQueryCache[user](entityQ, page, versions, ttl); err == nil {
			t.Fatalf("ttl=%v 必须被拒绝", ttl)
		}
	}
}

// nil 缓存实例必须在构造期被拒绝，而不是等到使用时 nil panic。
func TestQueryCacheRejectsNilBackends(t *testing.T) {
	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})

	cases := []struct {
		name      string
		entity    dcache.Cache[user]
		pageC     dcache.Cache[cached.QueryPage[user]]
		versionsC dcache.Cache[string]
	}{
		{"nil entity", nil, page, versions},
		{"nil page", entityQ, nil, versions},
		{"nil versions", entityQ, page, nil},
	}
	for _, tc := range cases {
		if _, err := cached.NewQueryCache[user](tc.entity, tc.pageC, tc.versionsC, time.Minute); err == nil {
			t.Fatalf("%s: 构造必须报错", tc.name)
		}
	}
}

// flakyGetCache 只让 Get 失败（模拟版本后端读抖动），Set 正常。
type flakyGetCache[T any] struct {
	inner dcache.Cache[T]
}

func (f *flakyGetCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return nil, errors.New("versions backend down")
}
func (f *flakyGetCache[T]) Set(ctx context.Context, key string, value *T) error {
	return f.inner.Set(ctx, key, value)
}
func (f *flakyGetCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	return f.inner.SetWithTTL(ctx, key, value, ttl)
}
func (f *flakyGetCache[T]) Del(ctx context.Context, key string) error {
	return f.inner.Del(ctx, key)
}
func (f *flakyGetCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	return f.inner.DelBatch(ctx, keys...)
}
func (f *flakyGetCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return f.inner.Exists(ctx, key)
}

// 无区分度参数（非导出字段结构体恒编 {}）：拒绝指纹，防止不同取值串数据。
type valuerLike struct{ v int }

func TestDefaultFingerprintRejectsLossyArgs(t *testing.T) {
	q := crud.NewQuery().Where("data", "=", valuerLike{v: 1})
	fp, err := cached.DefaultFingerprint[user](q.Spec())
	if err == nil || fp != "" {
		t.Fatalf("lossy 参数必须拒绝指纹，got fp=%q err=%v", fp, err)
	}

	// 正常参数仍可指纹，且不同取值不同指纹
	f1, err1 := cached.DefaultFingerprint[user](crud.NewQuery().Where("age", "=", 1).Spec())
	f2, err2 := cached.DefaultFingerprint[user](crud.NewQuery().Where("age", "=", 2).Spec())
	if err1 != nil || err2 != nil {
		t.Fatalf("正常参数不应报错: %v %v", err1, err2)
	}
	if f1 == f2 {
		t.Fatal("不同取值必须产生不同指纹")
	}
}
