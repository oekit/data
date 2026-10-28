package cached_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// tenantKey 是租户标识在 ctx 里的 key。
type tenantKey struct{}

// tenantCtx 返回带租户标识的 ctx。
func tenantCtx(tenant string) context.Context {
	return context.WithValue(context.Background(), tenantKey{}, tenant)
}

// tenantRepo 是「按租户隔离」的假仓储：同一个 ID 在不同租户下是**不同的行**
// （模拟 PostgreSQL RLS / 每租户连接 / 每租户库）。
//
// 只覆写 GetByID（其余方法沿用 fakeRepo 的假实现，测试用不到）。
type tenantRepo struct {
	*fakeRepo[user]
	mu    sync.Mutex
	rows  map[string]map[int64]*user
	calls int
}

func newTenantRepo() *tenantRepo {
	return &tenantRepo{
		fakeRepo: newFakeRepo[user](func(u *user) int64 { return u.ID }, nil),
		rows:     map[string]map[int64]*user{},
	}
}

// put 放一行（租户 → ID → 行）。
func (r *tenantRepo) put(tenant string, u *user) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.rows[tenant]
	if !ok {
		m = map[int64]*user{}
		r.rows[tenant] = m
	}
	m[u.ID] = u
}

// GetByID 按租户取行；该租户没有这行时返回 crud.ErrNotFound。
func (r *tenantRepo) GetByID(ctx context.Context, id int64) (*user, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	tenant, _ := ctx.Value(tenantKey{}).(string)
	u, ok := r.rows[tenant][id]
	if !ok {
		return nil, crud.ErrNotFound
	}
	return u, nil
}

// count 返回仓储被调用的次数。
func (r *tenantRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// tenantCacheFactory 组一个「SingleFlight×Local」后端。
func tenantCacheFactory(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
	return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[user]{
		TTL:     time.Minute,
		Source:  src,
		NullTTL: time.Minute,
	}))
}

// scopeOf 从 ctx 取租户号作为作用域键。
func scopeOf(ctx context.Context) (string, error) {
	t, ok := ctx.Value(tenantKey{}).(string)
	if !ok {
		return "", errors.New("no tenant in ctx")
	}
	return t, nil
}

// TestScopeKey_PreventsCrossTenantRead 回归测试：作用域隔离开启后，两个租户
// 的同 ID 行**互不可见**。
//
// 缺陷成因：缓存键只有「前缀:类型:id:N」三段，**没有租户维度** ⇒ 租户 A
// 写入的缓存条目会被租户 B 命中 ⇒ **跨租户读泄漏**。
func TestScopeKey_PreventsCrossTenantRead(t *testing.T) {
	repo := newTenantRepo()
	repo.put("A", &user{ID: 1, Name: "alice-A"})
	repo.put("B", &user{ID: 1, Name: "bob-B"})

	cr := cached.NewRepository[user, int64](repo, tenantCacheFactory,
		cached.WithScopeKey[user, int64](scopeOf))

	gotA, err := cr.GetByID(tenantCtx("A"), 1)
	if err != nil {
		t.Fatalf("A GetByID: %v", err)
	}
	if gotA.Name != "alice-A" {
		t.Fatalf("A 读到 %q，期望 alice-A", gotA.Name)
	}

	gotB, err := cr.GetByID(tenantCtx("B"), 1)
	if err != nil {
		t.Fatalf("B GetByID: %v", err)
	}
	if gotB.Name != "bob-B" {
		t.Fatalf("B 读到 %q（期望 bob-B）——跨租户读泄漏！", gotB.Name)
	}

	// 再读一次：两个租户都必须命中各自的缓存（不回落仓储）
	before := repo.count()
	if _, err := cr.GetByID(tenantCtx("A"), 1); err != nil {
		t.Fatalf("A 二次 GetByID: %v", err)
	}
	if _, err := cr.GetByID(tenantCtx("B"), 1); err != nil {
		t.Fatalf("B 二次 GetByID: %v", err)
	}
	if repo.count() != before {
		t.Fatalf("二次读取回落了仓储（calls %d → %d）", before, repo.count())
	}
}

