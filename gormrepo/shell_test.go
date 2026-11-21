package gormrepo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	pkgpagination "github.com/oekit/pkg/pagination"
	"gorm.io/gorm"

	"github.com/oekit/data/gormrepo"
	"github.com/oekit/data/internal/testsame"
	srccrud "github.com/oekit/gormx/crud"
	src "github.com/oekit/gormx/gormrepo"
)

// smokeUser 是冒烟实体（sqlite 真表）。
type smokeUser struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"size:100"`
}

func setupSmokeRepo(t *testing.T) (*gorm.DB, *gormrepo.GormRepository[smokeUser, int64]) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err = db.AutoMigrate(&smokeUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, gormrepo.NewGormRepository[smokeUser, int64](db)
}

// TestTypeIdentity 壳 alias 与源包同一类型；Repository/Query 便捷别名与
// crud 契约同一类型（双向赋值编译期成立）。
func TestTypeIdentity(t *testing.T) {
	_, repo := setupSmokeRepo(t)
	testsame.Assert[*src.GormRepository[smokeUser, int64]](repo)

	// Repository 便捷别名可承接实现，且与 crud 仓储契约同一类型。
	var r gormrepo.Repository[smokeUser, int64] = repo
	testsame.Assert[srccrud.Repository[smokeUser, int64]](r)

	q := &srccrud.Query{} // crud 构建器指针可直接赋给门面 Query 别名
	testsame.Assert[*gormrepo.Query](q)
	qv := gormrepo.Query{}
	testsame.Assert[srccrud.Query](qv) // 值形态同型
}

// TestRepositorySmoke 构造 + 基本调用冒烟（Create/GetByID/GetOne/Count/
// Delete/PaginateList）——门面装配 CRUD 栈不再需要回源仓 import。
func TestRepositorySmoke(t *testing.T) {
	ctx := context.Background()
	_, repo := setupSmokeRepo(t)

	for _, name := range []string{"a", "b", "c"} {
		if err := repo.Create(ctx, &smokeUser{Name: name}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	got, err := repo.GetByID(ctx, 1)
	if err != nil || got == nil || got.Name != "a" {
		t.Fatalf("GetByID(1) = %v, %v; want a", got, err)
	}

	one, err := repo.GetOne(ctx, (&gormrepo.Query{}).Where("name", "=", "b"))
	if err != nil || one == nil || one.Name != "b" {
		t.Fatalf("GetOne(name=b) = %v, %v", one, err)
	}

	total, err := repo.Count(ctx, &gormrepo.Query{})
	if err != nil || total != 3 {
		t.Fatalf("Count = %d, %v; want 3", total, err)
	}

	resp, err := gormrepo.PaginateList[smokeUser](repo.DB(), pkgpagination.NewPageRequest(1, 2), &gormrepo.Query{})
	if err != nil {
		t.Fatalf("PaginateList: %v", err)
	}
	if resp.Total != 3 || len(resp.Items) != 2 || resp.Pages != 2 {
		t.Fatalf("PaginateList = {total %d items %d pages %d}, want {3 2 2}",
			resp.Total, len(resp.Items), resp.Pages)
	}

	if _, err = repo.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err = repo.GetByID(ctx, 1); !errors.Is(err, gormrepo.ErrNotFound) {
		t.Fatalf("删除后 GetByID 应返回门面 ErrNotFound, got %v", err)
	}
}

// TestOptionsSmoke 选项转发冒烟：查询装饰器生效、WithScopeRequired 无
// decorator 时 fail-closed（不再放行裸查询）。
func TestOptionsSmoke(t *testing.T) {
	ctx := context.Background()
	db, _ := setupSmokeRepo(t)
	for _, name := range []string{"alice", "bob"} {
		if err := db.Create(&smokeUser{Name: name}).Error; err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	scoped := gormrepo.NewGormRepository[smokeUser, int64](db,
		gormrepo.WithQueryDecorator[smokeUser](func(context.Context) func(*gorm.DB) *gorm.DB {
			return func(tx *gorm.DB) *gorm.DB { return tx.Where("name = ?", "bob") }
		}),
	)
	if _, err := scoped.GetByID(ctx, 1); !errors.Is(err, gormrepo.ErrNotFound) {
		t.Fatalf("装饰器应把 alice 挡在 scope 外（ErrNotFound）, got %v", err)
	}
	if got, err := scoped.GetByID(ctx, 2); err != nil || got == nil || got.Name != "bob" {
		t.Fatalf("scope 内 GetByID(2) = %v, %v; want bob", got, err)
	}

	strict := gormrepo.NewGormRepository[smokeUser, int64](db, gormrepo.WithScopeRequired[smokeUser]())
	if _, err := strict.GetByID(ctx, 1); err == nil {
		t.Fatal("WithScopeRequired 无 decorator 时应 fail-closed 报错")
	}
}
