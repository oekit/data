// Package cachetest 是 data 门面子包：CacheAside 实现契约测试套件（8 场景，含 -race）。
//
// re-export 自 github.com/oekit/pkg/cache/cachetest：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package cachetest

import src "github.com/oekit/pkg/cache/cachetest"

type Contract[T any] = src.Contract[T]
