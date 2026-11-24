package cached_test

import (
	"context"
	"errors"
	"sync/atomic"
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

	if _, err := cr.Update(ctx, &user{ID: 1, Name: "a2"}); err != nil {
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

	if _, err := cr.Update(ctx, &user{ID: 1, Name: "a2"}); err != nil {
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

// GetOne 负结果在 HitGetter 后端（LocalCache）写负标记（#253）：第二次
// 直接命中负标记，不再回源；经本仓储的写操作 bump 版本后立即失效。
func TestGetOneNegativeCachedWithHitGetter(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)
	q := crud.NewQuery().Where("id", "=", 99)

	for i := range 2 {
		if _, err := cr.GetOne(ctx, q); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetOne #%d err = %v, want ErrNotFound", i+1, err)
		}
	}
	if n := repo.count("GetOne"); n != 1 {
		t.Fatalf("repo.GetOne calls = %d, want 1（负结果命中负标记，不得回源）", n)
	}

	// 经仓储写入（bump 版本）后负标记必须立即失效
	if err := cr.Create(ctx, &user{ID: 99, Name: "late"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := cr.GetOne(ctx, q)
	if err != nil || got == nil || got.Name != "late" {
		t.Fatalf("写后 GetOne = %v, %v; want late（负标记未被版本 bump 失效）", got, err)
	}
	if n := repo.count("GetOne"); n != 2 {
		t.Fatalf("repo.GetOne calls after write = %d, want 2", n)
	}
}

// List 空页在 HitGetter 后端写负标记（#253）：第二次不再回源；经本仓储
// 的写操作 bump 版本后立即失效。
//
// 与 #167 历史口径的差异（有意）：负结果一旦缓存，**带外**写入（不经过
// 本仓储、不 bump 版本）在条目 TTL 内不可见——这是负结果防穿透的必然
// 取舍；经本仓储的写路径保持一致（版本 bump）。
func TestListNegativeCachedWithHitGetter(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	for i := range 2 {
		items, total, err := cr.List(ctx, crud.NewQuery())
		if err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
		if len(items) != 0 || total != 0 {
			t.Fatalf("List #%d = %d items/%d total, want 0/0", i+1, len(items), total)
		}
	}
	if n := repo.count("List"); n != 1 {
		t.Fatalf("repo.List calls = %d, want 1（空页命中负标记，不得回源）", n)
	}

	if err := cr.Create(ctx, &user{ID: 1, Name: "new"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	items, total, err := cr.List(ctx, crud.NewQuery())
	if err != nil {
		t.Fatalf("写后 List: %v", err)
	}
	if len(items) != 1 || total != 1 {
		t.Fatalf("写后 List = %d items/%d total, want 1/1（空页负标记未被版本 bump 失效）", len(items), total)
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls after write = %d, want 2", n)
	}
}

// plainCache 抹掉后端可选能力接口（只保留 Cache 最小面），模拟不具备
// HitGetter 的后端（SingleFlightCache / RedisCache 当前形态）。
type plainCache[T any] struct{ dcache.Cache[T] }

// 不具备 HitGetter 的后端保持历史行为：负结果既不写标记也不可识别，
// 每次回源（#253 的对照面——修复不得把「无法区分」当成「确定不存在」）。
func TestNegativeNotCachedWithoutHitGetter(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()

	entityQ := &plainCache[user]{Cache: dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})}
	pageQ := &plainCache[cached.QueryPage[user]]{Cache: dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})}
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})
	qc, err := cached.NewQueryCache[user](entityQ, pageQ, versions, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewQueryCache: %v", err)
	}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc))

	for i := range 2 {
		if _, err := cr.GetOne(ctx, crud.NewQuery().Where("id", "=", 99)); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetOne #%d err = %v, want ErrNotFound", i+1, err)
		}
		if _, _, err := cr.List(ctx, crud.NewQuery()); err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
	}
	if n := repo.count("GetOne"); n != 2 {
		t.Fatalf("repo.GetOne calls = %d, want 2（无 HitGetter 时负结果不得缓存）", n)
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（无 HitGetter 时空页不得缓存）", n)
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

// 指纹失败：降级直通，不把错误当结果；error 降级与空串降级可辨（#251）。
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
	var gotOps []string
	var gotErrs []error
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			gotOps = append(gotOps, op)
			gotErrs = append(gotErrs, err)
		}))

	for i := 0; i < 2; i++ {
		if _, _, err := cr.List(ctx, crud.NewQuery()); err != nil {
			t.Fatalf("List #%d: %v", i+1, err)
		}
	}
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（指纹失败必须直通）", n)
	}
	if len(gotOps) != 2 || gotOps[0] != "fingerprint" {
		t.Fatalf("指纹 error 降级必须上报 op=fingerprint，got %v", gotOps)
	}
	if errors.Is(gotErrs[0], cached.ErrFingerprintDegraded) {
		t.Fatal("error 降级不应被标成空串哨兵（两者必须可辨）")
	}
}

