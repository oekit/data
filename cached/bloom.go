package cached

import (
	"context"
	"strconv"
)

// BloomChecker 是布隆过滤器的最小接口（redisx.BloomFilter 天然满足，
// 测试可注入 fake）。
type BloomChecker interface {
	// Exists 判断元素"可能存在"（true 含误判）或"绝对不存在"（false 可信）。
	Exists(ctx context.Context, item string) (bool, error)
	// Add 写入元素（只增不减）。
	Add(ctx context.Context, item string) error
}

// WithBloomFilter 开启布隆防穿透前置门（可选件）：GetByID 回源前先问
// bloom，判定"绝对不存在"的 ID 直接走 null marker，仓储零调用——
// 在 null marker 之前再挡掉一层恶意 ID 扫描。
//
// ⚠️ 启用前提（违反会造成"数据消失"级事故）：
//   - **所有写入必须走本装饰器**（Create 时自动 Add 新 ID）——旁路写入
//     的 ID 不在 bloom 里，点读会被误判为不存在（null marker 兜底，
//     错误窗口受 null TTL 约束但数据在窗口内不可见）；
//   - **存量数据必须预热**（部署前把既有 ID 全量 MAdd）；
//   - ID 空间应当单调递增（自增主键天然满足）——bloom 只增不减，删除
//     不摘除，误判只会放行仓储（正确，只是少省一次查询），绝不会反向。
//
// bloom 读失败 fail-open 放行仓储（fail-closed 会错杀真数据，绝不接受）；
// 读写异常均经 WithOnError 上报（op="bloom"）。
func WithBloomFilter[T any](bf BloomChecker) CachedOption[T] {
	return func(c *config[T]) { c.bloom = bf }
}

// bloomItem 实体 ID 的 bloom 元素形态（实体类型专享 bloom 实例，元素即
// ID 字符串；跨类型复用同一 bloom 会导致键空间互串）。
func bloomItem(id int64) string {
	return strconv.FormatInt(id, 10)
}

// bloomGate 点读回源前置门：返回 gated=true 表示 bloom 判定"绝对不存在"
// （调用方直接走 null marker）；"可能存在"或读失败（fail-open）均放行仓储。
func (r *CachedRepository[T]) bloomGate(ctx context.Context, id int64) bool {
	if r.cfg.bloom == nil {
		return false
	}
	exists, err := r.cfg.bloom.Exists(ctx, bloomItem(id))
	if err != nil {
		r.report(ctx, "bloom", nil, err)
		return false
	}
	return !exists
}

// bloomAdd 把新 born 的实体 ID 写入 bloom（best-effort，失败上报不阻断）。
// 仅在 Create 路径调用（Update/Delete 不动 bloom——只增不减的过滤器
// 对已存在元素重复 Add 无害但对语义无益）。
func (r *CachedRepository[T]) bloomAdd(ctx context.Context, ids []int64) {
	if r.cfg.bloom == nil {
		return
	}
	for _, id := range ids {
		if err := r.cfg.bloom.Add(ctx, bloomItem(id)); err != nil {
			r.report(ctx, "bloom", nil, err)
		}
	}
}
