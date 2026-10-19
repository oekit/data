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
