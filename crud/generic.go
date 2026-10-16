package crud

import src "github.com/oekit/gormx/crud"

// NewBaseService 创建通用 CRUD Service。
func NewBaseService[T any, CreateReq, UpdateReq any](repo Repository[T]) *BaseService[T, CreateReq, UpdateReq] {
	return src.NewBaseService[T, CreateReq, UpdateReq](repo)
}
