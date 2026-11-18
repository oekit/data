package rediscache_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/oekit/data/internal/testsame"
	"github.com/oekit/data/rediscache"
	pkgcache "github.com/oekit/pkg/cache"
	redisxcache "github.com/oekit/redisx/cache"
)

// TestTypeIdentity 构造（nil client 不触网）+ 双向赋值 + 接口满足。
func TestTypeIdentity(t *testing.T) {
	c := rediscache.NewRedisCache(nil, redisxcache.RedisConfig[int]{Prefix: "shell:"})

	testsame.Assert[*redisxcache.RedisCache[int]](c) // 门面 → 源

	bwd := redisxcache.NewRedisCache(nil, redisxcache.RedisConfig[int]{Prefix: "shell:"})
	testsame.Assert[*rediscache.RedisCache[int]](bwd) // 源 → 门面
	testsame.Assert[pkgcache.Cache[int]](bwd)         // 接口满足
}

// TestNewRedisCacheSmoke 构造 + 基本调用冒烟（#276）：门面泛型构造器
// NewRedisCache[T] 住 generic.go（与 shell.go 同包同一 API 面，审计只扫
// shell.go 会漏看），经 miniredis 真跑一次 Set → Get 往返，锁死「经门面
// 拿得到类型也建得出实例」。
func TestNewRedisCacheSmoke(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := rediscache.NewRedisCache[int](client, rediscache.RedisConfig[int]{Prefix: "shell:"})
	ctx := context.Background()
	want := 42
	if err := c.Set(ctx, "k", &want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || *got != want {
		t.Fatalf("Get = %v, want %d", got, want)
	}
}