// 指纹函数返回空串（合法的「本次别缓存」形态）必须与 error 降级在
// 观测面上等价（#251）：onError 收到 op="fingerprint" 且 err 命中
// ErrFingerprintDegraded，而不是因 err==nil 静默零上报。
func TestFingerprintEmptyStringReportsDegraded(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()

	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour})
	qc, err := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewQueryCache: %v", err)
	}
	qc.WithFingerprint(func(spec crud.QuerySpec) (string, error) {
		return "", nil // 契约允许的「本次别缓存」，不是 error
	})
	var gotOps []string
	var gotErrs []error
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			gotOps = append(gotOps, op)
			gotErrs = append(gotErrs, err)
		}))

	for i := 0; i < 2; i++ {
		if _, err := cr.GetOne(ctx, crud.NewQuery()); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetOne #%d err = %v, want ErrNotFound", i+1, err)
		}
	}
	if n := repo.count("GetOne"); n != 2 {
		t.Fatalf("repo.GetOne calls = %d, want 2（空串指纹必须直通）", n)
	}
	if len(gotOps) != 2 {
		t.Fatalf("空串降级必须每次上报（got %d 次），观测面不得静默", len(gotOps))
	}
	for i := range gotOps {
		if gotOps[i] != "fingerprint" {
			t.Fatalf("第 %d 次上报 op = %q, want fingerprint", i+1, gotOps[i])
		}
		if !errors.Is(gotErrs[i], cached.ErrFingerprintDegraded) {
			t.Fatalf("第 %d 次上报 err = %v, want ErrFingerprintDegraded", i+1, gotErrs[i])
		}
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

// 版本键读失败：无法确认当前令牌 ⇒ 查询缓存降级直通，绝不以空版本
// 建键回落到上一轮降级窗口的历史条目（#250）；失败经 onError 上报。
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
	if n := repo.count("List"); n != 2 {
		t.Fatalf("repo.List calls = %d, want 2（版本读失败必须降级直通，不得用空版本键命中历史条目）", n)
	}
	if reported == 0 {
		t.Fatal("版本读失败必须经 onError 上报")
	}
}

