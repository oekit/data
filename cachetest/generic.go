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
