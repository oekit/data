package gormx_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	pkgpagination "github.com/oekit/pkg/pagination"
	"gorm.io/gorm"

	"github.com/oekit/data/gormx"
	"github.com/oekit/data/internal/testsame"
	gormxsrc "github.com/oekit/gormx"
)

// testModel 嵌入 BaseModel 满足 SingletonRepo 的 Model 约束。
type testModel struct {
	gormx.BaseModel
}

// TestTypeIdentity 类型双向赋值（含 alias-of-alias 的 PageResponse）。
func TestTypeIdentity(t *testing.T) {
	fb := gormx.NewFilterBuilder()
	testsame.Assert[*gormxsrc.FilterBuilder](fb) // 门面 → 源

	var pr *gormx.PageResponse[int]
	testsame.Assert[*pkgpagination.PageResponse[int]](pr) // alias-of-alias 同一

	sr := gormx.NewSingletonRepo[*testModel](nil, func() *testModel { return &testModel{} })
	testsame.Assert[*gormxsrc.SingletonRepo[*testModel]](sr) // 门面 → 源
}

// TestFilterBuilderSmoke 行为冒烟。
func TestFilterBuilderSmoke(t *testing.T) {
	fb := gormx.NewFilterBuilder()
	fb.Add(true, "status = ?", "active")
	if fb.IsEmpty() {
		t.Fatal("FilterBuilder 应记录条件")
	}
}

// paginateUser 分页冒烟实体（sqlite :memory: 真表）。
type paginateUser struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"size:100"`
}

// TestPaginateSmoke 构造 + 基本调用冒烟（#276）：门面分页主入口 Paginate[T]
// 住 generic.go（与 shell.go 同包同一 API 面，审计只扫 shell.go 会漏看），
// 真 sqlite 跑一次——3 行种子按 PageSize=2 取第一页，Total/Items/Pages 齐活。
func TestPaginateSmoke(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err = db.AutoMigrate(&paginateUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err = db.Create(&paginateUser{Name: name}).Error; err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	resp, err := gormx.Paginate[paginateUser](db, pkgpagination.NewPageRequest(1, 2), nil)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if resp.Total != 3 || len(resp.Items) != 2 || resp.Page != 1 || resp.PageSize != 2 || resp.Pages != 2 {
		t.Fatalf("Paginate = {total %d items %d page %d size %d pages %d}, want {3 2 1 2 2}",
			resp.Total, len(resp.Items), resp.Page, resp.PageSize, resp.Pages)
	}
}
