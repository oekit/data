// Package distlock 是 data 门面子包：分布式锁——RedisLocker + WithLock + 优雅降级
// + 手动续期 Renew + 分布式信号量 Semaphore。
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

var ErrLockNotAcquired = src.ErrLockNotAcquired
var ErrLockTimeout = src.ErrLockTimeout
var WithLock = src.WithLock
var NewRedisLocker = src.NewRedisLocker
var NewSemaphore = src.NewSemaphore
var WithAutoRenew = src.WithAutoRenew
var WithGracefulDegradation = src.WithGracefulDegradation
var WithMaxAutoRenew = src.WithMaxAutoRenew
var WithOnLock = src.WithOnLock
var WithOnUnlock = src.WithOnUnlock
var WithPrefix = src.WithPrefix
var WithRenewInterval = src.WithRenewInterval
var WithRetryInterval = src.WithRetryInterval
