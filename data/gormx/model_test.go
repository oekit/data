package gormx

import (
	"testing"
	"time"
)

func TestBaseModel_GetSetID(t *testing.T) {
	m := BaseModel{}
	if m.GetID() != 0 {
		t.Errorf("expected default ID 0, got %d", m.GetID())
	}

	m.SetID(42)
	if m.GetID() != 42 {
		t.Errorf("expected ID 42, got %d", m.GetID())
	}
}

func TestBaseModel_Fields(t *testing.T) {
	now := time.Now()
	m := BaseModel{
		ID:        1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if m.ID != 1 {
		t.Errorf("expected ID 1, got %d", m.ID)
	}
	if !m.CreatedAt.Equal(now) {
		t.Errorf("expected CreatedAt %v, got %v", now, m.CreatedAt)
	}
	if !m.UpdatedAt.Equal(now) {
		t.Errorf("expected UpdatedAt %v, got %v", now, m.UpdatedAt)
	}
}

func TestSoftDeleteModel_GetSetID(t *testing.T) {
	m := SoftDeleteModel{}
	if m.GetID() != 0 {
		t.Errorf("expected default ID 0, got %d", m.GetID())
	}

	m.SetID(100)
	if m.GetID() != 100 {
		t.Errorf("expected ID 100, got %d", m.GetID())
	}
}

func TestSoftDeleteModel_Fields(t *testing.T) {
	now := time.Now()
	m := SoftDeleteModel{
		ID:        1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if m.ID != 1 {
		t.Errorf("expected ID 1, got %d", m.ID)
	}
	if m.DeletedAt.Valid {
		t.Error("expected DeletedAt to be zero initially")
	}
}

func TestAuditModel_GetSetID(t *testing.T) {
	m := AuditModel{}
	if m.GetID() != 0 {
		t.Errorf("expected default ID 0, got %d", m.GetID())
	}

	m.SetID(200)
	if m.GetID() != 200 {
		t.Errorf("expected ID 200, got %d", m.GetID())
	}
}

func TestAuditModel_Fields(t *testing.T) {
	now := time.Now()
	m := AuditModel{
		ID:        1,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: 10,
		UpdatedBy: 20,
	}

	if m.ID != 1 {
		t.Errorf("expected ID 1, got %d", m.ID)
	}
	if m.CreatedBy != 10 {
		t.Errorf("expected CreatedBy 10, got %d", m.CreatedBy)
	}
	if m.UpdatedBy != 20 {
		t.Errorf("expected UpdatedBy 20, got %d", m.UpdatedBy)
	}
}

func TestBindPageRequest(t *testing.T) {
	// 这个测试需要 Hertz RequestContext，跳过
	// 实际集成测试中会覆盖
	t.Skip("requires Hertz RequestContext")
}
