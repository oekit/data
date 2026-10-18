package cached_test

import (
	"context"
	"strings"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// recordingFactory 组装会记录失效事件的装饰器（事件与仓储事件同切片，可断言时序）。
func recordingFactory(events *[]string) cached.CacheFactory[user] {
	return func(src dcache.SourceFunc[user]) dcache.CacheAside[user] {
		local := dcache.NewLocalCache(dcache.LocalConfig[user]{
			TTL: time.Minute, Source: src, NullTTL: time.Minute,
		})
		return dcache.NewSingleFlightCache(&recordingCache[user]{inner: local, events: events})
	}
}

// 事务内写只在提交后失效：cache:del 必须出现在 tx:commit 之后。
func TestRunInTxInvalidatesOnlyAfterCommit(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := cached.NewRepository[user, int64](repo, recordingFactory(&events),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}

	err := cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		return tx.Update(ctx, &user{ID: 7, Name: "bob"})
	})
	if err != nil {
		t.Fatalf("RunInTx: %v", err)
	}

	commitAt := indexOf(events, "tx:commit")
	delAt := firstIndexOfPrefix(events, "cache:del:")
	if commitAt < 0 {
		t.Fatalf("缺少 tx:commit 事件: %v", events)
	}
	if delAt < commitAt {
		t.Fatalf("cache:del 必须在 tx:commit 之后（事务内删缓存会放跑回填旧值竞态）: %v", events)
	}

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByID after tx: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("name after tx = %q, want bob", got.Name)
	}
}

// 回滚不失效：缓存保持旧值（回滚意味着旧值仍是权威值）。
func TestRunInTxRollbackKeepsCache(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := cached.NewRepository[user, int64](repo, recordingFactory(&events),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}

	err := cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		if err := tx.Update(ctx, &user{ID: 7, Name: "bob"}); err != nil {
			return err
		}
		return context.Canceled // 模拟业务失败回滚
	})
	if err == nil {
		t.Fatal("RunInTx 应透传业务错误")
	}

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByID after rollback: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name after rollback = %q, want alice（回滚不得失效缓存）", got.Name)
	}
	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo.GetByID calls = %d, want 1（缓存未失效，直接命中）", n)
	}
	if hasAnyPrefix(events, "cache:del:") {
		t.Fatalf("回滚后不得出现失效事件: %v", events)
	}
}

// 事务内读直通仓储：保证读到事务内状态，而不是缓存里的旧值。
func TestTxInnerReadsBypassCache(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := cached.NewRepository[user, int64](repo, recordingFactory(&events),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}

	_ = cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		if _, err := tx.GetByID(ctx, 7); err != nil {
			return err
		}
		return nil
	})

	if n := repo.count("GetByID"); n != 2 {
		t.Fatalf("repo.GetByID calls = %d, want 2（事务内读必须直通，即使点读缓存命中）", n)
	}
}

// 与 gormx/crud.BaseService 组合：类型断言自动走事务路径，提交后失效。
func TestBaseServiceUpdateUsesTxPathAndInvalidates(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := cached.NewRepository[user, int64](repo, recordingFactory(&events),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	type req struct{ Name string }
	svc := crud.NewBaseService[user, int64, req, req](cr)
	svc.SetUpdateEntity(func(u *user, r *req) { u.Name = r.Name })

	if _, err := svc.Get(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if _, err := svc.Update(ctx, 7, &req{Name: "bob"}); err != nil {
		t.Fatalf("svc.Update: %v", err)
	}

	if n := repo.count("GetByIDForUpdate"); n != 1 {
		t.Fatalf("GetByIDForUpdate calls = %d, want 1（必须走事务+行锁路径，而非无锁回退）", n)
	}
	commitAt := indexOf(events, "tx:commit")
	delAt := firstIndexOfPrefix(events, "cache:del:")
	if commitAt < 0 || delAt < commitAt {
		t.Fatalf("失效必须在提交后: %v", events)
	}

	got, err := svc.Get(ctx, 7)
	if err != nil {
		t.Fatalf("svc.Get after update: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("name = %q, want bob", got.Name)
	}
}

// 嵌套事务：内层 RunInTx 的写同样只收集、在外层提交后统一失效。
func TestRunInTxNestedStillCollects(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	ctx := context.Background()

	cr := cached.NewRepository[user, int64](repo, recordingFactory(&events),
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }))

	if _, err := cr.GetByID(ctx, 7); err != nil {
		t.Fatalf("warm: %v", err)
	}

	err := cr.RunInTx(ctx, func(tx crud.Repository[user, int64]) error {
		txRepo, ok := tx.(crud.TxRepository[user, int64])
		if !ok {
			t.Fatal("事务视图应实现 TxRepository")
		}
		return txRepo.RunInTx(ctx, func(inner crud.Repository[user, int64]) error {
			return inner.Update(ctx, &user{ID: 7, Name: "bob"})
		})
	})
	if err != nil {
		t.Fatalf("RunInTx nested: %v", err)
	}

	commitAt := indexOf(events, "tx:commit")
	delAt := firstIndexOfPrefix(events, "cache:del:")
	if commitAt < 0 || delAt < commitAt {
		t.Fatalf("嵌套事务的失效也必须在提交后: %v", events)
	}

	got, err := cr.GetByID(ctx, 7)
	if err != nil {
		t.Fatalf("GetByID after nested tx: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("name after nested tx = %q, want bob（嵌套事务写必须触发失效）", got.Name)
	}
}

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}

func firstIndexOfPrefix(events []string, prefix string) int {
	for i, e := range events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

func hasAnyPrefix(events []string, prefix string) bool {
	return firstIndexOfPrefix(events, prefix) >= 0
}
