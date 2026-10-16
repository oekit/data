package all

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	datacache "github.com/oekit/data/cache"
	redisxcache "github.com/oekit/redisx/cache"
)

// twoLevelFixture 两级缓存测试夹具：暴露 miniredis 以便直接操纵/检查 L2 存储。
type twoLevelFixture struct {
	cache *TwoLevelCache[int]
	mr    *miniredis.Miniredis
	local *datacache.LocalCache[int]
}

func newFixture(t *testing.T, source datacache.SourceFunc[int]) *twoLevelFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	// 真实时钟 TTL 递减语义（miniredis 默认只在 FastForward() 拨钟时过期）
	mr.ClockTTL(true)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute})
	remote := redisxcache.NewRedisCache(client, redisxcache.RedisConfig[int]{
		Prefix: "tl:",
		TTL:    time.Minute,
		Source: source,
	})
	c := NewTwoLevelCache(local, remote)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return &twoLevelFixture{cache: c, mr: mr, local: local}
}

func (f *twoLevelFixture) redisKey(key string) string { return "tl:" + key }

// TestTwoLevel_GetOrLoadBackfillsLocal 回源真值必须回填 L1，且 L1 命中后
// 不再打 L2（L2 数据被绕过仍能读到——两级缓存的收益所在）。
func TestTwoLevel_GetOrLoadBackfillsLocal(t *testing.T) {
	var calls int
	f := newFixture(t, func(ctx context.Context, key string) (*int, error) {
		calls++
		v := 42
		return &v, nil
	})
	ctx := context.Background()

	got, err := f.cache.GetOrLoad(ctx, "k")
	if err != nil || got == nil || *got != 42 {
		t.Fatalf("GetOrLoad: got (%v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("source 应恰好调用一次，got %d", calls)
	}
	if ok, e := f.local.Exists(ctx, "k"); e != nil || !ok {
		t.Fatalf("回填后 L1 应存在，exists=%v err=%v", ok, e)
	}

	// 绕过 TwoLevelCache 直接删 L2 存储：Get 仍应经 L1 命中
	f.mr.Del(f.redisKey("k"))
	got, err = f.cache.Get(ctx, "k")
	if err != nil || got == nil || *got != 42 {
		t.Fatalf("L2 已删后 Get 应命中 L1，got (%v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("L1 命中不应回源，source 计数 got %d", calls)
	}
}

// TestTwoLevel_SetWritesThrough 写穿：Set 后两级都可见。
func TestTwoLevel_SetWritesThrough(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	v := 7

	if err := f.cache.Set(ctx, "wt", &v); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if ok, _ := f.local.Exists(ctx, "wt"); !ok {
		t.Fatal("Set 后 L1 应存在")
	}
	if !f.mr.Exists(f.redisKey("wt")) {
		t.Fatal("Set 后 L2 应存在")
	}
}

// TestTwoLevel_DelDeletesBothLevels 双删：Set 后 Del，两级都不可见。
func TestTwoLevel_DelDeletesBothLevels(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	v := 7
	if err := f.cache.Set(ctx, "dd", &v); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := f.cache.Del(ctx, "dd"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if ok, _ := f.local.Exists(ctx, "dd"); ok {
		t.Fatal("Del 后 L1 不应存在")
	}
	if f.mr.Exists(f.redisKey("dd")) {
		t.Fatal("Del 后 L2 不应存在")
	}
}

// TestTwoLevel_SetWithTTLPropagates 显式 TTL 必须同时传导到两级
// （L1 若长驻会吞掉显式短 TTL 的过期语义）。短 TTL + 有界等待断言两级
// 同步过期；miniredis 侧另验 TTL 数值已落位。
func TestTwoLevel_SetWithTTLPropagates(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	v := 7

	const ttl = 50 * time.Millisecond
	const boundedWait = 120 * time.Millisecond

	if err := f.cache.SetWithTTL(ctx, "ttl", &v, ttl); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	if got := f.mr.TTL(f.redisKey("ttl")); got <= 0 || got > ttl {
		t.Fatalf("L2 TTL 应落在 (0, %v]，got %v", ttl, got)
	}

	time.Sleep(boundedWait)

	got, err := f.cache.Get(ctx, "ttl")
	if err != nil || got != nil {
		t.Fatalf("两级过期后 Get 应 miss，got (%v, %v)", got, err)
	}
	if ok, e := f.local.Exists(ctx, "ttl"); e != nil || ok {
		t.Fatalf("过期后 L1 Exists 应为 false，got exists=%v err=%v", ok, e)
	}
}

// TestTwoLevel_NullDelegatedToRemote null 防穿透由 L2 marker 承担：
// Source (nil,nil) 后 L1 不留条目（下次 miss 会打一跳 L2 被 marker 挡住），
// source 计数不增长。
func TestTwoLevel_NullDelegatedToRemote(t *testing.T) {
	var calls int
	f := newFixture(t, func(ctx context.Context, key string) (*int, error) {
		calls++
		return nil, nil
	})
	ctx := context.Background()

	got, err := f.cache.GetOrLoad(ctx, "nk")
	if err != nil || got != nil {
		t.Fatalf("null 场景应返回 (nil, nil)，got (%v, %v)", got, err)
	}
	if ok, _ := f.local.Exists(ctx, "nk"); ok {
		t.Fatal("L1 不应缓存 null（marker 委托 L2）")
	}
	if !f.mr.Exists(f.redisKey("nk")) {
		t.Fatal("L2 应有 null marker")
	}

	// 再次 GetOrLoad：L1 miss → L2 marker 命中，不回源
	if got, err = f.cache.GetOrLoad(ctx, "nk"); err != nil || got != nil {
		t.Fatalf("marker 命中应返回 (nil, nil)，got (%v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("marker 防穿透：source 计数应保持 1，got %d", calls)
	}
}

// TestTwoLevel_LocalMissFallsThroughToRemote L2 真值在 L1 miss 时可读并回填。
func TestTwoLevel_LocalMissFallsThroughToRemote(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	v := 9
	if err := f.cache.Set(ctx, "fb", &v); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// 只清 L1，模拟本地淘汰
	if err := f.local.Del(ctx, "fb"); err != nil {
		t.Fatalf("local del: %v", err)
	}

	got, err := f.cache.Get(ctx, "fb")
	if err != nil || got == nil || *got != 9 {
		t.Fatalf("L1 miss 应落穿 L2，got (%v, %v)", got, err)
	}
	if ok, _ := f.local.Exists(ctx, "fb"); !ok {
		t.Fatal("落穿命中后应回填 L1")
	}
}

// TestTwoLevel_NewNilFailFast 构造期 nil fail-fast。
func TestTwoLevel_NewNilFailFast(t *testing.T) {
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{})
	remote := redisxcache.NewRedisCache[int](nil, redisxcache.RedisConfig[int]{})

	if p := recoverNil(t, func() { NewTwoLevelCache[int](nil, remote) }); !p {
		t.Fatal("local=nil 应 panic")
	}
	if p := recoverNil(t, func() { NewTwoLevelCache[int](local, nil) }); !p {
		t.Fatal("remote=nil 应 panic")
	}
}

func recoverNil(t *testing.T, fn func()) (panicked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

// TestTwoLevel_CloseIdempotent Close 幂等（透传 L1，Once 语义）。
func TestTwoLevel_CloseIdempotent(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	if err := f.cache.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.cache.Close(ctx); err != nil {
		t.Fatalf("重复 Close 不应报错，got %v", err)
	}
}

// TestTwoLevel_DelBatchBothLevels 批量双删 + 空 keys 幂等。
func TestTwoLevel_DelBatchBothLevels(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	v0, v1 := 1, 2
	if err := f.cache.Set(ctx, "b0", &v0); err != nil {
		t.Fatalf("Set b0: %v", err)
	}
	if err := f.cache.Set(ctx, "b1", &v1); err != nil {
		t.Fatalf("Set b1: %v", err)
	}

	if err := f.cache.DelBatch(ctx, "b0", "b1", "b-never"); err != nil {
		t.Fatalf("DelBatch: %v", err)
	}
	if ok, _ := f.local.Exists(ctx, "b0"); ok {
		t.Fatal("DelBatch 后 L1 b0 不应存在")
	}
	if f.mr.Exists(f.redisKey("b1")) {
		t.Fatal("DelBatch 后 L2 b1 不应存在")
	}
	if err := f.cache.DelBatch(ctx); err != nil {
		t.Fatalf("空 DelBatch 不应报错，got %v", err)
	}
}

// TestTwoLevel_RemoteErrorPropagates L2 错误向上透传（不吞）。
func TestTwoLevel_RemoteErrorPropagates(t *testing.T) {
	// 指向不存在的 redis 地址：Get 应返回网络错误而非 (nil, nil)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if err := client.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}
	local := datacache.NewLocalCache(datacache.LocalConfig[int]{})
	remote := redisxcache.NewRedisCache(client, redisxcache.RedisConfig[int]{Prefix: "e:"})
	c := NewTwoLevelCache(local, remote)
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	if _, err := c.Get(context.Background(), "x"); err == nil {
		t.Fatal("L2 不可达时 Get 应返回错误")
	}
	if _, err := c.GetOrLoad(context.Background(), "x"); err == nil {
		t.Fatal("L2 不可达时 GetOrLoad 应返回错误")
	}
	// Del 的 L2 错误经 errors.Join 仍可见
	if err := c.Del(context.Background(), "x"); err == nil {
		t.Fatal("L2 不可达时 Del 应返回错误")
	}
}
