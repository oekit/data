// Package gormx 是 data 门面子包：SQL/GORM 增强——Open/连接池/审计与 OTel 插件/日志/分页/单例仓储/事务。
//
// re-export 自 github.com/oekit/gormx：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package gormx

import (
	"context"

	"gorm.io/gorm"

	src "github.com/oekit/gormx"
)

type AuditModel = src.AuditModel
type AuditPlugin = src.AuditPlugin
type BaseModel = src.BaseModel
type FilterBuilder = src.FilterBuilder
type Logger = src.Logger
type IntSoftDelete = src.IntSoftDelete
type Model = src.Model
type MySQLConfig = src.MySQLConfig
type OpenOption = src.OpenOption
type PageResponse[T any] = src.PageResponse[T]
type SingletonRepo[T Model] = src.SingletonRepo[T]
type SoftDeleteModel = src.SoftDeleteModel
type TXFunc = src.TXFunc

var ErrAdvisoryLockUnsupported = src.ErrAdvisoryLockUnsupported
var InTransactionContext = src.InTransactionContext
var IsBusyErr = src.IsBusyErr
var IsNotFound = src.IsNotFound
var IsUniqueViolation = src.IsUniqueViolation
var MapQueryError = src.MapQueryError
var Open = src.Open
var OpenMySQL = src.OpenMySQL
var RunWithAdvisoryLock = src.RunWithAdvisoryLock
var PaginateScope = src.PaginateScope
var NewAuditPlugin = src.NewAuditPlugin
var NewFilterBuilder = src.NewFilterBuilder
var NewDefaultLogger = src.NewDefaultLogger
var NewLogger = src.NewLogger
var WithConnMaxIdleTime = src.WithConnMaxIdleTime
var WithConnectionPool = src.WithConnectionPool
var WithGormOptions = src.WithGormOptions

// ---- 部分更新/子查询/Page/日志脱敏（gormx v0.11.3 #80-#106）----

type Page = src.Page

type LoggerOption = src.LoggerOption

var NormalizePage = src.NormalizePage

// SavePatch 泛型函数薄包装（同 NewSingleValue 的说明）。
func SavePatch[M any](ctx context.Context, db *gorm.DB, pk any, patch any) error {
	return src.SavePatch[M](ctx, db, pk, patch)
}

var SubqueryIn = src.SubqueryIn

var DeleteBySubquery = src.DeleteBySubquery

var WithLogger = src.WithLogger

var WithRedactColumns = src.WithRedactColumns

// ---- 模糊查询参数原语（gormx v0.11.5 #114）----

// EscapeLike 转义 LIKE 通配符（先 \ 后 % 再 _，顺序即优先级）；SQLite 须显式
// ESCAPE 子句（见上游 gormx.EscapeLike godoc）。
func EscapeLike(s string) string { return src.EscapeLike(s) }

// LikeContains 转义后包裹为 %s%（包含匹配）。
func LikeContains(s string) string { return src.LikeContains(s) }

// LikePrefix 转义后包裹为 s%（前缀匹配）。
func LikePrefix(s string) string { return src.LikePrefix(s) }

// LikeSuffix 转义后包裹为 %s（后缀匹配）。
func LikeSuffix(s string) string { return src.LikeSuffix(s) }
