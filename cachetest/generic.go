package cachetest

import (
	"testing"

	pkgcache "github.com/oekit/pkg/cache"

	src "github.com/oekit/pkg/cache/cachetest"
)

// Run 跑完整契约场景（8 个子测试）。
func Run[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source pkgcache.SourceFunc[T]) pkgcache.CacheAside[T]) {
	src.Run(t, c, factory)
}

// Comparable 返回可比较类型的现成 Contract。
func Comparable[T comparable]() Contract[T] { return src.Comparable[T]() }

// FakeClock 是套件提供的假时钟（并发安全；测试内手动推进）——供时钟注入实现
// 接入 RunWithClock 假时钟 TTL 套件（pkg v0.13.0）。
type FakeClock = src.FakeClock

// NewFakeClock 创建起点为 2026-01-01T00:00:00Z 的假时钟。
var NewFakeClock = src.NewFakeClock

// ClockFactory 是支持时钟注入的缓存工厂形态：实现把 clock.Now 路由到全部 TTL 判定。
type ClockFactory[T any] = src.ClockFactory[T]

// RunWithClock 跑时钟驱动的 TTL 边界场景（4 子测试：过期闭边界/TTL=0 永不过期/
// null marker 过窗重源/重写刷新过期）。
func RunWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunWithClock(t, c, factory)
}

// RunSweepWithClock 跑「手动清扫」契约（pkg v0.16.0，pkg#25）——实现须提供
// cache.Sweeper 能力（Sweep(ctx) int），未提供即判失败。
func RunSweepWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunSweepWithClock(t, c, factory)
}

// RunHitWithClock 跑「命中语义」契约（pkg v0.16.0，pkg#25）——实现须提供
// cache.HitGetter 能力（GetEx(ctx) (*T, bool, error)），未提供即判失败。
func RunHitWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunHitWithClock(t, c, factory)
}

// RunReadIsolation 跑「读命中返回副本」契约（pkg v0.16.0，pkg#25）——调用方
// 改写 Get 返回值不得污染缓存内部条目。
func RunReadIsolation[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source pkgcache.SourceFunc[T]) pkgcache.CacheAside[T]) {
	src.RunReadIsolation(t, c, factory)
}

// RunReadPromotion 跑「读命中提升顺序（LRU）」契约（pkg v0.16.0，pkg#25）——
// factory 须构造容量为 capacity 的 LRU 缓存，场景能区分 FIFO 与 LRU。
func RunReadPromotion[T any](t *testing.T, c Contract[T], capacity int, factory func(t *testing.T, source pkgcache.SourceFunc[T]) pkgcache.CacheAside[T]) {
	src.RunReadPromotion(t, c, capacity, factory)
}

// RunNoBackgroundGoroutine 跑「不起后台 goroutine」契约（pkg v0.16.0，pkg#25）——
// 构造缓存不得在进程内新增 goroutine。
func RunNoBackgroundGoroutine[T any](t *testing.T, factory func(t *testing.T, source pkgcache.SourceFunc[T]) pkgcache.CacheAside[T]) {
	src.RunNoBackgroundGoroutine(t, factory)
}

// RunStaleIfErrorWithClock 跑「stale-if-error」契约（pkg v0.23.0，pkg#72）——
// 回源失败时回退已过期的历史真值（fail-open），不得把回源错误原样抛出。
// 钉住的是「装饰器不得改变内层语义」：SingleFlightCache 包装 LocalCache 时，
// 其前置检查若走 Get（惰性删除路径）会把 stale 兜底需要的那份过期真值删掉。
func RunStaleIfErrorWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunStaleIfErrorWithClock(t, c, factory)
}

// RunStaleIfErrorRepeatedWithClock 跑「stale 兜底可反复生效」契约
// （pkg v0.23.0，pkg#72）——兜底值必须留在表内，否则只生效第一次。
func RunStaleIfErrorRepeatedWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunStaleIfErrorRepeatedWithClock(t, c, factory)
}

// RunStatsWithClock 跑「观测计数」契约（pkg v0.24.0，pkg#76）——实现须提供
// cache.StatsProvider 能力（Stats() cache.Stats）；计数口径自洽（每次读恰好
// 计入命中或未命中之一）且单调不减。
func RunStatsWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunStatsWithClock(t, c, factory)
}

// RunSourceInjectionWithClock 跑「按调用注入回源」契约（pkg v0.24.0，pkg#77）
// ——实现须提供 cache.SourceInjector 能力；钉住注入生效 / 命中不回源 /
// 注入为 nil 退回构造期 source / 动态 TTL 生效 / 显式 ttl 覆盖动态 TTL。
func RunSourceInjectionWithClock[T any](t *testing.T, c Contract[T], factory ClockFactory[T]) {
	src.RunSourceInjectionWithClock(t, c, factory)
}
