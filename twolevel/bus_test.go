package twolevel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	datacache "github.com/oekit/data/cache"
	"github.com/oekit/data/twolevel"
	redisx "github.com/oekit/redisx"
	redisxcache "github.com/oekit/redisx/cache"
)

const busChannel = "test:evict"

// newBussedCache 组装一个挂了失效广播的两级缓存实例（miniredis 后端）。
func newBussedCache(t *testing.T, mr *miniredis.Miniredis, prefix string) (*twolevel.TwoLevelCache[int], *datacache.LocalCache[int], *redisx.Client) {
	return newBussedCacheCtx(t, mr, prefix, context.Background())
}

func newBussedCacheCtx(t *testing.T, mr *miniredis.Miniredis, prefix string, startCtx context.Context) (*twolevel.TwoLevelCache[int], *datacache.LocalCache[int], *redisx.Client) {
	t.Helper()
	mr.ClockTTL(true)

	goredis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remote := redisxcache.NewRedisCache(goredis, redisxcache.RedisConfig[int]{
		Prefix: prefix + ":", TTL: time.Minute,
	})
	rdb, err := redisx.NewClientFromAddr(mr.Addr(), "", 0)
	if err != nil {
		t.Fatalf("redisx client: %v", err)
	}
	c, err := twolevel.NewTwoLevelCache[int](local, remote,
		twolevel.WithBus[int](twolevel.NewBus(rdb, busChannel)))
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if err := c.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, local, rdb
}

// 跨实例失效广播：A 删除 key（发布），B 的订阅循环驱逐自己的 L1 副本。
func TestBusCrossInstanceEviction(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)

	cacheA, _, _ := newBussedCache(t, mr, "a")
	_, localB, _ := newBussedCache(t, mr, "b")

	// B 的 L1 预置副本；A 的 L1 也预置（自驱逐即时生效，不依赖广播）
	v := 5
	if err := localB.Set(ctx, "shared", &v); err != nil {
		t.Fatalf("B L1 Set: %v", err)
	}

	// A.Del 发布失效消息；B 订阅就绪存在异步窗口，轮询等待驱逐落地
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := cacheA.Del(ctx, "shared"); err != nil {
			t.Fatalf("A Del: %v", err)
		}
		if ok, _ := localB.Exists(ctx, "shared"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("3s 内 B 的 L1 未被广播驱逐")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// 坏消息容忍：频道里的非法 payload 不炸订阅循环，后续正常消息照常处理。
func TestBusToleratesMalformedMessage(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)

	cacheA, localA, rdb := newBussedCache(t, mr, "a")

	// 先注入一条坏消息
	if err := rdb.Publish(ctx, busChannel, "not-json-at-all").Err(); err != nil {
		t.Fatalf("publish garbage: %v", err)
	}

	// 随后正常广播仍须生效（循环未被坏消息打死）
	v := 9
	if err := localA.Set(ctx, "k", &v); err != nil {
		t.Fatalf("L1 Set: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := cacheA.Del(ctx, "k"); err != nil {
			t.Fatalf("Del: %v", err)
		}
		if ok, _ := localA.Exists(ctx, "k"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("坏消息后订阅循环失效：合法消息未触发驱逐")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Close 幂等且停掉订阅（重复 Close 不 panic）。
func TestBusCloseIdempotent(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	cacheA, _, _ := newBussedCache(t, mr, "a")

	if err := cacheA.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := cacheA.Close(ctx); err != nil {
		t.Fatalf("second Close（必须幂等）: %v", err)
	}
}

// 无 bus 的实例 Del 不广播也能正常两级删除（广播是可选件）。
func TestDelWithoutBusStillWorks(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mr.ClockTTL(true)

	goredis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remote := redisxcache.NewRedisCache(goredis, redisxcache.RedisConfig[int]{
		Prefix: "nobus:", TTL: time.Minute,
	})
	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}

	v := 1
	if err := c.Set(ctx, "k", &v); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if got, _ := c.Get(ctx, "k"); got != nil {
		t.Fatalf("Del 后仍读到 %v", *got)
	}
	if ok, _ := local.Exists(ctx, "k"); ok {
		t.Fatal("L1 未被删除")
	}
}

// ctx 取消必须真正停止订阅循环（go-redis Subscribe(ctx) 只约束订阅命令，
// 停止靠 Bus 内部 watcher）。观测方式：取消前 B.Del 能驱逐 A 的 L1（正
// 控制）；取消后 B.Del 不再驱逐 A 的 L1（负断言——Del 同步删发布者自身
// 本地与远端，与订阅循环无关，故必须用第三方发布来观测 A 的循环）。
func TestBusStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr := miniredis.RunT(t)

	cacheA, localA, _ := newBussedCacheCtx(t, mr, "a", ctx)
	var cacheB *twolevel.TwoLevelCache[int]
	{
		goredis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		localB := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
		remoteB := redisxcache.NewRedisCache(goredis, redisxcache.RedisConfig[int]{
			Prefix: "b:", TTL: time.Minute,
		})
		rdb, err := redisx.NewClientFromAddr(mr.Addr(), "", 0)
		if err != nil {
			t.Fatalf("redisx client: %v", err)
		}
		cacheB, err = twolevel.NewTwoLevelCache[int](localB, remoteB,
			twolevel.WithBus[int](twolevel.NewBus(rdb, busChannel)))
		if err != nil {
			t.Fatalf("NewTwoLevelCache B: %v", err)
		}
		if err := cacheB.Start(context.Background()); err != nil {
			t.Fatalf("Start B: %v", err)
		}
		t.Cleanup(func() { _ = cacheB.Close(context.Background()) })
	}

	// 正控制：取消前，B.Del 的广播能驱逐 A 的 L1
	v := 3
	if err := localA.Set(ctx, "k", &v); err != nil {
		t.Fatalf("A L1 Set: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "k")
		if ok, _ := localA.Exists(context.Background(), "k"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("正控制失败：B.Del 未能驱逐 A 的 L1（广播链路不通）")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 取消 A 的 ctx 并给 watcher 落地时间
	cancel()
	time.Sleep(300 * time.Millisecond)

	// 负断言：A 循环已死，B.Del 的广播不再驱逐 A 的 L1
	if err := localA.Set(ctx, "k", &v); err != nil {
		t.Fatalf("A L1 Set: %v", err)
	}
	_ = cacheB.Del(context.Background(), "k")
	time.Sleep(400 * time.Millisecond)
	if ok, _ := localA.Exists(context.Background(), "k"); !ok {
		t.Fatal("ctx 取消后 A 的订阅循环应已停止，L1 不应再被广播驱逐")
	}
	_ = cacheA.Close(context.Background())
}
