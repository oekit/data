package cached_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx"
	"github.com/oekit/gormx/crud"
)

// recordingCache 记录失效事件的缓存包装（供时序断言）。
type recordingCache[T any] struct {
	inner  dcache.CacheAside[T]
	events *[]string
}

func (r *recordingCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return r.inner.Get(ctx, key)
}
func (r *recordingCache[T]) Set(ctx context.Context, key string, value *T) error {
	return r.inner.Set(ctx, key, value)
}
func (r *recordingCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	return r.inner.SetWithTTL(ctx, key, value, ttl)
}
func (r *recordingCache[T]) Del(ctx context.Context, key string) error {
	*r.events = append(*r.events, "cache:del:"+key)
	return r.inner.Del(ctx, key)
}
func (r *recordingCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		*r.events = append(*r.events, "cache:del:"+k)
	}
	return r.inner.DelBatch(ctx, keys...)
}
func (r *recordingCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return r.inner.Exists(ctx, key)
}
func (r *recordingCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	return r.inner.GetOrLoad(ctx, key)
}
func (r *recordingCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	return r.inner.Refresh(ctx, key)
}

// delFailCache 只让 Del/DelBatch 失败（模拟删缓存时 Redis 抖动）。
type delFailCache[T any] struct {
	inner dcache.CacheAside[T]
}

func (d *delFailCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return d.inner.Get(ctx, key)
}
func (d *delFailCache[T]) Set(ctx context.Context, key string, value *T) error {
	return d.inner.Set(ctx, key, value)
}
func (d *delFailCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	return d.inner.SetWithTTL(ctx, key, value, ttl)
}
func (d *delFailCache[T]) Del(ctx context.Context, key string) error {
	return errors.New("del failed")
}
func (d *delFailCache[T]) DelBatch(ctx context.Context, keys ...string) error {
	return errors.New("delbatch failed")
}
func (d *delFailCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return d.inner.Exists(ctx, key)
}
func (d *delFailCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	return d.inner.GetOrLoad(ctx, key)
}
func (d *delFailCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	return d.inner.Refresh(ctx, key)
}

// --- 批次 2：写路径失效 ---

// Update 成功后实体键失效：下次 GetByID 回源拿到新值。
func TestUpdateInvalidatesEntityCache(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()
	cr := newCachedRepo(repo)

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm GetByID: %v", err)
	}

	if err := cr.Update(ctx, &user{ID: 7, Name: "bob"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("name after update = %q, want bob（缓存必须已失效）", got.Name)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo.GetByID calls = %d, want 2（warm + 失效后回源）", n)
	}
}

// Delete 后回源拿到 ErrNotFound（并经 null marker 稳定住）。
func TestDeleteInvalidatesEntityCache(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()
	cr := newCachedRepo(repo)

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm GetByID: %v", err)
	}
	if err := cr.Delete(ctx, 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := cr.GetByID(ctx, 7); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetByID #%d after delete err = %v, want ErrNotFound", i+1, err)
		}
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo.GetByID calls = %d, want 2（warm + 删除后一次回源，第二次命中 null marker）", n)
	}
}

// 预置主键的 Create 必须清掉此前的 null marker，否则新行点读永远不存在。
func TestCreateWithPresetIDClearsNullMarker(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newCachedRepo(repo)

	// 先形成 null marker
	if _, err := cr.GetByID(ctx, 9); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("pre-create GetByID err = %v, want ErrNotFound", err)
	}

	// 以预置 ID=9 落库 + 走装饰器 Create
	repo.byID[9] = &user{ID: 9, Name: "late"}
	if err := cr.Create(ctx, &user{ID: 9, Name: "late"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := cr.GetByID(ctx, 9)
	if err != nil {
		t.Fatalf("GetByID after preset-id create: %v", err)
	}
	if got.Name != "late" {
		t.Fatalf("name = %q, want late（null marker 必须被 Create 清除）", got.Name)
	}
}

// UpdateFields/BatchDelete/BatchCreate 同样走失效。
func TestOtherWritePathsInvalidate(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	repo.byID[2] = &user{ID: 2, Name: "b"}
	ctx := context.Background()
	cr := newCachedRepo(repo)

	if _, err := cr.GetByID(ctx, 1); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := cr.UpdateFields(ctx, 1, map[string]any{"name": "a2"}); err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}
	// 键已失效 → 下次读回源（fake 不改数据，仅验证回源发生）
	if _, err := cr.GetByID(ctx, 1); err != nil {
		t.Fatalf("GetByID after UpdateFields: %v", err)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("after UpdateFields repo calls = %d, want 2", n)
	}

	if _, err := cr.GetByID(ctx, 2); err != nil {
		t.Fatalf("warm 2: %v", err)
	}
	if err := cr.BatchDelete(ctx, []int64{2}); err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}
	if _, err := cr.GetByID(ctx, 2); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("after BatchDelete err = %v, want ErrNotFound", err)
	}
}