// 版本键过期不得回落空版本键（#250）：空版本键上的历史条目不受任何
// bump 约束，旧实现会在 versions 过期后命中写前旧值。修复后版本缺失
// 签发新令牌，旧令牌条目自然作废，写后读保持新值。
func TestVersionKeyExpiryNoStaleHit(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "v0"}
	ctx := context.Background()

	// 假时钟只注入 versions：手动推进即确定性驱动版本键过期，无 sleep。
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	entityQ := dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute})
	page := dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute})
	versions := dcache.NewLocalCache(dcache.LocalConfig[string]{
		TTL:               time.Hour,
		NoBackgroundSweep: true,
		Now:               func() time.Time { return time.Unix(0, clock.Load()) },
	})
	qc, err := cached.NewQueryCache[user](entityQ, page, versions, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewQueryCache: %v", err)
	}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory, cached.WithQueryCache[user, int64](qc))

	q := crud.NewQuery().Where("id", "=", 1)
	// t0：版本键缺失，首次读回填 v0
	got, err := cr.GetOne(ctx, q)
	if err != nil || got == nil || got.Name != "v0" {
		t.Fatalf("首次 GetOne = %v, %v; want v0", got, err)
	}

	// t1：写路径 bump 版本，读取得 v1
	if _, werr := cr.Update(ctx, &user{ID: 1, Name: "v1"}); werr != nil {
		t.Fatalf("Update: %v", werr)
	}
	got, err = cr.GetOne(ctx, q)
	if err != nil || got == nil || got.Name != "v1" {
		t.Fatalf("写后 GetOne = %v, %v; want v1", got, err)
	}

	// t2：versions 过期——旧实现回落空版本键命中 t0 的 v0
	clock.Add(int64(2 * time.Hour))
	got, err = cr.GetOne(ctx, q)
	if err != nil || got == nil {
		t.Fatalf("版本过期后 GetOne = %v, %v", got, err)
	}
	if got.Name != "v1" {
		t.Fatalf("版本键过期后读到 %q, want v1（空版本键命中了写前旧值，#250）", got.Name)
	}
	if n := repo.count("GetOne"); n != 3 {
		t.Fatalf("repo.GetOne calls = %d, want 3（版本过期后必须重建，不得命中空版本键条目）", n)
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

// 容器形态的无区分度参数同样必须 fail-closed（#197）：切片/map 包住的
// {} 元素让容器编码不再恰为 {} / null，旧判据（只认整段恰为 {} / null）
// 放行 ⇒ 两条不同查询同指纹串数据。
func TestDefaultFingerprintRejectsLossyContainerArgs(t *testing.T) {
	cases := []struct {
		name string
		arg  any
	}{
		{"切片元素无区分度", []valuerLike{{v: 1}}},
		{"map 值无区分度", map[string]any{"k": valuerLike{v: 1}}},
		{"嵌套容器", []any{[]any{valuerLike{v: 1}}}},
		{"容器含 null 元素", []any{nil, 1}},
	}
	for _, tc := range cases {
		q := crud.NewQuery().Where("data", "IN", tc.arg)
		fp, err := cached.DefaultFingerprint[user](q.Spec())
		if err == nil || fp != "" {
			t.Fatalf("%s: 必须拒绝指纹，got fp=%q err=%v", tc.name, fp, err)
		}
	}

	// 对照：容器内全部元素可区分时照常指纹，且不同取值指纹不同。
	f1, err1 := cached.DefaultFingerprint[user](crud.NewQuery().Where("data", "IN", []int64{1, 2}).Spec())
	f2, err2 := cached.DefaultFingerprint[user](crud.NewQuery().Where("data", "IN", []int64{3, 4}).Spec())
	if err1 != nil || err2 != nil || f1 == "" || f2 == "" {
		t.Fatalf("可区分容器参数不应拒绝: %q/%v %q/%v", f1, err1, f2, err2)
	}
	if f1 == f2 {
		t.Fatal("不同取值必须产生不同指纹")
	}
}

// 端到端回归（#197）：两条语义不同的 IN 查询（元素无区分度，旧判据下
// 同指纹）必须各自回源拿到各自的行——修复前第二条直接命中第一条的
// 查询缓存条目（串数据，repo 只被回源一次）。
func TestQueryCacheLossyContainerCollisionDegrades(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "row-1"}
	ctx := context.Background()
	cr := newQueryCachedRepo(repo)

	got1, err := cr.GetOne(ctx, crud.NewQuery().Where("data", "IN", []valuerLike{{v: 1}}))
	if err != nil || got1 == nil || got1.Name != "row-1" {
		t.Fatalf("GetOne q1 = %v, %v; want row-1", got1, err)
	}

	// 仓储状态翻转：v=2 的查询语义上依赖 row-2。
	repo.mu.Lock()
	repo.byID = map[int64]*user{2: {ID: 2, Name: "row-2"}}
	repo.mu.Unlock()

	got2, err := cr.GetOne(ctx, crud.NewQuery().Where("data", "IN", []valuerLike{{v: 2}}))
	if err != nil || got2 == nil || got2.Name != "row-2" {
		t.Fatalf("GetOne q2 = %v, %v; want row-2（同指纹命中 q1 缓存条目即串数据）", got2, err)
	}
	if n := repo.count("GetOne"); n != 2 {
		t.Fatalf("repo.GetOne calls = %d, want 2（无区分度容器参数必须降级直通，各自回源）", n)
	}
}
