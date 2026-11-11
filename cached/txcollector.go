package cached

import (
	"context"
	"fmt"
	"sync"

	"github.com/oekit/gormx/crud"
)

// 本文件是事务路径职责（#179 拆分）：装饰器的事务包装（RunInTx）与
// 事务内的记账视图（txCollector），提交后的统一失效 applyCollected。

// RunInTx 事务包装：fn 收到的事务仓储把写操作记录进失效清单，
// 提交成功后统一失效（先删实体键后 bump 版本）；回滚不失效。
// 事务内的读操作直通事务仓储（保证读到事务内状态，缓存只会是旧的）。
func (r *CachedRepository[T, ID]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T, ID]) error) error {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T, ID])
	if !ok {
		return fmt.Errorf("cached: 底层仓储未实现 TxRepository，无法开启事务")
	}
	tw := &txCollector[T, ID]{outer: r}
	err := txRepo.RunInTx(ctx, func(txRepo crud.Repository[T, ID]) error {
		innerTx, ok := txRepo.(crud.TxRepository[T, ID])
		if !ok {
			return fmt.Errorf("cached: 事务内仓储未实现 TxRepository")
		}
		tw.rebind(txRepo, innerTx)
		return fn(tw)
	})
	if err != nil {
		return err
	}
	// 提交后的失效用 WithoutCancel：调用方在提交瞬间取消 context
	// 不能吞掉失效（失效失败只剩 TTL 兜底，能救一次是一次）。
	// 作用域在事务入口解析一次（事务内外的 ctx 相同，作用域相同），
	// 解析失败则跳过失效（TTL 兜底）——写已提交，不因此报错。
	scope, scopeErr := r.scope(ctx)
	if scopeErr != nil {
		r.report(ctx, "scope", nil, scopeErr)
		return nil
	}
	r.applyCollected(context.WithoutCancel(ctx), scope, tw)
	return nil
}

// GetByIDForUpdate 行锁读直通：锁读的语义就是"拿最权威的数据"，
// 过缓存既拿不到锁还会读到旧值。
func (r *CachedRepository[T, ID]) GetByIDForUpdate(ctx context.Context, id ID) (*T, error) {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T, ID])
	if !ok {
		return nil, fmt.Errorf("cached: 底层仓储未实现 TxRepository")
	}
	return txRepo.GetByIDForUpdate(ctx, id)
}

// txCollector 事务内的仓储视图：写操作只记账（收集失效清单），
// 读操作与行锁读直通事务仓储。inner/innerTx 与 ids/created 同锁保护
// （#180）：嵌套 RunInTx 会重绑 inner/innerTx，事务内并发读写收集器
// 视图时字段访问必须无数据竞争。
type txCollector[T any, ID comparable] struct {
	outer   *CachedRepository[T, ID]
	inner   crud.Repository[T, ID]
	innerTx crud.TxRepository[T, ID]

	mu      sync.Mutex
	ids     []ID
	created []ID
}

// rebind 绑定事务仓储视图（锁内写）。外层 RunInTx 的闭包与嵌套 RunInTx
// 的绑定/恢复共用。
func (t *txCollector[T, ID]) rebind(repo crud.Repository[T, ID], innerTx crud.TxRepository[T, ID]) {
	t.mu.Lock()
	t.inner, t.innerTx = repo, innerTx
	t.mu.Unlock()
}

// binding 读当前绑定（锁内读）。
func (t *txCollector[T, ID]) binding() (crud.Repository[T, ID], crud.TxRepository[T, ID]) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inner, t.innerTx
}

// bound 读当前事务仓储（锁内读，读写路径用）。
func (t *txCollector[T, ID]) bound() crud.Repository[T, ID] {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inner
}

// boundTx 读当前事务仓储的 Tx 形态（锁内读，行锁读/嵌套事务委托用）。
func (t *txCollector[T, ID]) boundTx() crud.TxRepository[T, ID] {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.innerTx
}

func (t *txCollector[T, ID]) note(ids []ID) {
	t.mu.Lock()
	t.ids = append(t.ids, ids...)
	t.mu.Unlock()
}

func (t *txCollector[T, ID]) noteCreated(ids []ID) {
	t.mu.Lock()
	t.created = append(t.created, ids...)
	t.mu.Unlock()
}

