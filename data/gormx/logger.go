package gormx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yi-nology/ekit-observability/observability/logx"
	gormLogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

// Logger 是基于 logx 的 GORM 日志适配器，实现 gormLogger.Interface 接口。
// 通过 logx 门面输出，支持 slog 结构化日志和 trace_id 传播。
type Logger struct {
	log logx.Logger
	gormLogger.Config
}

// NewLogger 根据给定的配置创建 Logger 实例。
// 内部持有 Delegating logger 懒解析默认后端：构造（gormx.Open）先于业务
// logx.SetDefault 时，SQL 日志仍能跟随运行期切换的后端，不会固化到构造
// 时刻的 Default。
func NewLogger(config gormLogger.Config) *Logger {
	return &Logger{
		log:    logx.Delegating("gormx"),
		Config: config,
	}
}

// NewDefaultLogger 创建带有合理默认配置（慢查询阈值 200ms、Info 级别）的 Logger。
func NewDefaultLogger() *Logger {
	config := gormLogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormLogger.Info,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	}
	return NewLogger(config)
}

// LogMode 返回一个设置了新日志级别的 Logger 副本。
func (l *Logger) LogMode(level gormLogger.LogLevel) gormLogger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info 以 Info 级别记录日志。
func (l *Logger) Info(ctx context.Context, msg string, data ...any) {
	l.log.CtxInfof(ctx, msg, data...)
}

// Warn 以 Warn 级别记录日志。
func (l *Logger) Warn(ctx context.Context, msg string, data ...any) {
	l.log.CtxWarnf(ctx, msg, data...)
}

// Error 以 Error 级别记录日志。
func (l *Logger) Error(ctx context.Context, msg string, data ...any) {
	l.log.CtxErrorf(ctx, msg, data...)
}

// Trace 根据耗时和错误情况，以相应级别记录 SQL 追踪日志。
// 通过 logx 结构化日志输出，包含调用位置、耗时、行数、SQL 等字段。
func (l *Logger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= gormLogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	caller := utils.FileWithLineNum()
	elapsedMs := float64(elapsed.Nanoseconds()) / 1e6

	switch {
	case err != nil && l.LogLevel >= gormLogger.Error && (!errors.Is(err, gormLogger.ErrRecordNotFound) || !l.IgnoreRecordNotFoundError):
		sql, rows := fc()
		rowsStr := fmt.Sprintf("%d", rows)
		if rows == -1 {
			rowsStr = "-"
		}
		l.log.CtxError(ctx, "SQL error",
			"caller", caller,
			"elapsed_ms", elapsedMs,
			"rows", rowsStr,
			"sql", sql,
			"err", err,
		)
	case elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= gormLogger.Warn:
		sql, rows := fc()
		rowsStr := fmt.Sprintf("%d", rows)
		if rows == -1 {
			rowsStr = "-"
		}
		l.log.CtxWarn(ctx, "slow SQL",
			"caller", caller,
			"threshold", l.SlowThreshold.String(),
			"elapsed_ms", elapsedMs,
			"rows", rowsStr,
			"sql", sql,
		)
	case l.LogLevel == gormLogger.Info:
		sql, rows := fc()
		rowsStr := fmt.Sprintf("%d", rows)
		if rows == -1 {
			rowsStr = "-"
		}
		l.log.CtxInfo(ctx, "SQL",
			"caller", caller,
			"elapsed_ms", elapsedMs,
			"rows", rowsStr,
			"sql", sql,
		)
	}
}
