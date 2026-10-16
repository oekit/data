// Package rediscache 是 data 门面子包：Redis 分布式缓存（RedisCache，CacheAside 的 Redis 后端，跑 pkg/cache/cachetest 契约）。
//
// re-export 自 github.com/oekit/redisx/cache：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package rediscache

import src "github.com/oekit/redisx/cache"

type RedisCache[T any] = src.RedisCache[T]
type RedisConfig[T any] = src.RedisConfig[T]
