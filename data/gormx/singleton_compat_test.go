package gormx

import "testing"

// TestModel_InterfaceSatisfiedByBaseModels 验证三个 Base 模型通过嵌入即满足 Model 接口。
// 修复前 Model 接口要求 GetID() uint，而 BaseModel 等实现的是 GetID() int64，
// 导致 SingletonRepo 根本无法配合官方 Base 模型编译。
func TestModel_InterfaceSatisfiedByBaseModels(t *testing.T) {
	var _ Model = (*BaseModel)(nil)
	var _ Model = (*SoftDeleteModel)(nil)
	var _ Model = (*AuditModel)(nil)

	// 嵌入式实体也应满足接口
	type cfg struct {
		BaseModel
		Value string
	}
	var _ Model = (*cfg)(nil)

	// 运行期行为校验
	m := &cfg{Value: "default"}
	if m.GetID() != 0 {
		t.Errorf("expected default ID 0, got %d", m.GetID())
	}
	m.SetID(99)
	if m.GetID() != 99 {
		t.Errorf("expected ID 99, got %d", m.GetID())
	}
}
