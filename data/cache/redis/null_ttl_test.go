package redis

import (
	"context"
	"testing"
)

// --- M7: NullTTL 语义翻转 + 空值缓存防穿透功能 ---

// TestRedisCache_GetOrLoad_NullTTL_Default_CachesNullMarker 回归测试（M7）：
// NullTTL 未设置（0）时必须**开启**空值缓存（默认 1 分钟）——
// 此前 0 会静默禁用 null marker，防穿透默认失效。
func TestRedisCache_GetOrLoad_NullTTL_Default_CachesNullMarker(t *testing.T) {
	client, _ := newTestRedisClient(t)
	var sourceCalls int
	c := NewRedisCache[testUser](client, RedisConfig[testUser]{
		Prefix: "x:",
		Source: func(ctx context.Context, key string) (*testUser, error) {
			sourceCalls++
			return nil, nil
		},
		// NullTTL 未设置（零值）
	})
	ctx := context.Background()

	// 第一次：未命中 → Source → 写入 null marker
	if _, err := c.GetOrLoad(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if sourceCalls != 1 {
		t.Fatalf("expected 1 Source call, got %d", sourceCalls)
	}

	// 第二次：命中 null marker → 不再调 Source（防穿透生效）
	if _, err := c.GetOrLoad(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if sourceCalls != 1 {
		t.Errorf("expected still 1 Source call (null marker cached by default), got %d", sourceCalls)
	}
}

// TestRedisCache_GetOrLoad_NullTTL_Disabled 回归测试（M7）：NullTTL=-1 显式
// 禁用空值缓存 → 每次 GetOrLoad 都会重新调用 Source。
func TestRedisCache_GetOrLoad_NullTTL_Disabled(t *testing.T) {
	client, _ := newTestRedisClient(t)
	var sourceCalls int
	c := NewRedisCache[testUser](client, RedisConfig[testUser]{
		Prefix: "x:",
		Source: func(ctx context.Context, key string) (*testUser, error) {
			sourceCalls++
			return nil, nil
		},
		NullTTL: -1, // 显式禁用
	})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := c.GetOrLoad(ctx, "missing"); err != nil {
			t.Fatal(err)
		}
	}
	if sourceCalls != 2 {
		t.Errorf("expected 2 Source calls (null caching disabled), got %d", sourceCalls)
	}
}