// TestScopeKey_NullMarkerNotPollutedAcrossTenants 回归测试：某租户下
// 「不存在」写下的 null marker **不得**让另一个租户明明存在的行报 NotFound。
//
// 缺陷成因：null marker 落在共享键上 ⇒ 租户 A 查不到 ID 2 → 写 marker ⇒
// 租户 B 查 ID 2（有这行）命中 marker ⇒ 报 NotFound（"不存在"跨租户污染）。
func TestScopeKey_NullMarkerNotPollutedAcrossTenants(t *testing.T) {
	repo := newTenantRepo()
	repo.put("B", &user{ID: 2, Name: "bob-B"})

	cr := cached.NewRepository[user, int64](repo, tenantCacheFactory,
		cached.WithScopeKey[user, int64](scopeOf))

	// 租户 A 没有 ID 2 ⇒ 写 null marker
	if _, err := cr.GetByID(tenantCtx("A"), 2); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("A 查不存在的 ID 2 期望 NotFound，实得 %v", err)
	}

	// 租户 B 有 ID 2 ⇒ 必须拿到真值，不能命中 A 的 null marker
	got, err := cr.GetByID(tenantCtx("B"), 2)
	if err != nil {
		t.Fatalf("B GetByID: %v（命中了别的租户的 null marker）", err)
	}
	if got.Name != "bob-B" {
		t.Fatalf("B 读到 %q，期望 bob-B", got.Name)
	}
}

// TestScopeKey_ResolutionFailure_FallsBackToRepo 回归测试：作用域解析失败
// 时，读路径**降级直查仓储**（权威源），且**绝不**回落共享缓存键。
func TestScopeKey_ResolutionFailure_FallsBackToRepo(t *testing.T) {
	repo := newTenantRepo()
	repo.put("A", &user{ID: 3, Name: "alice-A"})

	var reported []string
	cr := cached.NewRepository[user, int64](repo, tenantCacheFactory,
		cached.WithScopeKey[user, int64](func(ctx context.Context) (string, error) {
			return "", errors.New("租户上下文缺失")
		}),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			reported = append(reported, op)
		}))

	// 解析器失败（租户服务不可用等），但 ctx 里租户信息仍在 ⇒ 仓储能查到行
	got, err := cr.GetByID(tenantCtx("A"), 3)
	if err != nil {
		t.Fatalf("作用域解析失败时应降级直查仓储，实得错误 %v", err)
	}
	if got.Name != "alice-A" {
		t.Fatalf("读到 %q，期望 alice-A", got.Name)
	}
	var sawScope bool
	for _, op := range reported {
		if op == "scope" {
			sawScope = true
		}
	}
	if !sawScope {
		t.Fatalf("作用域解析失败未上报（op=%v）", reported)
	}
}

// TestScopeKey_EmptyScope_Rejected 回归测试：作用域键为空串时被拒绝
// （空串等于没开隔离 ⇒ 会串数据）。
func TestScopeKey_EmptyScope_Rejected(t *testing.T) {
	repo := newTenantRepo()
	repo.put("A", &user{ID: 4, Name: "alice-A"})

	cr := cached.NewRepository[user, int64](repo, tenantCacheFactory,
		cached.WithScopeKey[user, int64](func(ctx context.Context) (string, error) {
			return "", nil
		}))

	// 空作用域 ⇒ 降级直查仓储（不报错、不回落共享键）
	got, err := cr.GetByID(tenantCtx("A"), 4)
	if err != nil {
		t.Fatalf("空作用域应降级直查仓储，实得错误 %v", err)
	}
	if got.Name != "alice-A" {
		t.Fatalf("读到 %q，期望 alice-A", got.Name)
	}
}

// TestScopeKey_Disabled_KeepsLegacyKeyShape 护栏：未启用作用域隔离时，行为
// 与历史一致（不改变既有部署的缓存键）。
func TestScopeKey_Disabled_KeepsLegacyKeyShape(t *testing.T) {
	repo := newTenantRepo()
	repo.put("A", &user{ID: 5, Name: "alice-A"})

	cr := cached.NewRepository[user, int64](repo, tenantCacheFactory)
	got, err := cr.GetByID(tenantCtx("A"), 5)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "alice-A" {
		t.Fatalf("读到 %q，期望 alice-A", got.Name)
	}
}
