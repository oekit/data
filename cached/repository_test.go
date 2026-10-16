package cached_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// user 是测试实体（无 BaseModel，ID 访问器走显式 WithID 注入）。
type user struct {
	ID   int64
	Name string
	Age  int
}

// fakeRepo 是内存仓储：按方法计数（断言缓存命中必须以"仓储不再被调"为准），
// 读写走同一张 map。TxRepository 直接同步执行 fn（单 goroutine 假事务）。
type fakeRepo[T any] struct {
	mu     sync.Mutex
	byID   map[int64]*T
	nextID int64
	idOf   func(*T) int64
	calls  map[string]int

	// events 记录仓储侧事件（"repo:op"、"tx:commit" 等），供失效时序断言。
	events *[]string

	// failTx 非 nil 时 RunInTx 返回该错误（模拟回滚）。
	failTx error

	// failIDs 按 ID 注入硬错误（模拟仓储对特定行的故障）。
	failIDs map[int64]error
}

func newFakeRepo[T any](idOf func(*T) int64, events *[]string) *fakeRepo[T] {
	return &fakeRepo[T]{
		byID:   map[int64]*T{},
		nextID: 1,
		idOf:   idOf,
		calls:  map[string]int{},
		events: events,
	}
}

func (f *fakeRepo[T]) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *fakeRepo[T]) tick(op string) {
	f.mu.Lock()
	f.calls[op]++
	f.mu.Unlock()
}

func (f *fakeRepo[T]) log(ev string) {
	if f.events == nil {
		return
	}
	f.mu.Lock()
	*f.events = append(*f.events, ev)
	f.mu.Unlock()
}

func (f *fakeRepo[T]) GetByID(ctx context.Context, id int64) (*T, error) {
	f.tick("GetByID")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failIDs != nil {
		if err, ok := f.failIDs[id]; ok {
			return nil, err
		}
	}
	if e, ok := f.byID[id]; ok {
		return e, nil
	}
	return nil, crud.ErrNotFound
}

func (f *fakeRepo[T]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	f.tick("GetOne")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.byID {
		return e, nil // 假实现：忽略条件，有数据就返回第一条
	}
	return nil, crud.ErrNotFound
}

func (f *fakeRepo[T]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	f.tick("GetByCondition")
	return f.GetOne(ctx, crud.NewQuery())
}

func (f *fakeRepo[T]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	f.tick("List")
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]T, 0, len(f.byID))
	for _, e := range f.byID {
		out = append(out, *e)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo[T]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	f.tick("Exists")
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID) > 0, nil
}

func (f *fakeRepo[T]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	f.tick("Count")
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.byID)), nil
}

func (f *fakeRepo[T]) Create(ctx context.Context, entity *T) error {
	f.tick("Create")
	f.log("repo:create")
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.idOf(entity)
	if id == 0 {
		id = f.nextID
		f.nextID++
		setID(entity, f.idOf, id)
	}
	f.byID[id] = entity
	return nil
}

func (f *fakeRepo[T]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	f.tick("BatchCreate")
	f.log("repo:batchcreate")
	for _, e := range entities {
		if err := f.Create(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeRepo[T]) Update(ctx context.Context, entity *T) error {
	f.tick("Update")
	f.log("repo:update")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[f.idOf(entity)] = entity
	return nil
}

func (f *fakeRepo[T]) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	f.tick("UpdateFields")
	f.log("repo:updatefields")
	return nil
}

func (f *fakeRepo[T]) Delete(ctx context.Context, id int64) error {
	f.tick("Delete")
	f.log("repo:delete")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byID, id)
	return nil
}

func (f *fakeRepo[T]) BatchDelete(ctx context.Context, ids []int64) error {
	f.tick("BatchDelete")
	f.log("repo:batchdelete")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		delete(f.byID, id)
	}
	return nil
}

func (f *fakeRepo[T]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T]) error) error {
	f.tick("RunInTx")
	f.log("tx:begin")
	if err := fn(f); err != nil {
		f.log("tx:rollback")
		return err
	}
	if f.failTx != nil {
		f.log("tx:rollback")
		return f.failTx
	}
	f.log("tx:commit")
	return nil
}

