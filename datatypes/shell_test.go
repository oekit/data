package datatypes_test

import (
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/oekit/data/datatypes"
	"github.com/oekit/data/internal/testsame"
	src "github.com/oekit/gormx/datatypes"
)

// smokeDoc 是 JSON 列冒烟实体（sqlite 真表）。
type smokeDoc struct {
	ID     int64                          `gorm:"primaryKey;autoIncrement"`
	Tags   datatypes.JSONSlice[string]    `gorm:"column:tags"`
	Labels datatypes.JSONMap[string, int] `gorm:"column:labels"`
}

// TestTypeIdentity 泛型 alias 与源包同一类型（底层切片/map 双向赋值）。
func TestTypeIdentity(t *testing.T) {
	tags := datatypes.JSONSlice[string]{"a"}
	testsame.Assert[src.JSONSlice[string]](tags)                             // 门面 → 源
	testsame.Assert[datatypes.JSONSlice[string]](src.JSONSlice[string]{"b"}) // 源 → 门面
	if !reflect.DeepEqual([]string(tags), []string{"a"}) {
		t.Fatalf("JSONSlice 同一性丢失: %v", tags)
	}

	labels := datatypes.JSONMap[string, int]{"x": 1}
	testsame.Assert[src.JSONMap[string, int]](labels) // 门面 → 源
	testsame.Assert[datatypes.JSONMap[string, int]](src.JSONMap[string, int]{"y": 2})
}

// TestJSONColumnSmoke 构造 + 基本调用冒烟：AutoMigrate 建 JSON 列，
// Create 写入、First 读回；NULL 列读回 nil 零值（与空集合可区分）。
func TestJSONColumnSmoke(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err = db.AutoMigrate(&smokeDoc{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	doc := &smokeDoc{
		Tags:   []string{"go", "json"},
		Labels: map[string]int{"priority": 2},
	}
	if err = db.Create(doc).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got smokeDoc
	if err = db.First(&got, doc.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if !reflect.DeepEqual([]string(got.Tags), []string{"go", "json"}) {
		t.Fatalf("Tags roundtrip = %v", got.Tags)
	}
	if !reflect.DeepEqual(map[string]int(got.Labels), map[string]int{"priority": 2}) {
		t.Fatalf("Labels roundtrip = %v", got.Labels)
	}

	empty := &smokeDoc{}
	if err = db.Create(empty).Error; err != nil {
		t.Fatalf("Create NULL row: %v", err)
	}
	var nullGot smokeDoc
	if err = db.First(&nullGot, empty.ID).Error; err != nil {
		t.Fatalf("First NULL row: %v", err)
	}
	if nullGot.Tags != nil || nullGot.Labels != nil {
		t.Fatalf("NULL 列应读回 nil 零值, got tags=%v labels=%v", nullGot.Tags, nullGot.Labels)
	}
}
