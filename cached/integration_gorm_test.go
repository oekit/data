package cached_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
	"github.com/oekit/gormx/gormrepo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// gormUser 是集成测试实体（真实 sqlite 表）。
type gormUser struct {
	ID    int64  `gorm:"primaryKey;autoIncrement"`
	Name  string `gorm:"size:100"`
	Email string `gorm:"size:200"`
}

func setupGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&gormUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newGormCachedRepo(t *testing.T, db *gorm.DB) *cached.CachedRepository[gormUser, int64] {
	t.Helper()
	raw := gormrepo.NewGormRepository[gormUser, int64](db)
	return cached.NewRepository[gormUser, int64](raw,
		func(src dcache.SourceFunc[gormUser]) dcache.CacheAside[gormUser] {
			return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[gormUser]{
				TTL: time.Minute, Source: src, NullTTL: time.Minute,
			}))
		},
		cached.WithID[gormUser, int64](func(u *gormUser) int64 { return u.ID }),
	)
}

// 端到端：Create（自增 ID 回填）→ 点读回填 → Update 失效 → 读到新值
// → Delete → null marker 稳定 ErrNotFound → RunInTx 事务更新提交后失效。
func TestIntegrationGormCachedRepository(t *testing.T) {
	db := setupGormDB(t)
	cr := newGormCachedRepo(t, db)
	ctx := context.Background()

	var err error
	var got *gormUser

	// Create：自增主键，装饰器失效不炸（键在 ID 回填后才删）
	created := &gormUser{Name: "alice", Email: "a@e.com"}
	if err = cr.Create(ctx, created); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected auto-increment ID populated")
	}

	// 点读回填 + 缓存命中（删除后表查不到，但缓存仍在——值未变即为证据）
	if got, err = cr.GetByID(ctx, created.ID); err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name = %q, want alice", got.Name)
	}

	// Update 失效 → 读到新值
	if err = cr.Update(ctx, &gormUser{ID: created.ID, Name: "alice2", Email: "a@e.com"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, err = cr.GetByID(ctx, created.ID); err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "alice2" {
		t.Fatalf("name after update = %q, want alice2（失效链路在真实 SQL 上断裂）", got.Name)
	}

	// Delete → ErrNotFound；null marker 让第二次同样稳定
	if err = cr.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err = cr.GetByID(ctx, created.ID); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetByID #%d after delete err = %v, want ErrNotFound", i+1, err)
		}
	}

	// 事务路径：真实 sqlite 事务 + 提交后失效
	txUser := &gormUser{Name: "bob", Email: "b@e.com"}
	if err = cr.Create(ctx, txUser); err != nil {
		t.Fatalf("Create bob: %v", err)
	}
	if _, err = cr.GetByID(ctx, txUser.ID); err != nil {
		t.Fatalf("warm bob: %v", err)
	}
	if err = cr.RunInTx(ctx, func(repo crud.Repository[gormUser, int64]) error {
		return repo.Update(ctx, &gormUser{ID: txUser.ID, Name: "bob2", Email: "b@e.com"})
	}); err != nil {
		t.Fatalf("RunInTx: %v", err)
	}
	if got, err = cr.GetByID(ctx, txUser.ID); err != nil {
		t.Fatalf("GetByID after tx: %v", err)
	}
	if got.Name != "bob2" {
		t.Fatalf("name after tx = %q, want bob2（事务提交后失效在真实 SQL 上断裂）", got.Name)
	}
}

// 事务回滚：真实 sqlite 回滚后，缓存旧值仍是权威值。
func TestIntegrationGormTxRollback(t *testing.T) {
	db := setupGormDB(t)
	cr := newGormCachedRepo(t, db)
	ctx := context.Background()

	u := &gormUser{Name: "carol", Email: "c@e.com"}
	if err := cr.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := cr.GetByID(ctx, u.ID); err != nil {
		t.Fatalf("warm: %v", err)
	}

	err := cr.RunInTx(ctx, func(repo crud.Repository[gormUser, int64]) error {
		if err := repo.Update(ctx, &gormUser{ID: u.ID, Name: "carol2"}); err != nil {
			return err
		}
		return errors.New("business rejected")
	})
	if err == nil {
		t.Fatal("RunInTx 应透传业务错误")
	}

	got, err := cr.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after rollback: %v", err)
	}
	if got.Name != "carol" {
		t.Fatalf("name after rollback = %q, want carol", got.Name)
	}
}
