package gormx

import (
	"time"

	"gorm.io/gorm"
)

// BaseModel 是通用基础模型，包含 ID 和时间戳。
// 嵌入此结构体后，实体自动获得主键和时间戳字段。
type BaseModel struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// GetID 返回主键 ID。
func (m *BaseModel) GetID() int64 { return m.ID }

// SetID 设置主键 ID。
func (m *BaseModel) SetID(id int64) { m.ID = id }

// SoftDeleteModel 是带软删除的基础模型。
// Delete 操作会设置 deleted_at 而非物理删除。
type SoftDeleteModel struct {
	ID        int64          `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// GetID 返回主键 ID。
func (m *SoftDeleteModel) GetID() int64 { return m.ID }

// SetID 设置主键 ID。
func (m *SoftDeleteModel) SetID(id int64) { m.ID = id }

// AuditModel 是带审计字段的基础模型。
// 除了软删除外，还包含 created_by 和 updated_by 字段。
type AuditModel struct {
	ID        int64          `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
	CreatedBy int64          `json:"created_by"`
	UpdatedBy int64          `json:"updated_by"`
}

// GetID 返回主键 ID。
func (m *AuditModel) GetID() int64 { return m.ID }

// SetID 设置主键 ID。
func (m *AuditModel) SetID(id int64) { m.ID = id }
