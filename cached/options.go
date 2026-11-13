package cached

import (
	"context"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
)

// CacheFactory 用装饰器注入的回源函数构造缓存后端。
//
// 装饰器把「键 → ID 解析、ErrNotFound → (nil, nil)」的数据源交给工厂，
// 工厂决定后端形态（LocalCache/RedisCache、是否包 SingleFlightCache）
// 与 TTL——TTL 是删缓存失败时的唯一安全网，务必配置有限值。
type CacheFactory[T any] func(source cache.SourceFunc[T]) cache.CacheAside[T]

// OnErrorFunc 缓存侧异常上报钩子：读降级（op="read"）、回填失败
// （op="backfill"）、指纹失败（op="fingerprint"）、写后失效失败
// （op="invalidate"）。所有场景都是"降级继续 + TTL 兜底"，钩子只管
// 观测（记日志/打点），不能也不需要参与决策。
type OnErrorFunc func(ctx context.Context, op string, keys []string, err error)

// config 是装饰器配置。
type config[T any, ID comparable] struct {
	repo     crud.Repository[T, ID]
	prefix   string
	typ      string
	idFn     func(*T) ID
	encode   func(ID) string
	decode   func(string) (ID, error)
	qc       *QueryCache[T]
	bloom    BloomChecker
	onError  OnErrorFunc
	scopeKey ScopeKeyFunc

	// idAcc 构造期解析好的 ID 访问器（WithID 优先，缺省探测次之，
	// 都没有则为 nil）——写路径不再逐次反射（#181）。
	idAcc func(*T) (ID, bool)
	// decodeIDFn 构造期解析好的键段解码器（WithIDCodec 优先，缺省按
	// ID 类型一次性选定）——缓存 miss 热路径不再做反射类型解析（#181）。
	decodeIDFn func(string) (ID, error)
}

// CachedOption 配置 CachedRepository（命名避开裸 Option——多包
// co-import 冲突，仓内硬规则；本仓 data/cache 壳的 CheckerOption 同款改名）。
type CachedOption[T any, ID comparable] func(*config[T, ID])

// WithID 注入实体 ID 访问器，写路径据此删实体键。
// 缺省时按顺序探测：GetID() 方法（含指针接收者，gormx.BaseModel 系列
// 天然满足；返回类型须与 ID 精确一致）→ 名为 "ID" 的可导出字段（类型
// 须与 ID 一致）；两者都失败时写路径只 bump 查询版本、无法删实体键
// （点读一致性退化为 TTL 兜底）。
//
// ⚠️ 该「TTL 兜底」承诺仅在未开启 WithBloomFilter 时成立（#210）：开启
// bloom 时缺 ID 访问器是「新实体点读永久 NotFound」——Create 无法补录
// bloom，点读被前置门判「绝对不存在」且无自愈路径，并非 TTL 有界的退化。
// 该组合在 NewRepository 构造期 fail-fast 拒绝。
func WithID[T any, ID comparable](fn func(*T) ID) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.idFn = fn }
}

// WithIDCodec 注入缓存键中 ID 段的编解码（encode：ID → 键段字符串；
// decode：键段字符串 → ID）。
//
// 缺省编解码：string 直通；全部整型走十进制（与 v0.13 及之前 int64 时代
// 的键格式完全兼容）。其余 comparable 类型（复合键、自定义结构等）的
// 键往返不可逆，必须显式注入——否则 source 解析键时报错（fail-fast，
// 不会静默错回源）。
func WithIDCodec[T any, ID comparable](encode func(ID) string, decode func(string) (ID, error)) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.encode, c.decode = encode, decode }
}

// WithType 覆盖键中的类型名段（默认反射类型名字符串，如 "main.User"）。
// 同一实体跨服务共用缓存前缀时建议显式指定短名。
func WithType[T any, ID comparable](name string) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.typ = name }
}

// WithPrefix 覆盖键前缀（默认 "cached"）。多应用共享一个 Redis 时用
// 应用名做前缀隔离。
func WithPrefix[T any, ID comparable](prefix string) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.prefix = prefix }
}

// WithQueryCache 开启查询缓存（GetOne/List）。传 nil 等价不开启。
func WithQueryCache[T any, ID comparable](qc *QueryCache[T]) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.qc = qc }
}

// WithOnError 注入缓存侧异常上报钩子（读降级/回填失败/失效失败）。
func WithOnError[T any, ID comparable](fn OnErrorFunc) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.onError = fn }
}

// ScopeKeyFunc 按调用上下文解析「作用域键」（租户/用户/分区…）。
//
// 返回 error 时**整个操作失败**：作用域解析不出来却继续用共享键，正是
// 跨作用域串数据的根因。
type ScopeKeyFunc func(ctx context.Context) (string, error)

// WithScopeKey 注入作用域键解析器：作用域段进入**每一个缓存键**。
//
// 例外：布隆过滤器的元素**刻意不按作用域切分**（#199，与 bloomItem 的
// 实现口径一致）——bloom 只回答"可能存在/绝对不存在"、不缓存数据，共享
// 元素不会跨租户泄漏；按作用域切分反而会让每个租户各自承担存量预热
// 义务，放大"误判不存在"的风险。取舍理由见 WithBloomFilter / bloomItem。
//
// # 为什么需要
//
// 缓存键原本只有「前缀:类型:id:N」三段，**没有租户/作用域维度**。底层仓储
// 若是按租户隔离的（PostgreSQL RLS / 每租户连接 / 每租户库），同一个 ID 在
// 不同租户下是**不同的行**，却共用同一个缓存键 ⇒
//
//   - **跨租户读泄漏**：A 租户命中 B 租户写入的缓存条目；
//   - **「不存在」污染**：A 租户下不存在的 ID 写下的 null marker，让 B 租户
//     明明有这行却命中 marker 报 NotFound。
//
// 启用后键形态为「前缀:类型:scope:<作用域>:id:N」，作用域由 ctx 解析 ⇒
// **一个仓储实例可服务多个租户**（此前只能每租户一个实例 + 每租户一个前缀）。
//
// # 约束
//
//   - 作用域解析失败 ⇒ 操作失败（读路径降级直查仓储；写路径跳过失效并经
//     onError 上报，TTL 兜底；bloom 补录不消费作用域、照常执行，见 #204），
//     **绝不**回落到无作用域的共享键；
//   - 作用域键不得为空串——空串会被拒绝（否则等于没开隔离）；确实没有
//     作用域时请返回一个占位值（如 "-"）。
//
// 未启用时（默认）键形态与历史完全一致（无 scope 段），行为不变。
func WithScopeKey[T any, ID comparable](fn ScopeKeyFunc) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.scopeKey = fn }
}
