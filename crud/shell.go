// Package crud 是 data 门面子包：框架无关 CRUD——Service/Repository/查询构造。
//
// re-export 自 github.com/oekit/gormx/crud：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通；泛型别名需 go 1.25+）；非泛型函数/变量经 var 引用同一函数值。
// 源包新增导出符号时本壳需同步（门面跟版税，data 门面化决策已接受）。
package crud

import src "github.com/oekit/gormx/crud"

type BaseService[T any, ID comparable, CreateReq, UpdateReq any] = src.BaseService[T, ID, CreateReq, UpdateReq]
type Condition = src.Condition

// ErrMapperFunc 是 Service 对外错误的映射钩子（gormx v0.7.0，gormx#62）——
// not-found / duplicate 等对外错误可映射进消费方码表/哨兵形态。
type ErrMapperFunc = src.ErrMapperFunc
type PageResponse[T any] = src.PageResponse[T]
type Preload = src.Preload
type Query = src.Query
type QuerySpec = src.QuerySpec
type Reader[T any, ID comparable] = src.Reader[T, ID]
type Repository[T any, ID comparable] = src.Repository[T, ID]
type Service[T any, ID comparable, CreateReq, UpdateReq any] = src.Service[T, ID, CreateReq, UpdateReq]

// ServiceOption 配置 BaseService（gormx v0.7.0，gormx#62）——
// WithNotFoundErr / WithErrMapper / WithNoOpUpdateAsNotFound 的注入位。
type ServiceOption = src.ServiceOption
type TxRepository[T any, ID comparable] = src.TxRepository[T, ID]

// UpdateOption 配置单次 Update 的写入形态（gormx v0.7.0，gormx#61）——
// crud.UpdateOption{Columns: ...} 列白名单保护 immutable 列。
type UpdateOption = src.UpdateOption
type Writer[T any, ID comparable] = src.Writer[T, ID]

var ErrNotFound = src.ErrNotFound
var NewQuery = src.NewQuery

// WithNotFoundErr 把 not-found 对外错误固定替换为 err（gormx v0.7.0，gormx#62）。
var WithNotFoundErr = src.WithNotFoundErr

// WithErrMapper 注入错误映射钩子（gormx v0.7.0，gormx#62）——
// 与 WithNotFoundErr 共用同一槽位，后设置者生效。
var WithErrMapper = src.WithErrMapper

// WithNoOpUpdateAsNotFound 把 Update 的 0 行更新按 not-found 处理
// （gormx v0.7.0，gormx#60）——区分「记录不存在」与「no-op 更新」。
var WithNoOpUpdateAsNotFound = src.WithNoOpUpdateAsNotFound
