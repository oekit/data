package cached

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/oekit/gormx/crud"
)

// 本文件是写路径与写后失效职责（#179 拆分）：先写库，成功后失效——
// 六条写方法的「作用域解析 + report + invalidate」尾串统一收口为
// afterWrite（#179），失效与上报的实现在本文件末尾。

// afterWrite 写路径尾串收口（#179）：写库成功后统一做「作用域解析 + 失效」。
//
// 作用域解析失败 ⇒ 无法定位该删哪个键 ⇒ 跳过失效并上报 op="scope"
// （TTL 兜底）；写操作本身已成功，不因此报错（报错会让调用方以为写失败
// 而重试）。addToBloom 仅 Create 族置位——bloom 只增不减，Update/Delete
// 重复 Add 无害但对语义无益（见 bloom.go）。
//
// bloomAdd 与作用域解耦（#204）：bloom 元素刻意不按作用域切分
// （bloomItem），不消费 scope——作用域失败只跳过 invalidate，bloomAdd
// 照常执行。跳过 invalidate 有 TTL 兜底；跳过 bloomAdd 没有任何兜底
// （bloom 只增不减，Create 是新 ID 的唯一补录点，错过即被 bloomGate
// 判「绝对不存在」且永不自愈）。
func (r *CachedRepository[T, ID]) afterWrite(ctx context.Context, ids []ID, addToBloom bool) {
	scope, err := r.scope(ctx)
	if err != nil {
		r.report(ctx, "scope", nil, err)
	} else {
		r.invalidate(ctx, scope, ids)
	}
	if addToBloom {
		r.bloomAdd(ctx, ids)
	}
}

// Create 写库后删实体键（预置主键的 Create 之前可能已有 null marker，
// 必须清）并 bump 查询版本（新行进列表）。
func (r *CachedRepository[T, ID]) Create(ctx context.Context, entity *T) error {
	if err := r.cfg.repo.Create(ctx, entity); err != nil {
		return err
	}
	r.afterWrite(ctx, r.entityIDs(entity), true)
	return nil
}

// BatchCreate 写库后逐实体删键 + 一次版本 bump。
func (r *CachedRepository[T, ID]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := r.cfg.repo.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []ID
	for _, e := range entities {
		ids = append(ids, r.entityIDs(e)...)
	}
	r.afterWrite(ctx, ids, true)
	return nil
}

// Update 写库后删实体键 + bump 查询版本。回传受影响行数（gormx v0.7.0，gormx#60）；
// opts 限定写入列（gormx#61），原样转发给底层仓储。
func (r *CachedRepository[T, ID]) Update(ctx context.Context, entity *T, opts ...crud.UpdateOption) (int64, error) {
	rows, err := r.cfg.repo.Update(ctx, entity, opts...)
	if err != nil {
		return rows, err
	}
	r.afterWrite(ctx, r.entityIDs(entity), false)
	return rows, nil
}

// UpdateFields 写库后删实体键 + bump 查询版本。回传受影响行数（gormx v0.7.0，gormx#60）。
func (r *CachedRepository[T, ID]) UpdateFields(ctx context.Context, id ID, fields map[string]any) (int64, error) {
	rows, err := r.cfg.repo.UpdateFields(ctx, id, fields)
	if err != nil {
		return rows, err
	}
	r.afterWrite(ctx, []ID{id}, false)
	return rows, nil
}

// Delete 写库后删实体键 + bump 查询版本。回传受影响行数（gormx v0.7.0，gormx#60）。
func (r *CachedRepository[T, ID]) Delete(ctx context.Context, id ID) (int64, error) {
	rows, err := r.cfg.repo.Delete(ctx, id)
	if err != nil {
		return rows, err
	}
	r.afterWrite(ctx, []ID{id}, false)
	return rows, nil
}

// BatchDelete 写库后批量删键 + 一次版本 bump。回传受影响行数（gormx v0.7.0，gormx#60）。
func (r *CachedRepository[T, ID]) BatchDelete(ctx context.Context, ids []ID) (int64, error) {
	rows, err := r.cfg.repo.BatchDelete(ctx, ids)
	if err != nil {
		return rows, err
	}
	r.afterWrite(ctx, ids, false)
	return rows, nil
}

// invalidate 写库成功后的失效：删实体键（best-effort）+ bump 查询版本。
// 失败不回滚写操作，经 onError 上报，TTL 兜底。
//
// 失效用 WithoutCancel 派生（#141）：失效发生在写已提交之后，是第二次
// I/O，不应再受请求 ctx 约束——请求在提交瞬间被取消（客户端断开 /
// deadline 到期 / defer cancel）若吞掉失效，缓存残留写前旧值且查询版本
// 未 bump，整个 scope 的查询缓存一起脏到 TTL，且默认配置下完全静默。
// WithoutCancel 收进本函数一处，全部调用点（含未来新增）无需各自记得；
// 口径与 RunInTx 提交后的失效一致（applyCollected 处的 WithoutCancel
// 由此变为冗余但无害的双保险）。
func (r *CachedRepository[T, ID]) invalidate(ctx context.Context, scope string, ids []ID) {
	ctx = context.WithoutCancel(ctx)
	if len(ids) > 0 {
		keys := make([]string, 0, len(ids))
		prefix := r.idKeyPrefix(scope)
		for _, id := range ids {
			keys = append(keys, prefix+r.encodeID(id))
		}
		if err := r.entity.DelBatch(ctx, keys...); err != nil {
			r.report(ctx, "invalidate", keys, err)
		}
	}
	if r.cfg.qc != nil {
		ver := newVersionToken()
		verKey := r.verKey(scope)
		if err := r.cfg.qc.versions.Set(ctx, verKey, &ver); err != nil {
			r.report(ctx, "invalidate", []string{verKey}, err)
		}
	}
}

// report 上报缓存侧异常；无钩子或无错误时静默（miss 不是错误）。
func (r *CachedRepository[T, ID]) report(ctx context.Context, op string, keys []string, err error) {
	if err == nil || r.cfg.onError == nil {
		return
	}
	r.cfg.onError(ctx, op, keys, err)
}

// newVersionToken 生成版本令牌：随机 16 字节 hex。不用自增——
// 并发写下读-改-写会丢 bump，随机令牌无条件覆盖即天然"后写者生效"。
func newVersionToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败几乎只可能是系统级故障，退化为纳秒时间戳
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}
