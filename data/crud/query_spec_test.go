package crud

import "testing"

// TestQuerySpec_Assembly 验证中性 Spec 物化：AND/OR/排序/预加载/分页/锁
// 各字段按链式调用顺序收集，值全部占位符参数化。
func TestQuerySpec_Assembly(t *testing.T) {
	q := NewQuery().
		Where("name", "=", "test").
		Where("age", ">", 18).
		Or("status", "=", "disabled").
		Order("created_at DESC").
		Preload("Profile").
		Select("id", "name").
		Omit("password").
		GroupBy("tenant_id").
		Page(2, 20).
		ForUpdate()

	spec := q.Spec()

	if len(spec.Conds) != 2 {
		t.Fatalf("expected 2 AND conds, got %d", len(spec.Conds))
	}
	if spec.Conds[0].Query != "name = ?" || spec.Conds[0].Args[0] != "test" {
		t.Errorf("cond[0] = %q %v", spec.Conds[0].Query, spec.Conds[0].Args)
	}
	if spec.Conds[1].Query != "age > ?" || spec.Conds[1].Args[0] != 18 {
		t.Errorf("cond[1] = %q %v", spec.Conds[1].Query, spec.Conds[1].Args)
	}
	if len(spec.OrConds) != 1 || spec.OrConds[0].Query != "status = ?" {
		t.Errorf("orConds = %+v", spec.OrConds)
	}
	if len(spec.Orders) != 1 || spec.Orders[0] != "created_at DESC" {
		t.Errorf("orders = %v", spec.Orders)
	}
	if len(spec.Preloads) != 1 || spec.Preloads[0].Assoc != "Profile" {
		t.Errorf("preloads = %+v", spec.Preloads)
	}
	if len(spec.Selects) != 2 || len(spec.Omits) != 1 {
		t.Errorf("selects=%v omits=%v", spec.Selects, spec.Omits)
	}
	if spec.GroupBy != "tenant_id" {
		t.Errorf("groupBy = %q", spec.GroupBy)
	}
	if spec.Page == nil || spec.Page.Page != 2 || spec.Page.PageSize != 20 {
		t.Errorf("page = %+v", spec.Page)
	}
	if !spec.ForUpdate {
		t.Error("ForUpdate not set")
	}
	if q.IsEmpty() {
		t.Error("IsEmpty() = true for non-empty query")
	}
}

// TestQuerySpec_FailClosed 未知操作符必须渲染为恒假 1=0，绝不静默丢弃。
func TestQuerySpec_FailClosed(t *testing.T) {
	q := NewQuery().Where("age", "; DROP TABLE users; --", 1)
	spec := q.Spec()
	if len(spec.Conds) != 1 || spec.Conds[0].Query != failClosedCondition {
		t.Fatalf("expected fail-closed 1=0, got %+v", spec.Conds)
	}
	if len(spec.Conds[0].Args) != 0 {
		t.Errorf("fail-closed cond should carry no args, got %v", spec.Conds[0].Args)
	}
}

// TestQuerySpec_SnapshotIsolation Spec 返回快照，物化后继续链式修改
// Query 不影响已取得的 Spec。
func TestQuerySpec_SnapshotIsolation(t *testing.T) {
	q := NewQuery().Where("a", "=", 1)
	spec := q.Spec()
	q.Where("b", "=", 2)
	if len(spec.Conds) != 1 {
		t.Errorf("snapshot mutated: %d conds", len(spec.Conds))
	}
	if len(q.Spec().Conds) != 2 {
		t.Errorf("query should have 2 conds after chained Where")
	}
}
