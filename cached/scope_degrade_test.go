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

// 本文件是写路径/查询读路径「作用域解析失败」尾串的行为锚点（#179 基线）：
// 尾串将在重构中收口为 afterWrite/afterRead，下列断言锁定现状行为——
//   - 写已提交 ⇒ 作用域解析失败只上报（op="scope"）并跳过失效，**不**改写返回值；
//   - 读路径作用域解析失败 ⇒ 上报并降级直查仓储，**不**回落共享缓存键。

// scopeErrFactory 组装记录失效事件的缓存后端工厂（事件外置供断言）。
func recordingFactoryFor(rec *recordingCache[user]) cached.CacheFactory[user] {
	return func(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
		return rec
	}
}

func containsScopeOp(ops []string) bool {
	for _, op := range ops {
		if op == "scope" {
			return true
		}
	}
	return false
}

// TestWriteScopeFailure_SkipsInvalidationButWriteSucceeds 锚定六条写路径：
// 作用域解析失败时——写不报错、op="scope" 上报、**零失效事件**（实体键不被删、
// 查询版本不被 bump）、Update/Delete 族回传底层仓储的行数。
func TestWriteScopeFailure_SkipsInvalidationButWriteSucceeds(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		write func(cr *cached.CachedRepository[user, int64]) (int64, error)
	}{
		{
			name: "Create",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return 0, cr.Create(ctx, &user{ID: 7, Name: "n"})
			},
		},
		{
			name: "BatchCreate",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return 0, cr.BatchCreate(ctx, []*user{{ID: 7, Name: "n"}}, 1)
			},
		},
		{
			name: "Update",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return cr.Update(ctx, &user{ID: 7, Name: "n"})
			},
		},
		{
			name: "UpdateFields",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return cr.UpdateFields(ctx, 7, map[string]any{"name": "n"})
			},
		},
		{
			name: "Delete",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return cr.Delete(ctx, 7)
			},
		},
		{
			name: "BatchDelete",
			write: func(cr *cached.CachedRepository[user, int64]) (int64, error) {
				return cr.BatchDelete(ctx, []int64{7})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
			repo.byID[7] = &user{ID: 7, Name: "alice"} // Update/Delete 族行数 = 1
			var events []string
			backend := &recordingCache[user]{
				inner:  dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute}),
				events: &events,
			}
			var ops []string
			cr := cached.NewRepository[user, int64](repo, recordingFactoryFor(backend),
				cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
				cached.WithScopeKey[user, int64](func(ctx context.Context) (string, error) {
					return "", errors.New("租户上下文缺失")
				}),
				cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
					ops = append(ops, op)
				}),
			)

			rows, err := tc.write(cr)
			if err != nil {
				t.Fatalf("写路径应成功（写已提交，作用域失败只跳过失效）: %v", err)
			}
			if !containsScopeOp(ops) {
				t.Fatalf("作用域解析失败未上报，ops = %v", ops)
			}
			if len(events) != 0 {
				t.Fatalf("作用域解析失败时不得有任何失效事件: %v", events)
			}
			if tc.name == "Update" || tc.name == "UpdateFields" || tc.name == "Delete" || tc.name == "BatchDelete" {
				if rows != 1 {
					t.Fatalf("rows = %d, want 1（底层仓储行数原样透传）", rows)
				}
			}
		})
	}
}

// TestWriteScopeSuccess_Invalidates 对照锚点：作用域解析成功时写路径照常
// 失效（删实体键），证明上一条测试的「零失效」确由作用域失败引起。
func TestWriteScopeSuccess_Invalidates(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	var events []string
	backend := &recordingCache[user]{
		inner:  dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute}),
		events: &events,
	}
	cr := cached.NewRepository[user, int64](repo, recordingFactoryFor(backend),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	if _, err := cr.Update(ctx, &user{ID: 7, Name: "bob"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if firstIndexOfPrefix(events, "cache:del:") < 0 {
		t.Fatalf("作用域正常时写路径必须失效实体键: %v", events)
	}
}

// TestQueryReadScopeFailure_DegradesToRepo 锚定查询缓存读路径（GetOne/List）：
// 作用域解析失败时上报 op="scope" 并降级直查仓储，不报错、不回落共享键。
func TestQueryReadScopeFailure_DegradesToRepo(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}

	var ops []string
	qc, err := cached.NewQueryCache[user](
		dcache.NewLocalCache(dcache.LocalConfig[user]{TTL: time.Minute}),
		dcache.NewLocalCache(dcache.LocalConfig[cached.QueryPage[user]]{TTL: time.Minute}),
		dcache.NewLocalCache(dcache.LocalConfig[string]{TTL: time.Hour}),
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("QueryCache 装配: %v", err)
	}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithQueryCache[user, int64](qc),
		cached.WithScopeKey[user, int64](func(ctx context.Context) (string, error) {
			return "", errors.New("租户上下文缺失")
		}),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			ops = append(ops, op)
		}),
	)

	got, err := cr.GetOne(ctx, crud.NewQuery())
	if err != nil {
		t.Fatalf("GetOne 应降级直查仓储: %v", err)
	}
	if got.Name != "a" {
		t.Fatalf("GetOne = %q, want a", got.Name)
	}
	items, total, err := cr.List(ctx, crud.NewQuery())
	if err != nil {
		t.Fatalf("List 应降级直查仓储: %v", err)
	}
	if len(items) != 1 || total != 1 || items[0].Name != "a" {
		t.Fatalf("List = %v/%d, want [a]/1", items, total)
	}
	if !containsScopeOp(ops) {
		t.Fatalf("作用域解析失败未上报，ops = %v", ops)
	}
}
