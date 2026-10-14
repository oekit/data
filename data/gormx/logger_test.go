package gormx

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

func TestNewLogger(t *testing.T) {
	l := NewLogger(logger.Config{
		SlowThreshold: time.Second,
		LogLevel:      logger.Warn,
	})
	if l == nil {
		t.Fatal("expected non-nil Logger")
	}
	if l.LogMode(logger.Warn) == nil {
		t.Error("expected non-nil from LogMode")
	}
}

func TestNewDefaultLogger(t *testing.T) {
	l := NewDefaultLogger()
	if l == nil {
		t.Fatal("expected non-nil")
	}
	lm := l.LogMode(logger.Info)
	if lm == nil {
		t.Error("expected non-nil from LogMode")
	}
}

func TestLogger_LogMode_ReturnsNewLogger(t *testing.T) {
	l := NewDefaultLogger()
	modes := []logger.LogLevel{
		logger.Silent,
		logger.Error,
		logger.Warn,
		logger.Info,
	}
	for _, m := range modes {
		got := l.LogMode(m)
		if got == nil {
			t.Errorf("expected non-nil for level %v", m)
		}
	}
}

func TestLogger_InfoWarnError(t *testing.T) {
	l := NewDefaultLogger()
	ctx := context.Background()

	// 不 panic 即可（gorm logger 契约为 printf 风格）
	l.Info(ctx, "info message %s", "kv")
	l.Warn(ctx, "warn message %s", "kv")
	l.Error(ctx, "error message %s", "kv")
}

func TestLogger_Trace(t *testing.T) {
	l := NewDefaultLogger()
	ctx := context.Background()

	// 慢 query 路径: begin time 早于 SlowThreshold
	begin := time.Now().Add(-2 * time.Second)
	rows := int64(5)
	fc := func() (string, int64) {
		return "SELECT 1", rows
	}
	l.Trace(ctx, begin, fc, nil)

	// 错误路径
	l.Trace(ctx, begin, fc, context.Canceled)

	// 快 query (rowsAffected = 0)
	fc2 := func() (string, int64) {
		return "SELECT 0", 0
	}
	l.Trace(ctx, time.Now(), fc2, nil)
}
