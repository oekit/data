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

type RetryHooks = src.RetryHooks
type RetryOutcome = src.RetryOutcome

// DeliveryAtMostOnce 至多一次投递（默认）：claim 即从队列删除，执行中断
// 任务即丢失。
const DeliveryAtMostOnce = src.DeliveryAtMostOnce

// DeliveryAtLeastOnce 至少一次投递：claim 移入租约集，中断可被重新认领
// 重投（消费方必须幂等）。
const DeliveryAtLeastOnce = src.DeliveryAtLeastOnce

// RetrySuccess 重试终态：任务执行成功。
const RetrySuccess = src.RetrySuccess

// RetryInterrupted 重试终态：被停机打断（任务未到终态，中断处置已由钩子完成）。
const RetryInterrupted = src.RetryInterrupted

// RetryExhausted 重试终态：次数耗尽仍失败（终态处置交给调用方）。
const RetryExhausted = src.RetryExhausted

type Task = src.Task
type TimerTask = src.TimerTask
type TimerTaskOption = src.TimerTaskOption

// ErrBackendAlreadyStarted 后端重复启动（Start 已在运行）。
var ErrBackendAlreadyStarted = src.ErrBackendAlreadyStarted

// ErrEmptyTaskID 提交的任务没有 ID（任务 ID 必须唯一）。
var ErrEmptyTaskID = src.ErrEmptyTaskID

// ErrBackendStopped 后端已停机（Start 已返回），实例进入终态。
var ErrBackendStopped = src.ErrBackendStopped

var NewLocalBackend = src.NewLocalBackend
var NormalizeRetry = src.NormalizeRetry
var ResolveOptions = src.ResolveOptions
var RunWithRetry = src.RunWithRetry
var WithAtLeastOnce = src.WithAtLeastOnce
var WithBuckets = src.WithBuckets
var WithDeliveryMode = src.WithDeliveryMode
var WithOnFailure = src.WithOnFailure
var WithRetry = src.WithRetry
var WithVisibilityTimeout = src.WithVisibilityTimeout
var WithWorkers = src.WithWorkers

// ---- Clock 注入形态（timertask v0.2.x）----

type Clock = src.Clock

type SystemClock = src.SystemClock

var WithClock = src.WithClock
