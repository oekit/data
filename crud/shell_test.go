package crud_test

import (
	"testing"

	"github.com/oekit/data/crud"
	"github.com/oekit/data/internal/testsame"
	gormxcrud "github.com/oekit/gormx/crud"
)

// TestTypeIdentity BaseService 双向赋值。
func TestTypeIdentity(t *testing.T) {
	fwd := crud.NewBaseService[int, int, int](nil)
	testsame.Assert[*gormxcrud.BaseService[int, int, int]](fwd) // 门面 → 源

	bwd := gormxcrud.NewBaseService[int, int, int](nil)
	testsame.Assert[*crud.BaseService[int, int, int]](bwd) // 源 → 门面
}
