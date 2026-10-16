// Package timertask 是 data 门面子包：分片定时任务——Local/Redis 后端 + FNV-1a 分片 + 心跳续约。
//
// re-export 自 github.com/oekit/timertask：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package timertask

import src "github.com/oekit/timertask"

type Handler = src.Handler
type LocalBackend = src.LocalBackend
type RedisBackend = src.RedisBackend
type ShardManager = src.ShardManager
type Task = src.Task
type TimerTask = src.TimerTask
type TimerTaskOption = src.TimerTaskOption

var NewLocalBackend = src.NewLocalBackend
var NewRedisBackend = src.NewRedisBackend
var NewShardManager = src.NewShardManager
var WithBuckets = src.WithBuckets
var WithOnFailure = src.WithOnFailure
var WithRetry = src.WithRetry
var WithWorkers = src.WithWorkers
