package cached

import (
	"context"
	"errors"
	"fmt"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
)

// GetByIDs 批量点读：后端具备 MGet 批量能力（pkg/cache.BatchGetter，
// LocalCache/RedisCache/twolevel 组合体均已实现）时一次批量往返拿真值，
// 缺席键再逐个走点读缓存路径（真 miss 回源回填、null marker 在后端短路）
// ——N 次网络往返收敛为 1 次批量 + 有限次回源；批量读失败自动降级逐 ID。
//
// 返回 map 中只包含找到的实体；未找到的 ID **不在 map 里且不算错误**
// （与点读缓存的 (nil, nil) 双关语义一致，第二次批量查询同类 ID 时
// 全部走缓存，仓储零调用）。单个 ID 的仓储硬错误不阻断其余 ID，
// 以 errors.Join 聚合返回——调用方拿到「部分成功 + 聚合错误」。
func (r *CachedRepository[T]) GetByIDs(ctx context.Context, ids []int64) (map[int64]*T, error) {
	out := make(map[int64]*T, len(ids))
	var errs []error

	// 去重保序（重复 ID 只取一次，map 结果与重复输入语义一致）
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}

	// 快路径：MGet 拿真值，缺席键留给 GetOrLoad（marker 短路/回源回填）
	pending := unique
	if bg, ok := r.entity.(cache.BatchGetter[T]); ok {
		keys := make([]string, 0, len(unique))
		keyOf := make(map[int64]string, len(unique))
		for _, id := range unique {
			k := r.idKey(id)
			keys = append(keys, k)
			keyOf[id] = k
		}
		hits, err := bg.MGet(ctx, keys...)
		if err != nil {
			// 批量读失败降级逐 ID（缓存后端故障不放大为批量失败）
			r.report(ctx, "read", keys, err)
		} else {
			pending = pending[:0]
			for _, id := range unique {
				if v, hit := hits[keyOf[id]]; hit {
					out[id] = v
				} else {
					pending = append(pending, id)
				}
			}
		}
	}

	for _, id := range pending {
		v, err := r.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, crud.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("cached: id %d: %w", id, err))
			continue
		}
		out[id] = v
	}
	return out, errors.Join(errs...)
}