func (f *fakeRepo[T]) GetByIDForUpdate(ctx context.Context, id int64) (*T, error) {
	f.tick("GetByIDForUpdate")
	return f.GetByID(ctx, id)
}

// setID 通过反射回填自增 ID（fake 通用写法，测试实体有可导出 ID 字段）。
func setID[T any](entity *T, _ func(*T) int64, id int64) {
	v := reflect.ValueOf(entity).Elem()
	if f := v.FieldByName("ID"); f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 {
		f.SetInt(id)
	}
}

// userCacheFactory 组一个「SingleFlight×Local」后端，Source 由装饰器注入。
func userCacheFactory(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
	return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[user]{
		TTL:     time.Minute,
		Source:  src,
		NullTTL: time.Minute,
	}))
}

func newCachedRepo(repo *fakeRepo[user], opts ...cached.CachedOption[user]) *cached.CachedRepository[user] {
	return cached.NewRepository[user](repo, userCacheFactory, opts...)
}

// --- RED 批次 1：点读缓存 ---

// GetByID 首次回源并回填；第二次命中缓存，仓储只被调一次。
func TestGetByIDBackfillsCache(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := newCachedRepo(repo)

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("first GetByID: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("first GetByID name = %q, want alice", got.Name)
	}

	got2, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("second GetByID: %v", err)
	}
	if got2.Name != "alice" {
		t.Fatalf("second GetByID name = %q, want alice", got2.Name)
	}

	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo.GetByID calls = %d, want 1（第二次必须命中缓存）", n)
	}
}

// 不存在的 ID：ErrNotFound 穿透为 crud.ErrNotFound，且 null marker 防穿透
// ——第二次仍返回 ErrNotFound 但仓储不再被调。
func TestGetByIDNotFoundUsesNullMarker(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	ctx := context.Background()
	cr := newCachedRepo(repo)

	_, err := cr.GetByID(ctx, 42)
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("first GetByID err = %v, want crud.ErrNotFound", err)
	}

	_, err = cr.GetByID(ctx, 42)
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("second GetByID err = %v, want crud.ErrNotFound", err)
	}

	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo.GetByID calls = %d, want 1（null marker 防穿透）", n)
	}
}

// 缓存后端读失败（如 Redis 抖动）：读降级直查仓储，不把缓存错误当数据错误。
func TestCacheReadErrorFallsBackToRepo(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	hookErrs := 0
	cr := cached.NewRepository[user](repo,
		func(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
			return &failingCache[user]{err: errors.New("redis down")}
		},
		cached.WithID[user](func(u *user) int64 { return u.ID }),
		cached.WithOnError[user](func(ctx context.Context, op string, keys []string, err error) {
			hookErrs++
			if op != "read" {
				t.Errorf("hook op = %q, want read", op)
			}
		}),
	)

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByID（缓存故障应降级直查）: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name = %q, want alice", got.Name)
	}
	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo.GetByID calls = %d, want 1", n)
	}
	if hookErrs == 0 {
		t.Fatal("OnError 未上报读降级")
	}
}

// failingCache 是全故障后端：所有操作返回 err（模拟 Redis 不可用）。
type failingCache[T any] struct{ err error }

func (f *failingCache[T]) Get(ctx context.Context, key string) (*T, error) {
	return nil, f.err
}
func (f *failingCache[T]) Set(ctx context.Context, key string, value *T) error {
	return f.err
}
func (f *failingCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	return f.err
}
func (f *failingCache[T]) Del(ctx context.Context, key string) error          { return f.err }
func (f *failingCache[T]) DelBatch(ctx context.Context, keys ...string) error { return f.err }
func (f *failingCache[T]) Exists(ctx context.Context, key string) (bool, error) {
	return false, f.err
}
func (f *failingCache[T]) GetOrLoad(ctx context.Context, key string) (*T, error) {
	return nil, f.err
}
func (f *failingCache[T]) Refresh(ctx context.Context, key string) (*T, error) {
	return nil, f.err
}
