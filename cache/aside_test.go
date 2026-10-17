package cache_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/data/cache"
	"github.com/oekit/data/cachetest"
	pkgcache "github.com/oekit/pkg/cache"
)

// newAside 构造 LocalCache 存储 + AsideCache 装饰器（不给 Local 配 Source，
// 回源统一由装饰器持有）。
func newAside[T any](t *testing.T, source cache.SourceFuncWithTTL[T], opts ...cache.AsideOption[T]) *cache.AsideCache[T] {
	t.Helper()
	inner := cache.NewLocalCache(cache.LocalConfig[T]{TTL: 5 * time.Minute})
	t.Cleanup(func() { _ = inner.Close(context.Background()) })
	return cache.NewAsideCache(inner, source, opts...)
}

// TestAside_ImplementsCacheAside 编译期+运行期接口契约。
func TestAside_ImplementsCacheAside(t *testing.T) {
	var c cache.CacheAside[int] = newAside[int](t, cache.SourceFrom(func(ctx context.Context, key string) (*int, error) {
		v := 1
		return &v, nil
	}))
	var tc cache.TTLCacheAside[int] = newAside[int](t, cache.SourceFrom(func(ctx context.Context, key string) (*int, error) {
		v := 1
		return &v, nil
	}))
	_ = c
	_ = tc
}

