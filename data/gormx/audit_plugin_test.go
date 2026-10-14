package gormx

import (
	"context"
	"testing"
)

// TestAuditPlugin_BatchCreate_SetsAuditFields 回归测试：BatchCreate（slice
// ReflectValue）时审计字段必须逐元素填充 —— 此前 setField 遇到 slice 直接跳过，
// 批量插入静默得到空 created_by/updated_by。
func TestAuditPlugin_BatchCreate_SetsAuditFields(t *testing.T) {
	db := newTestDB(t, &TestAudit{})
	if err := db.Use(NewAuditPlugin(func(ctx context.Context) int64 {
		return 42
	})); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TestAudit{}); err != nil {
		t.Fatal(err)
	}

	users := []*TestAudit{
		{Name: "b1"},
		{Name: "b2"},
		{Name: "b3"},
	}
	if err := db.WithContext(context.Background()).CreateInBatches(&users, 2).Error; err != nil {
		t.Fatalf("CreateInBatches: %v", err)
	}

	for i, u := range users {
		if u.CreatedBy != 42 {
			t.Errorf("user %d: expected CreatedBy=42, got %d", i, u.CreatedBy)
		}
	}

	// 从 DB 侧二次确认（而非仅内存对象）
	var loaded []TestAudit
	if err := db.WithContext(context.Background()).Find(&loaded).Error; err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(loaded))
	}
	for i, u := range loaded {
		if u.CreatedBy != 42 {
			t.Errorf("loaded user %d: expected CreatedBy=42, got %d", i, u.CreatedBy)
		}
	}
}

// TestAuditPlugin_BatchCreate_NonPointerSlice 验证值类型 slice（[]TestAudit）
// 的批量插入同样填充审计字段。
func TestAuditPlugin_BatchCreate_NonPointerSlice(t *testing.T) {
	db := newTestDB(t, &TestAudit{})
	if err := db.Use(NewAuditPlugin(func(ctx context.Context) int64 {
		return 7
	})); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TestAudit{}); err != nil {
		t.Fatal(err)
	}

	users := []TestAudit{{Name: "v1"}, {Name: "v2"}}
	if err := db.WithContext(context.Background()).Create(&users).Error; err != nil {
		t.Fatalf("Create(slice): %v", err)
	}

	for i, u := range users {
		if u.CreatedBy != 7 {
			t.Errorf("user %d: expected CreatedBy=7, got %d", i, u.CreatedBy)
		}
	}
}

// TestAuditPlugin_MapUpdates_SetsUpdatedBy 回归：setField 曾只改
// ReflectValue（struct），Updates(map[string]any) 的 SET 列表完全来自 map
// ——该路径 updated_by 恒为 0 且无告警。钉住：map 路径按 schema 列名注入。
func TestAuditPlugin_MapUpdates_SetsUpdatedBy(t *testing.T) {
	db := newTestDB(t, &TestAudit{})
	if err := db.Use(NewAuditPlugin(func(ctx context.Context) int64 {
		return 99
	})); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TestAudit{}); err != nil {
		t.Fatal(err)
	}

	u := &TestAudit{Name: "mu"}
	if err := db.WithContext(context.Background()).Create(u).Error; err != nil {
		t.Fatal(err)
	}

	// map 形式的 Updates（crud UpdateFields 走的就是这条路）
	if err := db.Model(&TestAudit{}).Where("id = ?", u.ID).
		Updates(map[string]any{"name": "mu2"}).Error; err != nil {
		t.Fatal(err)
	}

	var loaded TestAudit
	if err := db.WithContext(context.Background()).First(&loaded, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.UpdatedBy != 99 {
		t.Errorf("map Updates should fill updated_by=99, got %d", loaded.UpdatedBy)
	}
}
