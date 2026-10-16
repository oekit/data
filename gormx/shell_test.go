package gormx_test

import (
	"testing"

	pkgpagination "github.com/oekit/pkg/pagination"

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

	sr := gormx.NewSingletonRepo[*testModel](nil, nil)
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
