package cachetest_test

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/data/cachetest"
	pkgcache "github.com/oekit/pkg/cache"
)

// TestRunViaFacade 经门面跑完整契约（LocalCache 自证，同时验证套件类型同一）。
func TestRunViaFacade(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), func(t *testing.T, source pkgcache.SourceFunc[int]) pkgcache.CacheAside[int] {
		return pkgcache.NewLocalCache(pkgcache.LocalConfig[int]{Source: source})
	})
}

// TestOptionalCapabilitySuitesViaFacade 经门面跑 pkg v0.16.0 新增的 5 个可选
// 能力契约套件（LocalCache 自证）——验证壳的包装转发真实生效（非仅编译通过）。
func TestOptionalCapabilitySuitesViaFacade(t *testing.T) {
	// 手动清扫：LocalCache 提供 Sweep，关闭后台清扫件由调用方显式回收。
	t.Run("sweep", func(t *testing.T) {
		cachetest.RunSweepWithClock(t, cachetest.Comparable[string](),
			func(t *testing.T, source pkgcache.SourceFunc[string], clock *cachetest.FakeClock) pkgcache.CacheAside[string] {
				c := pkgcache.NewLocalCache(pkgcache.LocalConfig[string]{
					TTL:               30 * time.Second,
					NullTTL:           time.Minute,
					Source:            source,
					Now:               clock.Now,
					NoBackgroundSweep: true,
				})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				return c
			})
	})

	// 命中语义：LocalCache 提供 GetEx（区分「不存在」与「有值但值为 nil」）。
	t.Run("hit", func(t *testing.T) {
		cachetest.RunHitWithClock(t, cachetest.Comparable[string](),
			func(t *testing.T, source pkgcache.SourceFunc[string], clock *cachetest.FakeClock) pkgcache.CacheAside[string] {
				c := pkgcache.NewLocalCache(pkgcache.LocalConfig[string]{
					TTL:     30 * time.Second,
					NullTTL: time.Minute,
					Source:  source,
					Now:     clock.Now,
				})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				return c
			})
	})

	// 读命中返回副本：LocalConfig.CloneOnRead 开启副本模式。
	t.Run("read_isolation", func(t *testing.T) {
		cachetest.RunReadIsolation(t, cachetest.Comparable[string](),
			func(t *testing.T, source pkgcache.SourceFunc[string]) pkgcache.CacheAside[string] {
				c := pkgcache.NewLocalCache(pkgcache.LocalConfig[string]{
					TTL:    5 * time.Minute,
					Source: source,
					CloneOnRead: func(v *string) *string {
						cp := *v
						return &cp
					},
				})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				return c
			})
	})

	// 读命中提升顺序：EvictLRU + MaxSize 构造（容量 3）。
	t.Run("read_promotion", func(t *testing.T) {
		cachetest.RunReadPromotion(t, cachetest.Comparable[string](), 3,
			func(t *testing.T, source pkgcache.SourceFunc[string]) pkgcache.CacheAside[string] {
				c := pkgcache.NewLocalCache(pkgcache.LocalConfig[string]{
					TTL:         5 * time.Minute,
					MaxSize:     3,
					EvictPolicy: pkgcache.EvictLRU,
					Source:      source,
				})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				return c
			})
	})

	// 不起后台 goroutine：LocalConfig.NoBackgroundSweep 关闭后台清扫协程。
	t.Run("no_background_goroutine", func(t *testing.T) {
		cachetest.RunNoBackgroundGoroutine(t,
			func(t *testing.T, source pkgcache.SourceFunc[string]) pkgcache.CacheAside[string] {
				c := pkgcache.NewLocalCache(pkgcache.LocalConfig[string]{
					TTL:               5 * time.Minute,
					Source:            source,
					NoBackgroundSweep: true,
				})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				return c
			})
	})
}
