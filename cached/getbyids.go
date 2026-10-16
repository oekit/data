package cached

import (
	"context"
	"errors"
	"fmt"

	"github.com/oekit/gormx/crud"
)

// GetByIDs 批量点读：逐 ID 走点读缓存路径（命中直达 / 未命中 GetOrLoad
// 回源回填 / 不存在的 ID 经 null marker 防穿透），并发场景由后端的
// SingleFlightCache 去重回源。
//
// 返回 map 中只包含找到的实体；未找到的 ID **不在 map 里且不算错误**
// （与点读缓存的 (nil, nil) 双关语义一致，第二次批量查询同类 ID 时
// 全部走缓存，仓储零调用）。单个 ID 的仓储硬错误不阻断其余 ID，
// 以 errors.Join 聚合返回——调用方拿到「部分成功 + 聚合错误」。
//
// 注意：这是"逐 ID 查"而非 MGet 批量网络往返——本地缓存后端每键开销
// 可忽略；Redis 后端是 N 次往返，大批量场景请自行控制批次大小
// （MGet 快路径等 pkg/cache 出现批量能力接口后再接）。
func (r *CachedRepository[T]) GetByIDs(ctx context.Context, ids []int64) (map[int64]*T, error) {
	out := make(map[int64]*T, len(ids))
	var errs []error
	for _, id := range ids {
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