func (t *txCollector[T, ID]) GetByID(ctx context.Context, id ID) (*T, error) {
	return t.bound().GetByID(ctx, id)
}

func (t *txCollector[T, ID]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	return t.bound().GetOne(ctx, q)
}

func (t *txCollector[T, ID]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	return t.bound().GetByCondition(ctx, query, args...)
}

func (t *txCollector[T, ID]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	return t.bound().List(ctx, q)
}

func (t *txCollector[T, ID]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return t.bound().Exists(ctx, q)
}

func (t *txCollector[T, ID]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return t.bound().Count(ctx, q)
}

func (t *txCollector[T, ID]) Create(ctx context.Context, entity *T) error {
	if err := t.bound().Create(ctx, entity); err != nil {
		return err
	}
	ids := t.outer.entityIDs(entity)
	t.note(ids)
	t.noteCreated(ids)
	return nil
}

func (t *txCollector[T, ID]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := t.bound().BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []ID
	for _, e := range entities {
		ids = append(ids, t.outer.entityIDs(e)...)
	}
	t.note(ids)
	t.noteCreated(ids)
	return nil
}

func (t *txCollector[T, ID]) Update(ctx context.Context, entity *T, opts ...crud.UpdateOption) (int64, error) {
	rows, err := t.bound().Update(ctx, entity, opts...)
	if err != nil {
		return rows, err
	}
	t.note(t.outer.entityIDs(entity))
	return rows, nil
}

func (t *txCollector[T, ID]) UpdateFields(ctx context.Context, id ID, fields map[string]any) (int64, error) {
	rows, err := t.bound().UpdateFields(ctx, id, fields)
	if err != nil {
		return rows, err
	}
	t.note([]ID{id})
	return rows, nil
}

func (t *txCollector[T, ID]) Delete(ctx context.Context, id ID) (int64, error) {
	rows, err := t.bound().Delete(ctx, id)
	if err != nil {
		return rows, err
	}
	t.note([]ID{id})
	return rows, nil
}

func (t *txCollector[T, ID]) BatchDelete(ctx context.Context, ids []ID) (int64, error) {
	rows, err := t.bound().BatchDelete(ctx, ids)
	if err != nil {
		return rows, err
	}
	t.note(ids)
	return rows, nil
}

func (t *txCollector[T, ID]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T, ID]) error) error {
	// 嵌套事务重新包装同一收集器：内层 fn 收到的仍是收集器视图，
	// 写操作继续记账，失效仍由最外层 RunInTx 在提交后统一执行。
	// 若直接透传 fn，底层仓储会给出裸事务仓储，写操作绕过收集——
	// 提交后不失效，点读读到 TTL 窗口内的旧值。
	//
	// #180：委托前保存当前绑定、返回后恢复（锁内）。否则嵌套事务提交后
	// t.inner/innerTx 永久指向已提交的内层事务仓储，外层 fn 中后续写
	// 继续打到「已完成」的事务仓储上——gorm 靠 SAVEPOINT 共享同一
	// *sql.Tx 才侥幸成立，接口契约不保证（提交后的仓储视图行为未定义）。
	prevInner, prevInnerTx := t.binding()
	defer t.rebind(prevInner, prevInnerTx)
	return t.boundTx().RunInTx(ctx, func(repo crud.Repository[T, ID]) error {
		innerTx, ok := repo.(crud.TxRepository[T, ID])
		if !ok {
			return fmt.Errorf("cached: 事务内仓储未实现 TxRepository")
		}
		t.rebind(repo, innerTx)
		return fn(t)
	})
}

func (t *txCollector[T, ID]) GetByIDForUpdate(ctx context.Context, id ID) (*T, error) {
	return t.boundTx().GetByIDForUpdate(ctx, id)
}

// applyCollected 提交成功后的统一失效。
func (r *CachedRepository[T, ID]) applyCollected(ctx context.Context, scope string, t *txCollector[T, ID]) {
	t.mu.Lock()
	ids := t.ids
	created := t.created
	t.mu.Unlock()
	r.invalidate(ctx, scope, ids)
	r.bloomAdd(ctx, created)
}
