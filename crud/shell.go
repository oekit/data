// Package crud 是 data 门面子包：框架无关 CRUD——Service/Repository/查询构造。
//
// re-export 自 github.com/oekit/gormx/crud：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package crud

import src "github.com/oekit/gormx/crud"

type BaseService[T any, CreateReq, UpdateReq any] = src.BaseService[T, CreateReq, UpdateReq]
type Condition = src.Condition
type PageResponse[T any] = src.PageResponse[T]
type Preload = src.Preload
type Query = src.Query
type QuerySpec = src.QuerySpec
type Reader[T any] = src.Reader[T]
type Repository[T any] = src.Repository[T]
type Service[T any, CreateReq, UpdateReq any] = src.Service[T, CreateReq, UpdateReq]
type TxRepository[T any] = src.TxRepository[T]
type Writer[T any] = src.Writer[T]

var ErrNotFound = src.ErrNotFound
var NewQuery = src.NewQuery
