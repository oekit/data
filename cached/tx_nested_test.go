package cached_test

import (
	"context"
	"sync"
	"testing"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// layeredRepo 模拟 gorm 事务分层：每次 RunInTx 产出**独立的**子事务仓储
// 对象（对应 SAVEPOINT 层级），数据读写共享底层 fakeRepo 的同一张 map
// （gorm 嵌套事务共享同一 *sql.Tx 的形态），写操作按层记账——提交时不并入
// 父层，借以区分「写打在哪一层」。
type layeredRepo[T any] struct {
	*fakeRepo[T]
	layers *txLayerBook
	path   string
}

// txLayerBook 各事务层的写记录（路径 → 有序写操作）。
type txLayerBook struct {
	mu     sync.Mutex
	writes map[string][]string
}

func newLayeredRepo[T any](idOf func(*T) int64) *layeredRepo[T] {
	return &layeredRepo[T]{
		fakeRepo: newFakeRepo[T](idOf, nil),
		layers:   &txLayerBook{writes: map[string][]string{}},
		path:     "root",
	}
}

func (r *layeredRepo[T]) record(op string) {
	r.layers.mu.Lock()
	r.layers.writes[r.path] = append(r.layers.writes[r.path], op)
	r.layers.mu.Unlock()
}

func (r *layeredRepo[T]) writesOf(path string) []string {
	r.layers.mu.Lock()
	defer r.layers.mu.Unlock()
	out := make([]string, len(r.layers.writes[path]))
	copy(out, r.layers.writes[path])
	return out
}

func (r *layeredRepo[T]) Create(ctx context.Context, entity *T) error {
	if err := r.fakeRepo.Create(ctx, entity); err != nil {
		return err
	}
	r.record("create")
	return nil
}

func (r *layeredRepo[T]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := r.fakeRepo.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	r.record("batchcreate")
	return nil
}

func (r *layeredRepo[T]) Update(ctx context.Context, entity *T, opts ...crud.UpdateOption) (int64, error) {
	rows, err := r.fakeRepo.Update(ctx, entity, opts...)
	if err != nil {
		return rows, err
	}
	r.record("update")
	return rows, nil
}

func (r *layeredRepo[T]) UpdateFields(ctx context.Context, id int64, fields map[string]any) (int64, error) {
	rows, err := r.fakeRepo.UpdateFields(ctx, id, fields)
	if err != nil {
		return rows, err
	}
	r.record("updatefields")
	return rows, nil
}

func (r *layeredRepo[T]) Delete(ctx context.Context, id int64) (int64, error) {
	rows, err := r.fakeRepo.Delete(ctx, id)
	if err != nil {
		return rows, err
	}
	r.record("delete")
	return rows, nil
}

func (r *layeredRepo[T]) BatchDelete(ctx context.Context, ids []int64) (int64, error) {
	rows, err := r.fakeRepo.BatchDelete(ctx, ids)
	if err != nil {
		return rows, err
	}
	r.record("batchdelete")
	return rows, nil
}

func (r *layeredRepo[T]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T, int64]) error) error {
	child := &layeredRepo[T]{fakeRepo: r.fakeRepo, layers: r.layers, path: r.path + "/inner"}
	return fn(child)
}

// TestRunInTxNestedRestoresBindingAfterCommit 回归测试（#180）：嵌套事务
// 提交返回后，收集器必须恢复指向**外层**事务仓储——外层 fn 中后续写不得
// 继续打到已提交的内层事务仓储上。
func TestRunInTxNestedRestoresBindingAfterCommit(t *testing.T) {
	root := newLayeredRepo[user](func(u *user) int64 { return u.ID })
	root.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()
	cr := cached.NewRepository[user, int64](root, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	err := cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		if _, err := tx.Update(ctx, &user{ID: 7, Name: "before"}); err != nil {
			return err
		}
		txr, ok := tx.(crud.TxRepository[user, int64])
		if !ok {
			t.Fatal("事务视图应实现 TxRepository")
		}
		if err := txr.RunInTx(ctx, func(inner crud.Repository[user, int64]) error {
			_, err := inner.Update(ctx, &user{ID: 7, Name: "during"})
			return err
		}); err != nil {
			return err
		}
		// 嵌套事务已提交——此后的写必须回到外层事务仓储（#180）
		_, err := tx.Update(ctx, &user{ID: 7, Name: "after"})
		return err
	})
	if err != nil {
		t.Fatalf("RunInTx: %v", err)
	}

	outer := root.writesOf("root/inner")
	if len(outer) != 2 {
		t.Fatalf("外层事务层写记录 = %v, want 2 笔（before + after——嵌套提交后的写必须回到外层仓储）", outer)
	}
	inner := root.writesOf("root/inner/inner")
	if len(inner) != 1 {
		t.Fatalf("内层事务层写记录 = %v, want 1 笔（during）", inner)
	}
}

// TestRunInTxNestedBindingRaceFree 回归测试（#180）：嵌套事务重绑 inner/innerTx
// 与外层事务内的并发读写不得构成数据竞争（字段写入与读取同锁保护）。
func TestRunInTxNestedBindingRaceFree(t *testing.T) {
	root := newLayeredRepo[user](func(u *user) int64 { return u.ID })
	root.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()
	cr := cached.NewRepository[user, int64](root, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	err := cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		txr, ok := tx.(crud.TxRepository[user, int64])
		if !ok {
			t.Fatal("事务视图应实现 TxRepository")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		// 一路反复进出嵌套事务（重绑/恢复 inner/innerTx）
		async.GoSafe(func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = txr.RunInTx(ctx, func(inner crud.Repository[user, int64]) error {
					_, err := inner.Update(ctx, &user{ID: 7, Name: "nested"})
					return err
				})
			}
		})
		// 另一路持续经收集器视图读写（读 inner 指向的仓储）
		async.GoSafe(func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = tx.GetByID(ctx, 7)
				_, _ = tx.Update(ctx, &user{ID: 7, Name: "outer"})
			}
		})
		wg.Wait()
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTx: %v", err)
	}
}
