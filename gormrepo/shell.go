// Package gormrepo 是 data 门面子包：github.com/oekit/gormx/crud 仓储接口
// 的 GORM 实现。
//
// re-export 自 github.com/oekit/gormx/gormrepo：类型为泛型 type alias（与
// 源包**完全同一类型**，双向赋值互通）；非泛型函数经 var 转发或薄包装。
// 源包新增导出符号时本壳需同步（门面跟版税，data 门面化决策已接受）。
//
// 本壳另带两个便捷类型别名 [Repository] / [Query]（来自 crud 契约）：
// 装配点只 import data/gormrepo 就能同时写出「实现 + 接口」签名，不必再
// 分别引入 crud/gormrepo 两个包。两个别名不改变类型身份——与
// github.com/oekit/gormx/crud.Repository / Query 完全同一类型。
//
//	repo := gormrepo.NewGormRepository[Project, int64](db)
//	svc := crud.NewBaseService[Project, int64, CreateReq, UpdateReq](repo)
//
// 多租户/行级隔离可挂查询装饰器（scope 自动注入，含按 ID 路径）：
//
//	repo := gormrepo.NewGormRepository[Project, int64](db,
//	    gormrepo.WithQueryDecorator[Project](func(ctx context.Context) func(*gorm.DB) *gorm.DB {
//	        uid := identity.UID(ctx)
//	        return func(db *gorm.DB) *gorm.DB { return db.Where("uid = ?", uid) }
//	    }),
//	    gormrepo.WithScopeRequired[Project](), // fail-closed
//	)
package gormrepo

import (
	"context"

	"gorm.io/gorm"

	"github.com/oekit/gormx/crud"
	src "github.com/oekit/gormx/gormrepo"
	"github.com/oekit/pkg/pagination"
)

// Repository 是 crud.Repository 的便捷别名（与源接口同一类型）：
// 装配点只 import 本包即可同时拿到实现与仓储契约。
type Repository[T any, ID comparable] = crud.Repository[T, ID]

// Query 是 crud.Query 的便捷别名（与源构建器同一类型）。
type Query = crud.Query

// GormRepository 是基于 GORM 的仓储实现（与源包同一类型）。
type GormRepository[T any, ID comparable] = src.GormRepository[T, ID]

// RepositoryOption 配置 GormRepository（与源包同一选项类型）。
type RepositoryOption[T any] = src.RepositoryOption[T]

// ErrNotFound 透传 crud.ErrNotFound（Service 层据此判 404）。
var ErrNotFound = src.ErrNotFound

// ApplyQuery 将 crud.Query 以完整语句形态（分页 + 排序 + 悲观锁）
// 应用到 gorm.DB。
var ApplyQuery = src.ApplyQuery

// NewGormRepository 创建 GORM 仓储实例。
// opts 省略时为纯 passthrough（单租户零负担）；多租户见 WithQueryDecorator /
// WithScopeRequired。
func NewGormRepository[T any, ID comparable](db *gorm.DB, opts ...RepositoryOption[T]) *GormRepository[T, ID] {
	return src.NewGormRepository[T, ID](db, opts...)
}

// WithQueryDecorator 挂接查询装饰器（行级 scope 自动注入）。
//
// fn 从 ctx 取当次请求身份（uid/team/tenant），返回 gorm scope；
// 返回 nil 表示本次不加 scope（如 admin 直通）。
func WithQueryDecorator[T any](fn func(ctx context.Context) func(*gorm.DB) *gorm.DB) RepositoryOption[T] {
	return src.WithQueryDecorator[T](fn)
}

// WithScopeRequired 开启 fail-closed：未配置 decorator 或 decorator 返回
// nil（裸查询）时显式报错，而不是放行。
func WithScopeRequired[T any]() RepositoryOption[T] {
	return src.WithScopeRequired[T]()
}

// PaginateList 用 crud.Query + 分页请求执行分页查询（泛型函数薄包装）。
func PaginateList[T any](db *gorm.DB, req *pagination.PageRequest, q *crud.Query) (*crud.PageResponse[T], error) {
	return src.PaginateList[T](db, req, q)
}
