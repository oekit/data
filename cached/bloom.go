package cached

import (
	"context"
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
func WithBloomFilter[T any, ID comparable](bf BloomChecker) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.bloom = bf }
}

// bloomItem 实体 ID 的 bloom 元素形态（实体类型专享 bloom 实例，元素即
// ID 键段字符串，与缓存键同一编解码；跨类型复用同一 bloom 会导致键空间
// 互串）。
//
// **刻意不按作用域切分**：bloom 只回答"可能存在/绝对不存在"（提示器），
// **不缓存数据** ⇒ 跨租户共享元素不会造成数据泄漏（判"可能存在"只是多查
// 一次仓储，判"绝对不存在"也只是少查一次）。而按作用域切分会让**每个租户
// 各自承担存量预热义务**（见 WithBloomFilter 的预热要求）⇒ 放大"误判不
// 存在"的风险（某租户的存量行没进它自己的 bloom ⇒ 点读被 gate 成 NotFound）。
//
// 真正需要按作用域隔离的是**缓存键**（null marker 落在其中），那由
// WithScopeKey 保证。
func (r *CachedRepository[T, ID]) bloomItem(id ID) string {
	return r.encodeID(id)
}

// bloomGate 点读回源前置门：返回 gated=true 表示 bloom 判定"绝对不存在"
// （调用方直接走 null marker）；"可能存在"或读失败（fail-open）均放行仓储。
func (r *CachedRepository[T, ID]) bloomGate(ctx context.Context, id ID) bool {
	if r.cfg.bloom == nil {
		return false
	}
	exists, err := r.cfg.bloom.Exists(ctx, r.bloomItem(id))
	if err != nil {
		r.report(ctx, "bloom", nil, err)
		return false
	}
	return !exists
}

// bloomAdd 把新 born 的实体 ID 写入 bloom（best-effort，失败上报不阻断）。
// 仅在 Create 路径调用（Update/Delete 不动 bloom——只增不减的过滤器
// 对已存在元素重复 Add 无害但对语义无益）。
func (r *CachedRepository[T, ID]) bloomAdd(ctx context.Context, ids []ID) {
	if r.cfg.bloom == nil {
		return
	}
	for _, id := range ids {
		if err := r.cfg.bloom.Add(ctx, r.bloomItem(id)); err != nil {
			r.report(ctx, "bloom", nil, err)
		}
	}
}
