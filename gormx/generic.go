package gormx

import (
	"gorm.io/gorm"

	"github.com/oekit/pkg/pagination"

	src "github.com/oekit/gormx"
)

// Paginate 分页查询（泛型函数无法 var re-export，经包装转发）。
func Paginate[T any](db *gorm.DB, req *pagination.PageRequest, queryFn func(db *gorm.DB) *gorm.DB) (*PageResponse[T], error) {
	return src.Paginate[T](db, req, queryFn)
}

// NewSingletonRepo 创建单例仓储（事务 + FOR UPDATE 防丢更新）。
func NewSingletonRepo[T Model](db *gorm.DB, defaultVal func() T) *SingletonRepo[T] {
	return src.NewSingletonRepo(db, defaultVal)
}
