package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- M7: LocalCache 空值缓存 ---
//
// null marker 的通用语义（Source (nil,nil) → 缓存 null 标记、Exists=true、
// 防穿透、Del 后重新回源）已由 cachetest 契约套件场景 null_marker 固化
//（见 cache_test.go TestContract_*），本文件只保留 LocalCache 特有的
// NullTTL 配置行为与并发覆盖。

// TestLocalCache_NullTTL_Disabled 回归测试（M7）：NullTTL=-1 显式禁用空值缓存，
// 每次 GetOrLoad 都重新调用 Source。
func TestLocalCache_NullTTL_Disabled(t *testing.T) {
	var sourceCalls int
	c := NewLocalCache[testUser](LocalConfig[testUser]{
		Source: func(ctx context.Context, key string) (*testUser, error) {
			sourceCalls++
			return nil, nil
		},
		NullTTL: -1, // 显式禁用
	})
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	for i := 0; i < 2; i++ {
		if _, err := c.GetOrLoad(ctx, "missing"); err != nil {
			t.Fatal(err)
		}
	}
	if sourceCalls != 2 {
		t.Errorf("expected 2 Source calls (null caching disabled), got %d", sourceCalls)
	}
}

// TestLocalCache_NullMarker_Expiry 验证 null marker 到期后重新调用 Source。
func TestLocalCache_NullMarker_Expiry(t *testing.T) {
	var sourceCalls int
	c := NewLocalCache[testUser](LocalConfig[testUser]{
		Source: func(ctx context.Context, key string) (*testUser, error) {
			sourceCalls++
			return nil, nil
		},
		NullTTL: 30 * time.Millisecond,
	})
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	_, _ = c.GetOrLoad(ctx, "missing")
	if sourceCalls != 1 {
		t.Fatalf("expected 1 Source call, got %d", sourceCalls)
	}
	_, _ = c.GetOrLoad(ctx, "missing")
	if sourceCalls != 1 {
		t.Fatalf("expected marker hit (still 1 Source call), got %d", sourceCalls)
	}

	time.Sleep(50 * time.Millisecond) // 等 null marker 过期

	_, _ = c.GetOrLoad(ctx, "missing")
	if sourceCalls != 2 {
		t.Errorf("expected marker expired and Source re-called (2), got %d", sourceCalls)
	}
}

// TestLocalCache_NullMarker_ConcurrentGetOrLoad 验证 null marker 在并发
// GetOrLoad 下无数据竞争（-race 覆盖）。
func TestLocalCache_NullMarker_ConcurrentGetOrLoad(t *testing.T) {
	c := NewLocalCache[int](LocalConfig[int]{
		Source: func(ctx context.Context, key string) (*int, error) {
			return nil, nil
		},
	})
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, _ = c.GetOrLoad(ctx, "null-key")
				_, _ = c.Get(ctx, "null-key")
				_ = c.SetWithTTL(ctx, "null-key", nil, time.Minute) // 与 null marker 并发写
			}
		}()
	}
	wg.Wait()
}
