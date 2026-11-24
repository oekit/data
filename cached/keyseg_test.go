package cached_test

import (
	"context"
	"sync"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// #285 cached 层级键段零校验——裸 ':' 拼接可构造跨实体串键。本文件钉住
// 键段边界立约：含 ':' 的静态段（prefix/typ）构造期 panic；运行期动态段
// （scope / ID 编码产物）含 ':' 时读降级直查、写跳过失效 + onError 上报
// （op="scope" / op="encode"），绝不静默串键。碰撞形态：scope "a"+ID
// "b:c" ≡ scope "a:id:b"+ID "c"（修复前拼出同一个 idKey）。

// colonDoc 文本主键实体：缺省编码 string 直通，ID 可携带 ':'（#285 的
// 触发面——文本主键 / 含分隔符的业务标识）。
type colonDoc struct {
	ID   string
	Name string
}

// colonRepo 作用域感知的字符串主键假仓储：同一 (scope, ID) 是不同的行。
// 只认真实现 GetByID / UpdateFields（写路径降级用例），其余桩实现保接口完整。
type colonRepo struct {
	mu    sync.Mutex
	rows  map[string]map[string]*colonDoc
	calls int
}

func newColonRepo() *colonRepo {
	return &colonRepo{rows: map[string]map[string]*colonDoc{}}
}

func (r *colonRepo) put(scope, id string, d *colonDoc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.rows[scope]
	if !ok {
		m = map[string]*colonDoc{}
		r.rows[scope] = m
	}
	m[id] = d
}

func (r *colonRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *colonRepo) GetByID(ctx context.Context, id string) (*colonDoc, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	scope, _ := ctx.Value(tenantKey{}).(string)
	if d, ok := r.rows[scope][id]; ok {
		return d, nil
	}
	return nil, crud.ErrNotFound
}

func (r *colonRepo) GetOne(ctx context.Context, q *crud.Query) (*colonDoc, error) {
	return nil, crud.ErrNotFound
}

func (r *colonRepo) GetByCondition(ctx context.Context, query any, args ...any) (*colonDoc, error) {
	return nil, crud.ErrNotFound
}

func (r *colonRepo) List(ctx context.Context, q *crud.Query) ([]colonDoc, int64, error) {
	return nil, 0, nil
}

func (r *colonRepo) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return false, nil
}

func (r *colonRepo) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return 0, nil
}

func (r *colonRepo) Create(ctx context.Context, entity *colonDoc) error { return nil }

func (r *colonRepo) BatchCreate(ctx context.Context, entities []*colonDoc, batchSize int) error {
	return nil
}

func (r *colonRepo) Update(ctx context.Context, entity *colonDoc, opts ...crud.UpdateOption) (int64, error) {
	return 1, nil
}

func (r *colonRepo) UpdateFields(ctx context.Context, id string, fields map[string]any) (int64, error) {
	return 1, nil
}

func (r *colonRepo) Delete(ctx context.Context, id string) (int64, error) { return 1, nil }

func (r *colonRepo) BatchDelete(ctx context.Context, ids []string) (int64, error) {
	return int64(len(ids)), nil
}

// colonCacheFactory 组一个「SingleFlight×Local」后端（与既有测试同款）。
func colonCacheFactory(src dcache.SourceFunc[colonDoc]) dcache.CacheAside[colonDoc] {
	return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[colonDoc]{
		TTL:     time.Minute,
		Source:  src,
		NullTTL: time.Minute,
	}))
}

// newColonCachedRepo 组带作用域与 onError 记录的缓存仓储。
func newColonCachedRepo(repo *colonRepo, ops *[]string) *cached.CachedRepository[colonDoc, string] {
	return cached.NewRepository[colonDoc, string](repo, colonCacheFactory,
		cached.WithID[colonDoc, string](func(d *colonDoc) string { return d.ID }),
		cached.WithScopeKey[colonDoc, string](scopeOf),
		cached.WithOnError[colonDoc, string](func(ctx context.Context, op string, keys []string, err error) {
			*ops = append(*ops, op)
		}),
	)
}

// opsContain 断言 onError 已上报全部给定 op（非静默）。
func opsContain(t *testing.T, ops []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, op := range ops {
			if op == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("onError 未上报 op=%q（已上报: %v）", w, ops)
		}
	}
}

// TestKeySegGuard_ColonSegmentsPanic 静态段（prefix/typ）含 ':' 构造期
// fail-fast（#285）：键段分层立约，装配期即暴露，不留运行期串键窗口。
func TestKeySegGuard_ColonSegmentsPanic(t *testing.T) {
	repo := newColonRepo()
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: 必须在装配期 fail-fast panic（含 ':' 的键段跨实体串键）", name)
			}
		}()
		fn()
	}

	mustPanic("typ 含 ':'", func() {
		cached.NewRepository[colonDoc, string](repo, colonCacheFactory,
			cached.WithType[colonDoc, string]("a:b"))
	})
	mustPanic("prefix 含 ':'", func() {
		cached.NewRepository[colonDoc, string](repo, colonCacheFactory,
			cached.WithPrefix[colonDoc, string]("p:q"))
	})

	// 对照面：不含 ':' 的合法段照常装配。
	_ = cached.NewRepository[colonDoc, string](repo, colonCacheFactory,
		cached.WithType[colonDoc, string]("colonDoc"),
		cached.WithPrefix[colonDoc, string]("cached"),
	)
}

