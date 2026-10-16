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
