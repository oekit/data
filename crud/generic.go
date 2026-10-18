package crud

import src "github.com/oekit/gormx/crud"

// NewBaseService 创建通用 CRUD Service。
// ID 为实体主键类型（int64 自增 / string 文本主键等）。
func NewBaseService[T any, ID comparable, CreateReq, UpdateReq any](repo Repository[T, ID]) *BaseService[T, ID, CreateReq, UpdateReq] {
	return src.NewBaseService[T, ID, CreateReq, UpdateReq](repo)
}
