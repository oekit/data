// Package redisx 是 data 门面子包：Redis 客户端封装——String/Hash/List/Set/ZSet/Counter/BloomFilter/Batch/PubSub 操作。
//
// re-export 自 github.com/oekit/redisx：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package redisx

import src "github.com/oekit/redisx"

// Batch 批量命令集合（redisx#161 起源包标记 deprecated：Pipeline 的功能
// 子集旧类型，行为不变、void 返回签名保留兼容）。壳仍转发——门面跟版税
// 的契约是导出面镜像源包，源包未删本壳不裁；新代码请用 Pipeline。
//
//nolint:staticcheck // SA1019：转发源包 deprecated 符号是门面契约
type Batch = src.Batch
type BloomFilter = src.BloomFilter
type Client = src.Client
type Counter = src.Counter

// Pipeline 是 redis.Pipeliner 的键前缀代理（redisx v0.11.0，redisx#79）——
// Pipeline()/TxPipeline() 改返 *Pipeline，convertKey 全覆盖、未覆盖命令编译期拦截；
// 需要原生管道的场景走 Raw()。
type Pipeline = src.Pipeline
type RedisClientOption = src.RedisClientOption
type RedisClientOptionFunc = src.RedisClientOptionFunc
type RedisConfig = src.RedisConfig

var NewClient = src.NewClient
var NewClientFromAddr = src.NewClientFromAddr
var NewClientFromClient = src.NewClientFromClient
var NewClientFromConfig = src.NewClientFromConfig
var WithPrefix = src.WithPrefix