// TestAside_DynamicTTL source 返回正 TTL 时按该 TTL 回填。
func TestAside_DynamicTTL(t *testing.T) {
	var calls int32
	v0 := 42
	a := newAside(t, func(ctx context.Context, key string) (*int, time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		return &v0, 50 * time.Millisecond, nil
	})
	ctx := context.Background()

	got, err := a.GetOrLoadWithTTL(ctx, "k")
	if err != nil || got == nil || *got != 42 {
		t.Fatalf("first load: got (%v, %v)", got, err)
	}
	got, err = a.GetOrLoadWithTTL(ctx, "k")
	if err != nil || got == nil || *got != 42 {
		t.Fatalf("ttl 内应命中: got (%v, %v)", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("ttl 内不应回源，calls=%d", calls)
	}

	time.Sleep(120 * time.Millisecond)
	if _, err = a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatalf("过期后回源: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("过期后应重新回源，calls=%d", calls)
	}
}

// TestAside_TTLFallback source 返回 ttl<=0 时回退默认 TTL（LocalCache 默认
// 5min，短等后仍命中）。
func TestAside_TTLFallback(t *testing.T) {
	var calls int32
	v0 := 7
	a := newAside(t, func(ctx context.Context, key string) (*int, time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		return &v0, -1, nil // 负 TTL → 回退默认
	})
	ctx := context.Background()

	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	got, err := a.GetOrLoadWithTTL(ctx, "k")
	if err != nil || got == nil || *got != 7 {
		t.Fatalf("默认 TTL 内应命中，got (%v, %v)", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("不应重复回源，calls=%d", calls)
	}
}

// TestAside_SetWithTTL 显式 SetWithTTL 仍生效，并写入 stale 影子。
func TestAside_SetWithTTL(t *testing.T) {
	v0 := 1
	a := newAside(t, func(ctx context.Context, key string) (*int, time.Duration, error) {
		return &v0, 0, nil
	})
	ctx := context.Background()

	if err := a.SetWithTTL(ctx, "k", &v0, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got, err := a.Get(ctx, "k")
	if err != nil || got == nil {
		t.Fatalf("ttl 内应命中，got (%v, %v)", got, err)
	}
	time.Sleep(90 * time.Millisecond)
	got, err = a.Get(ctx, "k")
	if err != nil || got != nil {
		t.Fatalf("过期后应 miss，got (%v, %v)", got, err)
	}
}

// TestAside_StaleIfError 动态 TTL 过期 + source 失败 → 返回陈旧值。
func TestAside_StaleIfError(t *testing.T) {
	v0 := 99
	fail := errors.New("db down")
	mode := "ok"
	var staleHits int32
	a := newAside(t,
		func(ctx context.Context, key string) (*int, time.Duration, error) {
			if mode == "fail" {
				return nil, 0, fail
			}
			return &v0, 30*time.Millisecond, nil
		},
		cache.WithStaleIfError[int](true),
		cache.WithOnStale[int](func(ctx context.Context, key string, value *int) {
			atomic.AddInt32(&staleHits, 1)
		}),
	)
	ctx := context.Background()

	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	mode = "fail"

	got, err := a.GetOrLoadWithTTL(ctx, "k")
	if err != nil {
		t.Fatalf("stale-if-error 应返回陈旧值，err=%v", err)
	}
	if got == nil || *got != 99 {
		t.Fatalf("应返回陈旧值 99，got %+v", got)
	}
	if atomic.LoadInt32(&staleHits) != 1 {
		t.Fatalf("OnStale 应恰好回调 1 次，got %d", staleHits)
	}
}

// TestAside_StaleDisabled_SourceError 默认 fail-closed：source 失败上抛。
func TestAside_StaleDisabled_SourceError(t *testing.T) {
	fail := errors.New("db down")
	a := newAside(t, func(ctx context.Context, key string) (*int, time.Duration, error) {
		return nil, 0, fail
	})
	got, err := a.GetOrLoadWithTTL(context.Background(), "k")
	if !errors.Is(err, fail) {
		t.Fatalf("默认应上抛 source 错误，got (%v, %v)", got, err)
	}
}

// TestAside_StaleDisabled_HasValue_StillError 有陈旧值但未开 stale-if-error
// → 仍上抛（开关是构造期语义，不看影子）。
func TestAside_StaleDisabled_HasValue_StillError(t *testing.T) {
	v0 := 5
	fail := errors.New("db down")
	mode := "ok"
	a := newAside(t, func(ctx context.Context, key string) (*int, time.Duration, error) {
		if mode == "fail" {
			return nil, 0, fail
		}
		return &v0, 30*time.Millisecond, nil
	})
	ctx := context.Background()
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	mode = "fail"
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); !errors.Is(err, fail) {
		t.Fatalf("未开 stale-if-error 应上抛，err=%v", err)
	}
}

// TestAside_NullMarkerNotStale Source (nil,nil) 清除 stale 影子。
func TestAside_NullMarkerNotStale(t *testing.T) {
	v0 := 3
	fail := errors.New("db down")
	mode := "ok"
	a := newAside(t,
		func(ctx context.Context, key string) (*int, time.Duration, error) {
			switch mode {
			case "ok":
				return &v0, time.Minute, nil
			case "nil":
				return nil, 0, nil
			default:
				return nil, 0, fail
			}
		},
		cache.WithStaleIfError[int](true),
	)
	ctx := context.Background()

	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	mode = "nil"
	got, err := a.Refresh(ctx, "k")
	if err != nil || got != nil {
		t.Fatalf("Refresh 到 (nil,nil)，got (%v, %v)", got, err)
	}
	// null marker 命中：Exists=true，不再回源
	exists, err := a.Exists(ctx, "k")
	if err != nil || !exists {
		t.Fatalf("null marker 应计存在，got (%v, %v)", exists, err)
	}
	// null marker 短路：不回源、不走 stale（返回 (nil,nil) 而非旧值）
	got, err = a.GetOrLoadWithTTL(ctx, "k")
	if err != nil || got != nil {
		t.Fatalf("null marker 短路应返回 (nil,nil)，got (%+v, %v)", got, err)
	}
}

// TestAside_DelClearsStale Del 清除影子，source 失败不得复活。
func TestAside_DelClearsStale(t *testing.T) {
	v0 := 8
	fail := errors.New("db down")
	mode := "ok"
	a := newAside(t,
		func(ctx context.Context, key string) (*int, time.Duration, error) {
			if mode == "fail" {
				return nil, 0, fail
			}
			return &v0, 30*time.Millisecond, nil
		},
		cache.WithStaleIfError[int](true),
	)
	ctx := context.Background()
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := a.Del(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	mode = "fail"
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); !errors.Is(err, fail) {
		t.Fatalf("Del 后不得 stale 复活，err=%v", err)
	}
}

// TestAside_StaleNoRefill stale 命中不回填：恢复后拿到新值。
func TestAside_StaleNoRefill(t *testing.T) {
	v0, v1 := 1, 2
	fail := errors.New("db down")
	mode := "ok"
	current := &v0
	a := newAside(t,
		func(ctx context.Context, key string) (*int, time.Duration, error) {
			if mode == "fail" {
				return nil, 0, fail
			}
			return current, 30*time.Millisecond, nil
		},
		cache.WithStaleIfError[int](true),
	)
	ctx := context.Background()
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	mode = "fail"
	if _, err := a.GetOrLoadWithTTL(ctx, "k"); err != nil {
		t.Fatalf("stale 命中: %v", err)
	}
	mode, current = "ok", &v1
	got, err := a.GetOrLoadWithTTL(ctx, "k")
	if err != nil || got == nil || *got != 2 {
		t.Fatalf("恢复后应拿到新值 2（stale 不回填），got (%v, %v)", got, err)
	}
}

// TestAside_NullTTLDisabled WithAsideNullTTL 负值禁用空值缓存。
func TestAside_NullTTLDisabled(t *testing.T) {
	var calls int32
	a := newAside(t,
		func(ctx context.Context, key string) (*int, time.Duration, error) {
			atomic.AddInt32(&calls, 1)
			return nil, 0, nil
		},
		cache.WithAsideNullTTL[int](-1),
	)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		got, err := a.GetOrLoadWithTTL(ctx, "missing")
		if err != nil || got != nil {
			t.Fatalf("应返回 (nil,nil)，got (%v, %v)", got, err)
		}
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("禁用空值缓存应每次回源，calls=%d", calls)
	}
}

// TestAside_ContractSuite 装饰器跑通 pkg/cache cachetest 8 场景契约
// （SourceFunc 经 SourceFrom 适配）。
func TestAside_ContractSuite(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), func(t *testing.T, source pkgcache.SourceFunc[int]) pkgcache.CacheAside[int] {
		inner := cache.NewLocalCache(cache.LocalConfig[int]{TTL: 5 * time.Minute})
		t.Cleanup(func() { _ = inner.Close(context.Background()) })
		return cache.NewAsideCache(inner, cache.SourceFrom(source))
	})
}

// TestAside_RunTTL 扩展契约：动态 TTL 三场景。
func TestAside_RunTTL(t *testing.T) {
	cachetest.RunTTL(t, cachetest.Comparable[int](), func(t *testing.T, source cache.SourceFuncWithTTL[int]) cache.TTLCacheAside[int] {
		return newAside(t, source)
	})
}

// TestAside_RunStaleIfError 扩展契约：stale-if-error 五场景。
func TestAside_RunStaleIfError(t *testing.T) {
	cachetest.RunStaleIfError(t, cachetest.Comparable[int](), func(t *testing.T, source cache.SourceFuncWithTTL[int]) cache.TTLCacheAside[int] {
		return newAside(t, source, cache.WithStaleIfError[int](true))
	})
}
