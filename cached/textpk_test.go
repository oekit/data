package cached_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
	"github.com/oekit/gormx/gormrepo"
	"gorm.io/gorm"
)

// textDoc 文本主键实体（TEXT PRIMARY KEY，客户端生成 hex 串）——issue #7
// 场景在缓存编排层的端到端钉版。
type textDoc struct {
	ID   string `gorm:"primaryKey;size:32"`
	Name string `gorm:"size:50"`
}

const textDocID = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"

func setupTextDocDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&textDoc{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newTextDocCachedRepo(t *testing.T, db *gorm.DB) *cached.CachedRepository[textDoc, string] {
	t.Helper()
	raw := gormrepo.NewGormRepository[textDoc, string](db)
	return cached.NewRepository[textDoc, string](raw,
		func(src dcache.SourceFunc[textDoc]) dcache.CacheAside[textDoc] {
			return dcache.NewSingleFlightCache(dcache.NewLocalCache(dcache.LocalConfig[textDoc]{
				TTL: time.Minute, Source: src, NullTTL: time.Minute,
			}))
		},
		cached.WithID[textDoc, string](func(d *textDoc) string { return d.ID }),
	)
}

// TestIntegrationTextPKCached 钉住文本主键在缓存编排层的全路径：
// 缺省键编解码（string 直通）经 source() 的键段解析往返——键格式
// "cached:类型:id:<hex>"，解码必须还原同一 ID，否则错回源/错失效。
func TestIntegrationTextPKCached(t *testing.T) {
	db := setupTextDocDB(t)
	cr := newTextDocCachedRepo(t, db)
	ctx := context.Background()

	if err := cr.Create(ctx, &textDoc{ID: textDocID, Name: "doc1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := cr.GetByID(ctx, textDocID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "doc1" {
		t.Errorf("Name = %q, want doc1", got.Name)
	}

	// 未命中 ID → null marker → 稳定 ErrNotFound
	if _, missErr := cr.GetByID(ctx, "ffffffffffffffffffffffffffffffaa"); !errors.Is(missErr, crud.ErrNotFound) {
		t.Errorf("miss want ErrNotFound, got %v", missErr)
	}

	// UpdateFields（string ID）写库 + 删实体键 → 点读必须拿到新值
	if updErr := cr.UpdateFields(ctx, textDocID, map[string]any{"name": "doc1-upd"}); updErr != nil {
		t.Fatalf("UpdateFields: %v", updErr)
	}
	got, err = cr.GetByID(ctx, textDocID)
	if err != nil {
		t.Fatalf("GetByID after UpdateFields: %v", err)
	}
	if got.Name != "doc1-upd" {
		t.Errorf("after UpdateFields Name = %q, want doc1-upd（实体键未被正确失效）", got.Name)
	}

	// GetByIDs 批量：string ID 做 map 键
	batch, err := cr.GetByIDs(ctx, []string{textDocID, "ffffffffffffffffffffffffffffffaa"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(batch) != 1 || batch[textDocID] == nil {
		t.Errorf("GetByIDs want only %s, got %d entries", textDocID, len(batch))
	}

	// Delete 失效 → null marker 稳定 ErrNotFound
	if err := cr.Delete(ctx, textDocID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := cr.GetByID(ctx, textDocID); !errors.Is(err, crud.ErrNotFound) {
		t.Errorf("after Delete want ErrNotFound, got %v", err)
	}
}
