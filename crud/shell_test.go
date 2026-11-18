package crud_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/oekit/data/crud"
	"github.com/oekit/data/internal/testsame"
	gormxcrud "github.com/oekit/gormx/crud"
	"github.com/oekit/gormx/gormrepo"
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

// shellUser 冒烟实体（sqlite :memory: 真表，与 cached 集成测试同款）。
type shellUser struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"size:100"`
}

type createUserReq struct{ Name string }
type updateUserReq struct{ Name string }

// TestNewBaseServiceSmoke 构造 + 基本调用冒烟（#276）：门面泛型构造器
// NewBaseService[T, ID, C, U] 住 generic.go（与 shell.go 同包同一 API 面，
// 审计只扫 shell.go 会漏看），挂 gormrepo 仓储真跑 SetToEntity → Create
// （自增 ID 回填）→ Get 往返，锁死「拿得到类型也建得出实例」。
func TestNewBaseServiceSmoke(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err = db.AutoMigrate(&shellUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := gormrepo.NewGormRepository[shellUser, int64](db)
	svc := crud.NewBaseService[shellUser, int64, createUserReq, updateUserReq](repo)
	svc.SetToEntity(func(req *createUserReq) *shellUser { return &shellUser{Name: req.Name} })

	ctx := context.Background()
	created, err := svc.Create(ctx, &createUserReq{Name: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected auto-increment ID populated")
	}
	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("Get = %+v, want name alice", got)
	}
}