// 删缓存失败：写操作不回滚（返回错误会诱发重试双写），错误经 onError 上报。
func TestInvalidateFailureReportedButWriteSucceeds(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	var ops []string
	cr := cached.NewRepository[user, int64](repo,
		func(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
			return &delFailCache[user]{inner: dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[user]{
				TTL: time.Minute, Source: src,
			}))}
		},
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			ops = append(ops, op+":"+strings.Join(keys, ","))
		}),
	)

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}

	if err := cr.Update(ctx, &user{ID: 7, Name: "bob"}); err != nil {
		t.Fatalf("Update 必须成功（删缓存失败不回滚写）: %v", err)
	}
	if n := repo.count("Update"); n != 1 {
		t.Fatalf("repo.Update calls = %d, want 1", n)
	}
	if len(ops) == 0 {
		t.Fatal("onError 未上报失效失败")
	}
	if !strings.HasPrefix(ops[0], "invalidate:") {
		t.Fatalf("onError op = %q, want invalidate:...", ops[0])
	}
}

// 缺省 ID 探测：实现 gormx BaseModel 的 GetID() 接口即可，无需 WithID。
func TestDefaultIDAccessorViaGetIDInterface(t *testing.T) {
	type model struct {
		gormx.BaseModel
		Name string
	}
	repo := newFakeRepo[model](func(m *model) int64 { return m.ID }, nil)
	m := &model{Name: "m"}
	repo.byID[5] = m
	ctx := context.Background()

	cr := cached.NewRepository[model, int64](repo, func(src dcache.SourceFunc[model]) dcache.CacheAside[model] {
		return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[model]{
			TTL: time.Minute, Source: src,
		}))
	})

	if _, err := cr.GetByID(ctx, 5); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := cr.Update(ctx, &model{BaseModel: gormx.BaseModel{ID: 5}, Name: "m2"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := cr.GetByID(ctx, 5)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "m2" {
		t.Fatalf("name = %q, want m2（GetID 接口缺省探测必须生效）", got.Name)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo calls = %d, want 2", n)
	}
}

// 纯 GetID 实体（无任何名为 ID 的字段）：接口探测必须独立生效，
// 不能靠字段兜底蒙混。
type idOnlyEntity struct {
	uid  int64
	Name string
}

func (e *idOnlyEntity) GetID() int64 { return e.uid }

func TestDefaultIDAccessorViaGetIDOnlyEntity(t *testing.T) {
	repo := newFakeRepo[idOnlyEntity](func(e *idOnlyEntity) int64 { return e.uid }, nil)
	repo.byID[5] = &idOnlyEntity{uid: 5, Name: "a"}
	ctx := context.Background()

	cr := cached.NewRepository[idOnlyEntity, int64](repo, func(src dcache.SourceFunc[idOnlyEntity]) dcache.CacheAside[idOnlyEntity] {
		return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[idOnlyEntity]{
			TTL: time.Minute, Source: src,
		}))
	})

	if _, err := cr.GetByID(ctx, 5); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := cr.Update(ctx, &idOnlyEntity{uid: 5, Name: "a2"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := cr.GetByID(ctx, 5)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "a2" {
		t.Fatalf("name = %q, want a2（GetID 接口探测失败则失效不发生，缓存旧值）", got.Name)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo calls = %d, want 2", n)
	}
}

// BatchCreate 的写后失效（非事务路径）。
func TestBatchCreateInvalidates(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newCachedRepo(repo)

	// 预置主键批量创建：先形成 null marker，Create 后必须被清掉
	if _, err := cr.GetByID(ctx, 10); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("pre-create err = %v, want ErrNotFound", err)
	}
	err := cr.BatchCreate(ctx, []*user{{ID: 10, Name: "x"}, {ID: 11, Name: "y"}}, 2)
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}

	got, err := cr.GetByID(ctx, 10)
	if err != nil {
		t.Fatalf("GetByID after batchcreate: %v", err)
	}
	if got.Name != "x" {
		t.Fatalf("name = %q, want x（BatchCreate 必须清 null marker）", got.Name)
	}
	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo calls = %d, want 2", n)
	}
}

// 完全无法取 ID 的实体：写路径不炸，仅退化为版本 bump / TTL 兜底。
func TestWritePathWithoutIDAccessStillSucceeds(t *testing.T) {
	type blob struct{ Name string }
	repo := newFakeRepo[blob](func(b *blob) int64 { return 0 }, nil)
	ctx := context.Background()
	cr := cached.NewRepository[blob, int64](repo, func(src dcache.SourceFunc[blob]) dcache.CacheAside[blob] {
		return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[blob]{
			TTL: time.Minute, Source: src,
		}))
	})

	if err := cr.Create(ctx, &blob{Name: "x"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := cr.Update(ctx, &blob{Name: "y"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if n := repo.count("Update"); n != 1 {
		t.Fatalf("repo.Update calls = %d, want 1", n)
	}
}
