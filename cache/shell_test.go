package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/data/cache"
	"github.com/oekit/data/internal/testsame"
	pkgcache "github.com/oekit/pkg/cache"
)

// TestTypeIdentity 门面 alias 与源包类型完全同一（双向赋值，消费方视角）。
func TestTypeIdentity(t *testing.T) {
	fwd := pkgcache.NewLocalCache(pkgcache.LocalConfig[int]{})
	testsame.Assert[*cache.LocalCache[int]](fwd) // 源 → 门面

	bwd := cache.NewLocalCache(cache.LocalConfig[int]{})
	testsame.Assert[*pkgcache.LocalCache[int]](bwd) // 门面 → 源
	testsame.Assert[pkgcache.CacheAside[int]](bwd)  // 接口同一
	testsame.Assert[pkgcache.BatchGetter[int]](bwd) // 批量能力接口同一
}

// TestOptionalCapabilityIdentity pkg v0.16.0 新增的可选能力类型：门面 alias
// 与源包类型完全同一（双向赋值，LocalCache 同时实现 Sweeper 与 HitGetter）。
func TestOptionalCapabilityIdentity(t *testing.T) {
	fwd := pkgcache.NewLocalCache(pkgcache.LocalConfig[int]{})
	defer func() { _ = fwd.Close(context.Background()) }()

	// 源包接口 → 门面接口。
	var swSrc pkgcache.Sweeper = fwd
	testsame.Assert[cache.Sweeper](swSrc)
	// 门面接口 → 源包接口。
	var swShell cache.Sweeper = fwd
	testsame.Assert[pkgcache.Sweeper](swShell)

	var hgSrc pkgcache.HitGetter[int] = fwd
	testsame.Assert[cache.HitGetter[int]](hgSrc)
	var hgShell cache.HitGetter[int] = fwd
	testsame.Assert[pkgcache.HitGetter[int]](hgShell)

	// EvictPolicy 非泛型：两个方向的赋值都成立即同一类型。
	testsame.Assert[cache.EvictPolicy](pkgcache.EvictPolicy(0))
	testsame.Assert[pkgcache.EvictPolicy](cache.EvictPolicy(0))
}

// TestGetOrLoadSmoke 门面入口行为冒烟。
func TestGetOrLoadSmoke(t *testing.T) {
	c := cache.NewLocalCache(cache.LocalConfig[int]{
		TTL: time.Minute,
		Source: func(ctx context.Context, key string) (*int, error) {
			v := 42
			return &v, nil
		},
	})
	defer func() { _ = c.Close(context.Background()) }()

	got, err := c.GetOrLoad(context.Background(), "k")
	if err != nil || got == nil || *got != 42 {
		t.Fatalf("GetOrLoad: got (%v, %v)", got, err)
	}
}
