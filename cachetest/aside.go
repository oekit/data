package cachetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oekit/data/cache"
)

// RunTTL 跑「回源动态 TTL」契约场景。factory 每个场景调用一次，用套件给定
// 的 source 构造全新被测缓存（source 可能返回任意 ttl——包括 <=0，factory
// 原样注入即可）；被测缓存的后台资源由 factory 经 t.Cleanup 清理。
//
// 每个子测试覆盖一条必须跨实现一致的语义：
//
//  1. source_ttl_honored     source 返回正 TTL → 到期后 miss（有界等待）
//  2. source_ttl_fallback    source 返回 ttl<=0 → 回退默认 TTL（回填后仍命中）
//  3. get_or_load_with_ttl   GetOrLoadWithTTL 与 GetOrLoad 同语义（回源计数恰 1）
func RunTTL[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	t.Run("source_ttl_honored", func(t *testing.T) { runTTLHonored(t, c, factory) })
	t.Run("source_ttl_fallback", func(t *testing.T) { runTTLFallback(t, c, factory) })
	t.Run("get_or_load_with_ttl", func(t *testing.T) { runGetOrLoadWithTTL(t, c, factory) })
}

// RunStaleIfError 跑「fail-open stale-if-error」契约场景。
//
// **factory 必须以 WithStaleIfError(true) 构造被测缓存**，否则场景必然失败
// （stale-if-error 是构造期开关，套件不探测可选能力）。
//
// 每个子测试覆盖一条必须跨实现一致的语义：
//
//  1. stale_serve_on_error     成功加载后 source 失败 → 返回陈旧值
//  2. no_stale_propagates      无陈旧值时 source 失败 → 上抛错误
//  3. null_marker_not_stale    Source 成功 (nil,nil) 后 marker 短路 (nil,nil)，
//                             不回 stale 旧值（marker 不参与 stale）
//  4. del_clears_stale         Del 后 source 失败 → 上抛（写后失效不得复活）
//  5. stale_no_refill          stale 命中不回填缓存（下一跳仍回源）
func RunStaleIfError[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	t.Run("stale_serve_on_error", func(t *testing.T) { runStaleServe(t, c, factory) })
	t.Run("no_stale_propagates", func(t *testing.T) { runStaleNoValue(t, c, factory) })
	t.Run("null_marker_not_stale", func(t *testing.T) { runStaleNullMarker(t, c, factory) })
	t.Run("del_clears_stale", func(t *testing.T) { runStaleAfterDel(t, c, factory) })
	t.Run("stale_no_refill", func(t *testing.T) { runStaleNoRefill(t, c, factory) })
}

// runTTLHonored 场景 1：source 返回正 TTL → 回填后命中，有界等待后过期 miss。
func runTTLHonored[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		return v0, 50 * time.Millisecond, nil
	})
	ctx := context.Background()

	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-honor")
	if err != nil {
		t.Fatalf("GetOrLoadWithTTL: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("应回源返回值，got %+v", got)
	}

	got, err = cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-honor")
	if err != nil || got == nil || !c.Equal(got, v0) {
		t.Fatalf("TTL 内应命中，got (%+v, %v)", got, err)
	}

	time.Sleep(120 * time.Millisecond) // 有界等待（约 2.4x TTL）

	got, err = cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-honor")
	if err != nil {
		t.Fatalf("过期后 GetOrLoadWithTTL: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("过期后应重新回源（source 恒返回 v0），got %+v", got)
	}
}

// runTTLFallback 场景 2：source 返回 ttl<=0 → 回退默认 TTL（回填后仍命中，
// 不会立刻过期）。
func runTTLFallback[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		calls++
		return v0, 0, nil // ttl=0 → 默认 TTL
	})
	ctx := context.Background()

	if _, err := cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-fallback"); err != nil {
		t.Fatalf("GetOrLoadWithTTL: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-fallback")
	if err != nil || got == nil || !c.Equal(got, v0) {
		t.Fatalf("默认 TTL 内应命中，got (%+v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("source 调用计数应恰 1，got %d", calls)
	}
}

// runGetOrLoadWithTTL 场景 3：GetOrLoadWithTTL 与 GetOrLoad 同语义——
// 回源计数恰 1，第二次命中缓存不回源。
func runGetOrLoadWithTTL[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		calls++
		return v0, time.Minute, nil
	})
	ctx := context.Background()

	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-ttl-gol")
	if err != nil || got == nil || !c.Equal(got, v0) {
		t.Fatalf("GetOrLoadWithTTL: got (%+v, %v)", got, err)
	}
	got, err = cc.GetOrLoad(ctx, "cachetest-ttl-gol")
	if err != nil || got == nil || !c.Equal(got, v0) {
		t.Fatalf("GetOrLoad 应命中同一缓存项，got (%+v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("source 调用计数应恰 1，got %d", calls)
	}
}

