// Package cache 是 data 门面子包：缓存域——接口（Cache/CacheAside/BatchGetter/SourceFunc）+ 本地缓存（LocalCache/SingleFlightCache）+ 缓存键构造 + Checker。实现在 pkg/cache。
//
// re-export 自 github.com/oekit/pkg/cache：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package cache

import src "github.com/oekit/pkg/cache"

type BatchGetter[T any] = src.BatchGetter[T]
type Cache[T any] = src.Cache[T]
type CacheAside[T any] = src.CacheAside[T]
type Checker = src.Checker
type LocalCache[T any] = src.LocalCache[T]
type LocalConfig[T any] = src.LocalConfig[T]
type Option = src.Option
type SingleFlightCache[T any] = src.SingleFlightCache[T]
type SourceFunc[T any] = src.SourceFunc[T]

// SourceFuncWithTTL 携带动态回填 TTL 的数据获取函数（pkg v0.14.0，pkg#11）。
type SourceFuncWithTTL[T any] = src.SourceFuncWithTTL[T]

// EvictPolicy 淘汰策略（pkg v0.16.0，pkg#25）——LocalConfig.EvictPolicy 取值。
type EvictPolicy = src.EvictPolicy

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
