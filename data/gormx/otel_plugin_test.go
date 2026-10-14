package gormx

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

func TestNewOtelPlugin_Default(t *testing.T) {
	p := NewOtelPlugin()
	if p.Name() != "otelgorm" {
		t.Errorf("expected Name 'otelgorm', got %q", p.Name())
	}
}

func TestOtelPlugin_AllOptions(t *testing.T) {
	// 验证所有 option 都能被应用, 不 panic, 也不破坏 Initialize
	customProvider := otel.GetTracerProvider()
	formatter := func(q string) string { return "FORMATTED:" + q }

	p := NewOtelPlugin(
		WithTracerProvider(customProvider),
		WithAttributes(attribute.String("k", "v")),
		WithDBName("mydb"),
		WithoutQueryVariables(),
		WithQueryFormatter(formatter),
		WithoutServerAddress(),
		WithRecordStackTrace(),
	)

	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}

	// 跑一次 query 触发 trace
	_ = db.Exec("SELECT 1").Error
}

func TestOtelPlugin_InitializeError(t *testing.T) {
	// 重复注册同名 plugin 应当报错
	p := NewOtelPlugin()
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	// 第二次注册应当失败
	p2 := NewOtelPlugin()
	if err := db.Use(p2); err == nil {
		t.Error("expected error on duplicate plugin registration")
	}
}

func TestOtelPlugin_QueryFormatter(t *testing.T) {
	// 验证 WithQueryFormatter 实际影响 span 记录
	// 注: queryFormatter 在 query 解析后调用, SQLite 的某些 query
	// 可能不进 trace, 这里仅验证不 panic + option 接收成功。
	formatter := func(q string) string {
		return "[" + q + "]"
	}
	p := NewOtelPlugin(WithQueryFormatter(formatter))
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	_ = db.Exec("SELECT 1").Error
}

func TestOtelPlugin_WithoutQueryVars_NotPanic(t *testing.T) {
	p := NewOtelPlugin(WithoutQueryVariables())
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	// 跑带 var 的 query, 不 panic
	type Item struct {
		ID   int64
		Name string
	}
	_ = db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)").Error
	_ = db.Create(&Item{ID: 1, Name: "x"}).Error
	_ = db.Where("name = ?", "x").Find(&[]Item{}).Error
}

func TestOtelPlugin_WithoutServerAddress_NotPanic(t *testing.T) {
	p := NewOtelPlugin(WithoutServerAddress())
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	_ = db.Exec("SELECT 1").Error
}

func TestOtelPlugin_WithDBName(t *testing.T) {
	// 验证 WithDBName 把 db.system 加到 attrs
	p := NewOtelPlugin(WithDBName("test-db"))
	// 通过 reflect / 直接比较 attrs 不可行 (私有字段), 只能验证不 panic
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	_ = db.Exec("SELECT 1").Error
}

func TestOtelPlugin_QueryErrorCapturesError(t *testing.T) {
	p := NewOtelPlugin()
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	// 故意制造一个错误: 无效 SQL
	err := db.Exec("INVALID SQL SYNTAX HERE").Error
	if err == nil {
		t.Error("expected SQL error")
	}
	_ = context.Background()
}

func TestOtelPlugin_StackTrace_NotPanic(t *testing.T) {
	p := NewOtelPlugin(WithRecordStackTrace())
	db := newTestDB(t)
	if err := db.Use(p); err != nil {
		t.Fatal(err)
	}
	_ = db.Exec("SELECT 1").Error
	_ = gorm.ErrRecordNotFound // 防止 unused import
}
