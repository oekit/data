// Package cache 是 data 门面子包：缓存域——接口（Cache/CacheAside/BatchGetter/SourceFunc）+ 本地缓存（LocalCache/SingleFlightCache）+ 缓存键构造 + Checker。实现在 pkg/cache。
//
// re-export 自 github.com/oekit/pkg/cache：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package cache

import src "github.com/oekit/pkg/cache"

// BatchGetter 可选能力接口：批量读取。契约补充（data#114）：返回 map 中
// 出现的键必须是找到的**非 nil 真值**——null marker（不存在）不得作为
// 命中返回，缺席即不在 map 中（消费方对缺席键回退 GetOrLoad 享受 marker
// 短路）。LocalCache/RedisCache 等仓内实现均遵守；防御性消费方（如
// cached.GetByIDs）对 (key, nil) 命中按缺席处理。
type BatchGetter[T any] = src.BatchGetter[T]
type Cache[T any] = src.Cache[T]
type CacheAside[T any] = src.CacheAside[T]
type Checker = src.Checker

// CheckerOption Checker 可选项（pkg#355 禁裸 Option 改名，pkg v0.43.0）。
type CheckerOption = src.CheckerOption
type LocalCache[T any] = src.LocalCache[T]
type LocalConfig[T any] = src.LocalConfig[T]
type SingleFlightCache[T any] = src.SingleFlightCache[T]
type SourceFunc[T any] = src.SourceFunc[T]

// SourceFuncWithTTL 携带动态回填 TTL 的数据获取函数（pkg v0.14.0，pkg#11）。
type SourceFuncWithTTL[T any] = src.SourceFuncWithTTL[T]

// EvictPolicy 淘汰策略（pkg v0.16.0，pkg#25）——LocalConfig.EvictPolicy 取值。
type EvictPolicy = src.EvictPolicy

// EvictFIFO 写序淘汰（缺省）：FIFO + 重写提升，读命中不提升（历史行为）。
// 壳此前只转发类型未转发取值常量（门面跟版税漏跟，与 data#196 同族）——
// 消费方在门面侧写不出合法取值。
const EvictFIFO = src.EvictFIFO

// EvictLRU 读序淘汰：读命中同样提升顺序（最近最少使用的条目先淘汰）。
const EvictLRU = src.EvictLRU

// Sweeper 可选能力接口：手动清扫过期条目（pkg v0.16.0，pkg#25）。
type Sweeper = src.Sweeper

// HitGetter 可选能力接口：取值时区分「未命中」与「命中但值为 nil」（pkg v0.16.0，pkg#25）。
type HitGetter[T any] = src.HitGetter[T]

// PeekGetter 可选能力接口：非删除式读取——读到过期条目只算不命中、不删除它
// （pkg v0.23.0，pkg#72）。stale-if-error 兜底需要那份过期真值留在表内；
// 装饰器的前置检查若走 Get（惰性删除路径）会把它删掉 ⇒ 兜底失效。
type PeekGetter[T any] = src.PeekGetter[T]

// Stats 观测计数快照（pkg v0.24.0，pkg#76）——命中/未命中/容量淘汰/TTL 过期
// 回收计数。要暴露缓存指标的仓不必自写一份带计数的 TTL 缓存。
type Stats = src.Stats

// StatsProvider 可选能力接口：暴露缓存的观测计数快照（pkg v0.24.0，pkg#76）。
type StatsProvider = src.StatsProvider

// NXSetter 可选能力接口：不存在才写入（原子 SetNX，pkg v0.32.0，pkg#168）——
// 与 Redis SET NX 同语义：占位/一次性标记/生成码唯一性这类「先到先得」的写入，
// 判定与写入必须原子完成（Get + Set 两步在并发下会双双判「不存在」而覆盖彼此）。
type NXSetter[T any] = src.NXSetter[T]

// SourceInjector 可选能力接口：按调用注入回源函数（pkg v0.24.0，pkg#77）——
// 回源依赖 key / 依赖调用上下文的缓存（token/凭据/租户级）不必在缓存外再包
// 一层 key→source 注册表，且单飞语义不变。
type SourceInjector[T any] = src.SourceInjector[T]

var HashKey = src.HashKey
var Key = src.Key
var TenantKey = src.TenantKey
var UserKey = src.UserKey
var NewChecker = src.NewChecker
var WithClock = src.WithClock

// ErrNoStale 是「取消 stale 兜底」的可识别哨兵错误（pkg v0.33.0，pkg#172）——
// 回源错误 unwrap 命中本哨兵（errors.Is(err, ErrNoStale)）时，即使 StaleIfError
// 开着也不做 stale 兜底，错误原样回传。
var ErrNoStale = src.ErrNoStale

// ErrNXUnsupported 内层不具备 NXSetter 能力时的 fail-loud 哨兵（pkg v0.32.0，pkg#168）——
// SingleFlightCache.SetNX 转发时内层无此能力即返回它（能力缺失要看得见，不能假装成功）。
var ErrNXUnsupported = src.ErrNXUnsupported

// ---- SingleValue[T] 单值 TTL 缓存形态（pkg v0.37.0 #213）----

type SingleValue[T any] = src.SingleValue[T]

type SingleValueConfig[T any] = src.SingleValueConfig[T]

type SingleValueSource[T any] = src.SingleValueSource[T]

type SingleValueSourceWithTTL[T any] = src.SingleValueSourceWithTTL[T]

var ErrNoValue = src.ErrNoValue

// NewSingleValue 是泛型函数——Go 不允许 var 引用未实例化的泛型函数值，
// 以薄包装透传（类型同一：入参/返回值都是源包类型）。
func NewSingleValue[T any](cfg src.SingleValueConfig[T]) *src.SingleValue[T] {
	return src.NewSingleValue(cfg)
}

// ---- 批量命中形态（pkg v0.37.4 #238）----

// BatchEntry 是批量读取的单键结果（pkg#238）：Hit 区分「nil 真值命中」与「缺席」。
type BatchEntry[T any] = src.BatchEntry[T]

// BatchHitGetter 是可选能力接口：批量读取时区分「未命中」与「命中但值为
// nil」（pkg#238）——HitGetter 的批量形态（LocalCache：MGetEx）。
type BatchHitGetter[T any] = src.BatchHitGetter[T]
