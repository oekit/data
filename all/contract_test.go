package all

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	datacache "github.com/oekit/data/cache"
	"github.com/oekit/data/cachetest"
	redisxcache "github.com/oekit/redisx/cache"
)

// newTwoLevelForContract 用 miniredis 构造接契约套件的 TwoLevelCache：
// L1 本地（TTL 1min）+ L2 RedisCache（Source 原样注入，行为由各场景闭包控制）。
// 返回类型必须是接口：cachetest.Run 的泛型推断要求 factory 返回值精确匹配
// CacheAside[T]（具体指针类型不参与推断）。
func newTwoLevelForContract(t *testing.T, source datacache.SourceFunc[int]) datacache.CacheAside[int] {
	t.Helper()
	mr := miniredis.RunT(t)
	// miniredis 默认只在 FastForward() 手动拨钟时才让 TTL 递减（真实时间
	// 流逝不触发过期），须显式开启随真实时钟递减的 TTL 语义（redisx 同款）。
	mr.ClockTTL(true)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	local := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remote := redisxcache.NewRedisCache(client, redisxcache.RedisConfig[int]{
		Prefix: "contract:",
		TTL:    time.Minute,
		Source: source,
	})
	c := NewTwoLevelCache(local, remote)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// TestTwoLevelCache_Contract 两级组合体跑完整契约（8 场景）：
// 组合不得破坏 data/cache 门面接口的任何跨实现语义。
func TestTwoLevelCache_Contract(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), newTwoLevelForContract)
}

// TestTwoLevelCache_ContractString 换类型再跑一轮（泛型实例化不偏科）。
func TestTwoLevelCache_ContractString(t *testing.T) {
	factory := func(t *testing.T, source datacache.SourceFunc[string]) datacache.CacheAside[string] {
		t.Helper()
		mr := miniredis.RunT(t)
		mr.ClockTTL(true)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		local := datacache.NewLocalCache(datacache.LocalConfig[string]{TTL: time.Minute})
		remote := redisxcache.NewRedisCache(client, redisxcache.RedisConfig[string]{
			Prefix: "contract-str:",
			TTL:    time.Minute,
			Source: source,
		})
		c := NewTwoLevelCache(local, remote)
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	cachetest.Run(t, cachetest.Comparable[string](), factory)
}
