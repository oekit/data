// Package redisx 是 data 门面子包：Redis 客户端封装——String/Hash/List/Set/ZSet/Counter/BloomFilter/Batch/PubSub 操作。
//
// re-export 自 github.com/oekit/redisx：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package redisx

import src "github.com/oekit/redisx"

type Batch = src.Batch
type BloomFilter = src.BloomFilter
type Client = src.Client
type Counter = src.Counter
type RedisClientOption = src.RedisClientOption
type RedisClientOptionFunc = src.RedisClientOptionFunc
type RedisConfig = src.RedisConfig

var NewClient = src.NewClient
var NewClientFromAddr = src.NewClientFromAddr
var NewClientFromClient = src.NewClientFromClient
var NewClientFromConfig = src.NewClientFromConfig
var WithPrefix = src.WithPrefix
