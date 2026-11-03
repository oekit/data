package crud_test

import (
	"reflect"
	"testing"

	"github.com/oekit/data/crud"
	"github.com/oekit/data/internal/testsame"
	gormxcrud "github.com/oekit/gormx/crud"
)

// TestTypeIdentity BaseService 双向赋值。
func TestTypeIdentity(t *testing.T) {
	fwd := crud.NewBaseService[int, int64, int, int](nil)
	testsame.Assert[*gormxcrud.BaseService[int, int64, int, int]](fwd) // 门面 → 源

	bwd := gormxcrud.NewBaseService[int, int64, int, int](nil)
	testsame.Assert[*crud.BaseService[int, int64, int, int]](bwd) // 源 → 门面
}

// TestV070Identity gormx v0.7.0 新增符号（gormx#60/#61/#62）：type alias 双向
// 赋值同一，var 转发的函数值与源完全同一（指针相等——包一层闭包编译期照样过，
// 只有比指针才抓得到）。
func TestV070Identity(t *testing.T) {
	testsame.Assert[gormxcrud.UpdateOption](crud.UpdateOption{})
	testsame.Assert[gormxcrud.ServiceOption](crud.ServiceOption(nil))
	testsame.Assert[gormxcrud.ErrMapperFunc](crud.ErrMapperFunc(nil))

	for name, shellFn := range map[string]any{
		"WithNotFoundErr":          crud.WithNotFoundErr,
		"WithErrMapper":            crud.WithErrMapper,
		"WithNoOpUpdateAsNotFound": crud.WithNoOpUpdateAsNotFound,
	} {
		srcFn := map[string]any{
			"WithNotFoundErr":          gormxcrud.WithNotFoundErr,
			"WithErrMapper":            gormxcrud.WithErrMapper,
			"WithNoOpUpdateAsNotFound": gormxcrud.WithNoOpUpdateAsNotFound,
		}[name]
		if reflect.ValueOf(shellFn).Pointer() != reflect.ValueOf(srcFn).Pointer() {
			t.Fatalf("crud.%s 与源包不是同一函数值", name)
		}
	}
}
