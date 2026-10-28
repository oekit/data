// Package timertask 是 data 门面子包：分片定时任务——本地后端 + 选项集。
//
// re-export 自 github.com/oekit/timertask：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
//
// Redis 后端自 timertask v0.2.0 起迁至嵌套模块 github.com/oekit/timertask/redis
// （根包不再引 go-redis，纯 stdlib 嵌入式场景可用）——本壳因此不再转发
// RedisBackend/ShardManager/NewRedisBackend/NewShardManager；需要 Redis 形态请直接
// 依赖 github.com/oekit/timertask/redis。
package timertask

import src "github.com/oekit/timertask"

type BackendConfig = src.BackendConfig
type DeliveryMode = src.DeliveryMode
type Handler = src.Handler
type LocalBackend = src.LocalBackend
type Task = src.Task
type TimerTask = src.TimerTask
type TimerTaskOption = src.TimerTaskOption

// ErrEmptyTaskID 提交的任务没有 ID（任务 ID 必须唯一）。
var ErrEmptyTaskID = src.ErrEmptyTaskID

var NewLocalBackend = src.NewLocalBackend
var ResolveOptions = src.ResolveOptions
var WithAtLeastOnce = src.WithAtLeastOnce
var WithBuckets = src.WithBuckets
var WithDeliveryMode = src.WithDeliveryMode
var WithOnFailure = src.WithOnFailure
var WithRetry = src.WithRetry
var WithVisibilityTimeout = src.WithVisibilityTimeout
var WithWorkers = src.WithWorkers