// TestKeySegGuard_ColonIDNoCrossEntityCollision 碰撞构造回归（#285）：
// scope "a:id:b"+ID "c" 与 scope "a"+ID "b:c" 修复前拼出同一个键
// "cached:colonDoc:scope:a:id:b:c"——后读的一方会命中先读一方回填的
// 条目（跨实体串数据）。修复后两条路径都拒绝建键（scope 段 op="scope"、
// ID 段 op="encode" 上报），各自降级直查仓储拿自己的行，且不以歧义键
// 落缓存；干净段（scope "a"+ID "plain"）照常缓存。
func TestKeySegGuard_ColonIDNoCrossEntityCollision(t *testing.T) {
	repo := newColonRepo()
	repo.put("a:id:b", "c", &colonDoc{ID: "c", Name: "owner-B"})
	repo.put("a", "b:c", &colonDoc{ID: "b:c", Name: "owner-A"})
	repo.put("a", "plain", &colonDoc{ID: "plain", Name: "plain-A"})

	var ops []string
	cr := newColonCachedRepo(repo, &ops)

	// 先读 B（scope "a:id:b"——作用域段含 ':'）：必须降级直查拿 B 自己的行
	gotB, err := cr.GetByID(tenantCtx("a:id:b"), "c")
	if err != nil {
		t.Fatalf("GetByID(a:id:b, c): %v", err)
	}
	if gotB == nil || gotB.Name != "owner-B" {
		t.Fatalf("GetByID(a:id:b, c) = %+v, want owner-B", gotB)
	}

	// 再读 A（scope "a" + ID "b:c"——ID 段含 ':'）：修复前此刻会命中 B
	// 回填的条目（串键），修复后必须降级直查拿 A 自己的行
	gotA, err := cr.GetByID(tenantCtx("a"), "b:c")
	if err != nil {
		t.Fatalf("GetByID(a, b:c): %v", err)
	}
	if gotA == nil || gotA.Name != "owner-A" {
		t.Fatalf("跨实体串键：GetByID(a, b:c) = %+v, want owner-A", gotA)
	}

	// 报错可观测：作用域段与 ID 段各上报一次，不是静默降级
	opsContain(t, ops, "scope", "encode")

	// 歧义键不落缓存：A 重复读仍直查仓储（读数递增）
	calls := repo.count()
	if _, err := cr.GetByID(tenantCtx("a"), "b:c"); err != nil {
		t.Fatalf("repeat GetByID(a, b:c): %v", err)
	}
	if repo.count() != calls+1 {
		t.Fatalf("含 ':' 的 ID 必须每次直查（不以歧义键落缓存），仓储读数 %d → %d", calls, repo.count())
	}

	// 对照面：干净 scope/ID 照常缓存——首次回源，二次命中（仓储读数不变）
	if _, err := cr.GetByID(tenantCtx("a"), "plain"); err != nil {
		t.Fatalf("GetByID(a, plain): %v", err)
	}
	calls = repo.count()
	if _, err := cr.GetByID(tenantCtx("a"), "plain"); err != nil {
		t.Fatalf("repeat GetByID(a, plain): %v", err)
	}
	if repo.count() != calls {
		t.Fatalf("干净段必须命中缓存（仓储读数 %d → %d）", calls, repo.count())
	}
}

// TestKeySegGuard_ColonIDGetByIDs GetByIDs 批量点读的键段校验（#285）：
// 含 ':' 的 ID 不进批量键（留在 pending 走逐个点读路径），干净 ID 照常
// 走 MGet 快路径——两类都拿到自己的行，互不串。
func TestKeySegGuard_ColonIDGetByIDs(t *testing.T) {
	repo := newColonRepo()
	repo.put("a", "b:c", &colonDoc{ID: "b:c", Name: "owner-A"})
	repo.put("a", "plain", &colonDoc{ID: "plain", Name: "plain-A"})

	var ops []string
	cr := newColonCachedRepo(repo, &ops)

	got, err := cr.GetByIDs(tenantCtx("a"), []string{"plain", "b:c"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetByIDs 返回 %d 条, want 2: %v", len(got), got)
	}
	if d := got["b:c"]; d == nil || d.Name != "owner-A" {
		t.Fatalf("got[b:c] = %+v, want owner-A（不得被干净 ID 的条目顶替）", d)
	}
	if d := got["plain"]; d == nil || d.Name != "plain-A" {
		t.Fatalf("got[plain] = %+v, want plain-A", d)
	}
	opsContain(t, ops, "encode")
}

// TestKeySegGuard_ColonScopeWriteSkipsInvalidate 写路径（#285）：scope
// 含 ':' 时无法定位该失效的键 ⇒ 跳过失效并上报 op="scope"（TTL 兜底）；
// 写库本身已成功，操作不报错。
func TestKeySegGuard_ColonScopeWriteSkipsInvalidate(t *testing.T) {
	repo := newColonRepo()
	repo.put("a:b", "42", &colonDoc{ID: "42", Name: "x"})

	var ops []string
	cr := newColonCachedRepo(repo, &ops)

	if _, err := cr.UpdateFields(tenantCtx("a:b"), "42", map[string]any{"name": "y"}); err != nil {
		t.Fatalf("scope 含 ':' 的写操作不得报错（写库已成功，失效降级 + TTL 兜底）: %v", err)
	}
	opsContain(t, ops, "scope")
}
