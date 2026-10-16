// Package cache 是 data 门面子包：缓存域——接口（Cache/CacheAside/SourceFunc）+ 本地缓存（LocalCache/SingleFlightCache）+ 缓存键构造 + Checker。实现在 pkg/cache。
//
// re-export 自 github.com/oekit/pkg/cache：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package cache

import src "github.com/oekit/pkg/cache"

type Cache[T any] = src.Cache[T]
type CacheAside[T any] = src.CacheAside[T]
type Checker = src.Checker
type LocalCache[T any] = src.LocalCache[T]
type LocalConfig[T any] = src.LocalConfig[T]
type Option = src.Option
type SingleFlightCache[T any] = src.SingleFlightCache[T]
type SourceFunc[T any] = src.SourceFunc[T]

var HashKey = src.HashKey
var Key = src.Key
var TenantKey = src.TenantKey
var UserKey = src.UserKey
var NewChecker = src.NewChecker
var WithClock = src.WithClock
