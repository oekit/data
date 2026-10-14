package gormx

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// TXFunc 是在事务中执行的函数类型，接收事务内的 *gorm.DB 并返回 error。
type TXFunc func(tx *gorm.DB) error

// InTransactionContext 在带有 context 的数据库事务中执行指定函数。
func InTransactionContext(ctx context.Context, db *gorm.DB, fn TXFunc) error {
	return db.WithContext(ctx).Transaction(fn)
}

// IsUniqueViolation 判断给定 error 是否为唯一键冲突。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "duplicate key value") ||
		strings.Contains(msg, "23505")
}
