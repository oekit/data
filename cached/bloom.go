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

// BloomMAdder 是布隆过滤器的可选批量能力（redisx.BloomFilter 的 MAdd
// 天然满足）：一次往返写入一批元素。BloomChecker 刻意不含它以保持最小
// 接口——bloomAdd 运行时经类型断言探测，具备则批量、否则退化为逐个
// Add（#187：批量创建路径 N 次 Redis 往返收敛为 1 次）。
type BloomMAdder interface {
	// MAdd 批量写入元素（只增不减）。
	MAdd(ctx context.Context, items ...string) ([]bool, error)
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
//   - **实体 ID 必须可解析**（WithID 注入，或 GetID() 方法 / 名为 "ID" 的
//     可导出字段缺省探测命中）——否则 Create 拿不到 ID、新实体永不进
//     bloom，点读恒判「不存在」且无自愈；该组合构造期 fail-fast（#210）；
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
// 在 Create / BatchCreate 与事务提交路径调用（Update/Delete 不动 bloom——
// 只增不减的过滤器对已存在元素重复 Add 无害但对语义无益）。
//
// 后端具备 BloomMAdder 批量能力时一次往返写入全部元素，否则退化为逐个
// Add（#187：BatchCreate N 个实体不该付 N 次 Redis 往返）。
//
// 与 invalidate 同理用 WithoutCancel 派生 ctx（#141）：写已提交后再补录
// bloom 不受请求取消殃及——丢失 Add 会让后续点读把存在的 ID 误判为
// 「绝对不存在」而多打 DB（性能退化，非正确性事故）。
func (r *CachedRepository[T, ID]) bloomAdd(ctx context.Context, ids []ID) {
	if r.cfg.bloom == nil || len(ids) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, r.bloomItem(id))
	}
	if ma, ok := r.cfg.bloom.(BloomMAdder); ok {
		if _, err := ma.MAdd(ctx, items...); err != nil {
			r.report(ctx, "bloom", nil, err)
		}
		return
	}
	for _, item := range items {
		if err := r.cfg.bloom.Add(ctx, item); err != nil {
			r.report(ctx, "bloom", nil, err)
		}
	}
}
