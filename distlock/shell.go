// Package distlock 是 data 门面子包：分布式锁——RedisLocker + WithLock + 优雅降级
// + 手动续期 Renew + 分布式信号量 Semaphore + 分布式防爆破锁 Lockout。
//
// re-export 自 github.com/oekit/redisx/distlock：类型为泛型 type alias（与源包**完全同一类型**，
// 双向赋值互通）；非泛型函数/变量经 var 引用同一函数值。源包新增导出符号时
// 本壳需同步（门面跟版税，data 门面化决策已接受）。
package distlock

import src "github.com/oekit/redisx/distlock"

type Locker = src.Locker
type RedisLocker = src.RedisLocker
type RedisLockerOption = src.RedisLockerOption
type Semaphore = src.Semaphore
type Lockout = src.Lockout
type LockoutConfig = src.LockoutConfig

var ErrLockNotAcquired = src.ErrLockNotAcquired
var ErrLockTimeout = src.ErrLockTimeout

// ErrLockNotHeld 解锁时锁不存在或已易主——本次 Unlock 没有删除任何锁（redisx/distlock #115）。
var ErrLockNotHeld = src.ErrLockNotHeld
var WithLock = src.WithLock
var NewRedisLocker = src.NewRedisLocker
var NewSemaphore = src.NewSemaphore
var NewLockout = src.NewLockout
var WithAutoRenew = src.WithAutoRenew
var WithGracefulDegradation = src.WithGracefulDegradation

// WithLogger 注入 per-instance 日志（redisx/distlock #120）；watchdog 告警只进该日志。
var WithLogger = src.WithLogger
var WithMaxAutoRenew = src.WithMaxAutoRenew
var WithOnLock = src.WithOnLock
var WithOnUnlock = src.WithOnUnlock
var WithPrefix = src.WithPrefix
var WithRenewInterval = src.WithRenewInterval
var WithRetryInterval = src.WithRetryInterval

// SemaphoreOption 信号量选项（redisx/distlock #126 自动续期家族）。
type SemaphoreOption = src.SemaphoreOption

// BruteForceGuard 分布式+进程内双实现防爆破锁对（redisx/distlock #128）。
type BruteForceGuard = src.BruteForceGuard

// GuardConfig 防爆破锁对配置（阈值同源、MemFallback 降级路由）。
type GuardConfig = src.GuardConfig

var NewBruteForceGuard = src.NewBruteForceGuard

// WithSemaphoreAutoRenew 开启信号量自动续期（watchdog 间隔缺省 slotTTL/2，#126）。
var WithSemaphoreAutoRenew = src.WithSemaphoreAutoRenew
var WithSemaphoreRenewInterval = src.WithSemaphoreRenewInterval
var WithSemaphoreMaxAutoRenew = src.WithSemaphoreMaxAutoRenew
var WithSemaphoreLogger = src.WithSemaphoreLogger
