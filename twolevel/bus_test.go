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

// Bus 订阅生命周期（#140 重构）：每实例 acquire 独立订阅——运行中重复
// Start 幂等；Close 只关自己的订阅，Close 后再 Start 建立全新订阅（不再
// 有「单生命周期」拒绝）；共享同一 Bus 实例的两个 cache，A Close 不影响
// B 的广播链路（#140 回归钉）。
func TestBusSubscriptionLifecyclePerOwner(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)

	// 两个实例显式共享同一个 Bus 实例（#140 的合法装配）
	rdb, err := redisx.NewClientFromAddr(mr.Addr(), "", 0)
	if err != nil {
		t.Fatalf("redisx client: %v", err)
	}
	shared := twolevel.NewBus(rdb, busChannel)

	localA := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remoteA := redisxcache.NewRedisCache(redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		redisxcache.RedisConfig[int]{Prefix: "a2:", TTL: time.Minute})
	cacheA, err := twolevel.NewTwoLevelCache[int](localA, remoteA, twolevel.WithBus[int](shared))
	if err != nil {
		t.Fatalf("NewTwoLevelCache A: %v", err)
	}
	if err := cacheA.Start(ctx); err != nil {
		t.Fatalf("Start A: %v", err)
	}

	// 运行中重复 Start：幂等（不追加订阅）
	if err := cacheA.Start(ctx); err != nil {
		t.Fatalf("运行中重复 Start 应幂等: %v", err)
	}

	// B 也挂同一个共享 Bus
	cacheB, localB := newBussedCacheShared(t, mr, shared, "b2")

	// 正控制：B.Del 的广播驱逐 A 的 L1
	v := 7
	if err := localA.Set(ctx, "sk", &v); err != nil {
		t.Fatalf("A L1 Set: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "sk")
		if ok, _ := localA.Exists(context.Background(), "sk"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("正控制失败：共享 Bus 下 B.Del 未能驱逐 A 的 L1")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A Close：只关 A 自己的订阅；B 的广播链路必须继续工作（#140 回归）
	if err := cacheA.Close(ctx); err != nil {
		t.Fatalf("A Close: %v", err)
	}
	localB.Set(ctx, "postA", &v)
	deadline = time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "postA")
		if ok, _ := localB.Exists(context.Background(), "postA"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A Close 后 B 自己的订阅必须仍然工作（#140：共享 Bus 不得被任一实例 Close 杀掉）")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Close 后再 Start：建立全新订阅（新语义，不再拒绝）。用第三方发布者
	// 真实验证新订阅在工作——仅断言 Start 返回 nil 抓不住「订阅未重建」
	// 的静默退化（发布者必须是他实例：Del 会同步删发布者自身 L1，与订阅
	// 循环无关）。
	if err := cacheA.Start(ctx); err != nil {
		t.Fatalf("Close 后再 Start 应建立全新订阅: %v", err)
	}
	localA.Set(ctx, "postRestart", &v)
	deadline = time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "postRestart")
		if ok, _ := localA.Exists(context.Background(), "postRestart"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close 后再 Start 未重建订阅：重启后的广播驱逐未生效")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newBussedCacheShared：与 newBussedCache 同构，但挂调用方给的共享 Bus。
func newBussedCacheShared(t *testing.T, mr *miniredis.Miniredis, shared *twolevel.Bus, prefix string) (*twolevel.TwoLevelCache[int], *datacache.LocalCache[int]) {
	ctx := context.Background()
	goredis := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remote := redisxcache.NewRedisCache(goredis, redisxcache.RedisConfig[int]{
		Prefix: prefix + ":", TTL: time.Minute,
	})
	c, err := twolevel.NewTwoLevelCache[int](local, remote, twolevel.WithBus[int](shared))
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, local
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

// TestStartRebuildsAfterParentCtxCancel（#185）：父 ctx 取消杀死订阅后，
// 后续 Start（换活 ctx）必须能重建——修复前 Start 幂等检查读到僵尸
// busSub 永远静默 no-op，L1 失效广播永久死亡且无信号。观测口径与
// TestBusStopsOnContextCancel 相同：用第三方实例的 Del 广播探测 A 的
// 订阅循环是否在工作。
func TestStartRebuildsAfterParentCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr := miniredis.RunT(t)

	cacheA, localA, _ := newBussedCacheCtx(t, mr, "reb", ctx)
	cacheB, _, _ := newBussedCache(t, mr, "rb")

	// 正控制：取消前广播链路通——B.Del 能驱逐 A 的 L1。
	v := 9
	if err := localA.Set(ctx, "k1", &v); err != nil {
		t.Fatalf("A L1 Set: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "k1")
		if ok, _ := localA.Exists(context.Background(), "k1"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("正控制失败：B.Del 未能驱逐 A 的 L1（广播链路不通）")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 杀死 A 的订阅（父 ctx 取消，watcher release 落地）。
	cancel()
	time.Sleep(300 * time.Millisecond)

	// 重建：换活 ctx 再 Start——修复前此处静默 no-op（僵尸订阅占位）。
	if err := cacheA.Start(context.Background()); err != nil {
		t.Fatalf("僵尸订阅后 Start 应重建订阅: %v", err)
	}

	// 重建后广播链路必须恢复：B.Del 再次驱逐 A 的 L1。
	if err := localA.Set(context.Background(), "k2", &v); err != nil {
		t.Fatalf("A L1 Set: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		_ = cacheB.Del(context.Background(), "k2")
		if ok, _ := localA.Exists(context.Background(), "k2"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start 重建后广播链路未恢复（僵尸 busSub 仍占位，幂等检查 no-op）")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