// runStaleServe 场景 1：成功加载后 source 转失败 → 返回陈旧真值。
func runStaleServe[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	fail := errors.New("cachetest: source down")
	mode := "ok"
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		if mode == "fail" {
			return nil, 0, fail
		}
		return v0, 30 * time.Millisecond, nil // 短 TTL 便于制造过期
	})
	ctx := context.Background()

	if _, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale"); err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // 等待缓存过期
	mode = "fail"

	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale")
	if err != nil {
		t.Fatalf("stale-if-error 应返回陈旧值而非上抛，got err %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("应返回陈旧真值，got %+v", got)
	}
}

// runStaleNoValue 场景 2：无陈旧值时 source 失败 → 上抛错误。
func runStaleNoValue[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	_ = c
	fail := errors.New("cachetest: source down")
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		return nil, 0, fail
	})

	got, err := cc.GetOrLoadWithTTL(context.Background(), "cachetest-stale-none")
	if !errors.Is(err, fail) {
		t.Fatalf("无陈旧值应上抛 source 错误，got (%+v, %v)", got, err)
	}
	if got != nil {
		t.Fatalf("上抛时值必须无效，got %+v", got)
	}
}

// runStaleNullMarker 场景 3：Source 成功返回 (nil, nil) 写入 null marker 并
// 清除 stale 形子——marker 命中短路返回 (nil, nil)，**不**回 stale 旧值
// （marker 表示「已知不存在」，不参与 stale 兜底）。
func runStaleNullMarker[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	mode := "ok"
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		calls++
		if mode == "nil" {
			return nil, 0, nil // 数据被删
		}
		return v0, time.Minute, nil
	})
	ctx := context.Background()

	if _, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-null"); err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	mode = "nil"
	// 需要越过可能的缓存命中：显式 Refresh 走回源
	if got, err := cc.Refresh(ctx, "cachetest-stale-null"); err != nil || got != nil {
		t.Fatalf("Refresh 到 (nil,nil) 应成功返回 nil，got (%+v, %v)", got, err)
	}

	// null marker 命中：短路 (nil, nil)，不回源、不回 stale 旧值
	before := calls
	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-null")
	if err != nil || got != nil {
		t.Fatalf("null marker 命中应返回 (nil,nil) 而非 stale 旧值，got (%+v, %v)", got, err)
	}
	if calls != before {
		t.Fatalf("null marker 防穿透：source 计数不应增长，before=%d after=%d", before, calls)
	}
}

// runStaleAfterDel 场景 4：Del 清除 stale 影子——source 失败不得复活已删值。
func runStaleAfterDel[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	fail := errors.New("cachetest: source down")
	mode := "ok"
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		if mode == "fail" {
			return nil, 0, fail
		}
		return v0, time.Minute, nil
	})
	ctx := context.Background()

	if _, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-del"); err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	if err := cc.Del(ctx, "cachetest-stale-del"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	mode = "fail"

	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-del")
	if !errors.Is(err, fail) {
		t.Fatalf("Del 后 source 失败应上抛（写后失效不得被 stale 复活），got (%+v, %v)", got, err)
	}
}

// runStaleNoRefill 场景 5：stale 命中不回填缓存——恢复后下一跳重新回源
// （陈旧窗口不因读而延长）。
func runStaleNoRefill[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFuncWithTTL[T]) cache.TTLCacheAside[T]) {
	t.Helper()
	v0, v1 := c.NewValue(0), c.NewValue(1)
	fail := errors.New("cachetest: source down")
	mode := "ok"
	current := v0
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, time.Duration, error) {
		calls++
		if mode == "fail" {
			return nil, 0, fail
		}
		return current, 30 * time.Millisecond, nil
	})
	ctx := context.Background()

	if _, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-refill"); err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	firstCalls := calls

	time.Sleep(80 * time.Millisecond) // 过期
	mode = "fail"
	got, err := cc.GetOrLoadWithTTL(ctx, "cachetest-stale-refill")
	if err != nil || got == nil || !c.Equal(got, v0) {
		t.Fatalf("stale 命中应返回旧值，got (%+v, %v)", got, err)
	}

	// 恢复 source 并给新值：若 stale 回填了缓存，这里会拿到 v0
	mode, current = "ok", v1
	got, err = cc.GetOrLoadWithTTL(ctx, "cachetest-stale-refill")
	if err != nil {
		t.Fatalf("恢复后 GetOrLoadWithTTL: %v", err)
	}
	if got == nil || !c.Equal(got, v1) {
		t.Fatalf("stale 命中不得回填缓存：恢复后应重新回源拿到新值，got %+v", got)
	}
	if calls <= firstCalls {
		t.Fatalf("恢复后应重新回源：call 数应增长，before=%d after=%d", firstCalls, calls)
	}
}

