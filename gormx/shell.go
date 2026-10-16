// Package gormx 是 data 门面子包：SQL/GORM 增强——Open/连接池/审计与 OTel 插件/日志/分页/单例仓储/事务。
//
// re-export 自 github.com/oekit/gormx：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package gormx

import src "github.com/oekit/gormx"

type AuditModel = src.AuditModel
type AuditPlugin = src.AuditPlugin
type BaseModel = src.BaseModel
type FilterBuilder = src.FilterBuilder
type Logger = src.Logger
type Model = src.Model
type MySQLConfig = src.MySQLConfig
type OpenOption = src.OpenOption
type PageResponse[T any] = src.PageResponse[T]
type SingletonRepo[T Model] = src.SingletonRepo[T]
type SoftDeleteModel = src.SoftDeleteModel
type TXFunc = src.TXFunc

var InTransactionContext = src.InTransactionContext
var IsUniqueViolation = src.IsUniqueViolation
var Open = src.Open
var OpenMySQL = src.OpenMySQL
var PaginateScope = src.PaginateScope
var NewAuditPlugin = src.NewAuditPlugin
var NewFilterBuilder = src.NewFilterBuilder
var NewDefaultLogger = src.NewDefaultLogger
var NewLogger = src.NewLogger
var WithConnMaxIdleTime = src.WithConnMaxIdleTime
var WithConnectionPool = src.WithConnectionPool
var WithGormOptions = src.WithGormOptions
