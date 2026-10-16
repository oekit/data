package cachetest

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oekit/data/cache"
)

// Contract 定义被测缓存的价值工厂与相等性。
//
// 通过 Contract 抽象，套件对任意值类型 T 复用同一组场景；
// 可比较类型可直接用 Comparable 获得现成实现。
type Contract[T any] struct {
	// NewValue 返回第 i 个测试值（i 从 0 递增）。
	// 实现必须保证任意 i != j 时两值两两不同（套件依赖此性质断言覆盖与刷新）。
	NewValue func(i int) *T

	// Equal 判断两个值是否相等。
	// 实现必须容忍 nil 入参（miss 场景会拿 nil 与期望值比较）。
	Equal func(a, b *T) bool
}

// Comparable 返回可比较类型的现成 Contract。
//
// NewValue 经反射按类型枚举生成第 i 个值：
//   - 有符号/无符号整数系：取 i 本身；
//   - 字符串系：strconv.Itoa(i)。
//
// 其他 comparable 类型（bool、struct、指针、channel 等）无法从整数可靠
// 枚举出两两不同的值，请自定义 Contract。
func Comparable[T comparable]() Contract[T] {
	return Contract[T]{
		NewValue: func(i int) *T {
			var v T
			rv := reflect.ValueOf(&v).Elem()
			switch rv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				rv.SetInt(int64(i))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				rv.SetUint(uint64(i))
			case reflect.String:
				rv.SetString(strconv.Itoa(i))
			default:
				panic(fmt.Sprintf("cachetest: Comparable 不支持类型 %s，请自定义 Contract", rv.Kind()))
			}
			return &v
		},
		Equal: func(a, b *T) bool {
			if a == nil || b == nil {
				return a == b
			}
			return *a == *b
		},
	}
}

// Run 跑完整契约场景（8 个子测试，任何子测试失败即实现违反契约）。
//
// factory 每个场景调用一次，用套件给定的 source 构造全新被测缓存
// （source 的行为——返回值/计数——由各场景闭包控制，factory 原样注入即可）；
// 被测缓存的后台资源由 factory 经 t.Cleanup 清理。
//
// 每个子测试覆盖一条必须跨实现一致的语义：
//
//  1. miss_returns_nil_nil    未命中返回 (nil, nil) 且非 error，Exists=false
//  2. set_get_roundtrip       Set/Get 往返相等；重复 Set 覆盖旧值
//  3. set_with_ttl_expiry     SetWithTTL 短 TTL 过期后 miss（有界等待）
//  4. del_and_delbatch        Del/DelBatch 后 miss；对不存在的 key 幂等
//  5. get_or_load_backfill    未命中回源回填，source 计数恰 1
//  6. null_marker             Source (nil,nil) → null 标记防穿透，Del 后重新回源
//  7. refresh                 Refresh 取 source 新值并更新缓存
//  8. concurrent_mixed_ops    并发混合读写删无 panic（配合 -race 检测数据竞争）
func Run[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()

	t.Run("miss_returns_nil_nil", func(t *testing.T) { runMiss(t, c, factory) })
	t.Run("set_get_roundtrip", func(t *testing.T) { runSetGet(t, c, factory) })
	t.Run("set_with_ttl_expiry", func(t *testing.T) { runSetWithTTL(t, c, factory) })
	t.Run("del_and_delbatch", func(t *testing.T) { runDel(t, c, factory) })
	t.Run("get_or_load_backfill", func(t *testing.T) { runGetOrLoad(t, c, factory) })
	t.Run("null_marker", func(t *testing.T) { runNullMarker(t, c, factory) })
	t.Run("refresh", func(t *testing.T) { runRefresh(t, c, factory) })
	t.Run("concurrent_mixed_ops", func(t *testing.T) { runConcurrent(t, c, factory) })
}

// unusedSource 返回"被调用即失败"的 source，用于不回源的场景。
func unusedSource[T any](t *testing.T) cache.SourceFunc[T] {
	return func(ctx context.Context, key string) (*T, error) {
		t.Errorf("该场景不应回源（key=%q）", key)
		return nil, fmt.Errorf("cachetest: unexpected source call, key=%q", key)
	}
}

// runMiss 场景 1：未命中必须返回 (nil, nil) 且非 error——miss 不是 error。
func runMiss[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	cc := factory(t, unusedSource[T](t))
	ctx := context.Background()

	got, err := cc.Get(ctx, "cachetest-miss")
	if err != nil {
		t.Fatalf("miss 应无错误（miss 不是 error），got %v", err)
	}
	if got != nil {
		t.Fatalf("miss 应返回 nil 值，got %+v", got)
	}
	exists, err := cc.Exists(ctx, "cachetest-miss")
	if err != nil {
		t.Fatalf("Exists miss 应无错误，got %v", err)
	}
	if exists {
		t.Fatal("miss 后 Exists 应为 false")
	}
}

// runSetGet 场景 2：Set/Get 往返相等；重复 Set 覆盖旧值。
func runSetGet[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	v0, v1 := c.NewValue(0), c.NewValue(1)
	if c.Equal(v0, v1) {
		t.Fatal("Contract.NewValue 违约：值必须两两不同")
	}
	cc := factory(t, unusedSource[T](t))
	ctx := context.Background()

	if err := cc.Set(ctx, "cachetest-rt", v0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := cc.Get(ctx, "cachetest-rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("Set/Get 往返失配，got %+v", got)
	}

	// 覆盖旧值
	if err := cc.Set(ctx, "cachetest-rt", v1); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	got, err = cc.Get(ctx, "cachetest-rt")
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got == nil || !c.Equal(got, v1) {
		t.Fatalf("重复 Set 应覆盖旧值，got %+v", got)
	}
}

// runSetWithTTL 场景 3：显式 TTL 生效——写入后命中，有界等待后过期 miss。
//
// TTL 50ms / 等待 120ms 量级为固定有界等待（非无界轮询）：给实现的时间
// 精度与调度延迟留 2 倍以上余量；极端 CI 负载下量级仍兜底。
func runSetWithTTL[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	cc := factory(t, unusedSource[T](t))
	ctx := context.Background()

	const ttl = 50 * time.Millisecond
	const boundedWait = 120 * time.Millisecond // 有界等待过期（约 2.4x TTL）

	if err := cc.SetWithTTL(ctx, "cachetest-ttl", v0, ttl); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	got, err := cc.Get(ctx, "cachetest-ttl")
	if err != nil {
		t.Fatalf("TTL 内 Get: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("TTL 内应命中，got %+v", got)
	}

	time.Sleep(boundedWait)

	got, err = cc.Get(ctx, "cachetest-ttl")
	if err != nil {
		t.Fatalf("过期后 Get: %v", err)
	}
	if got != nil {
		t.Fatalf("显式 TTL 过期后应 miss，got %+v", got)
	}
	if exists, e := cc.Exists(ctx, "cachetest-ttl"); e != nil || exists {
		t.Fatalf("过期后 Exists 应为 false，got exists=%v err=%v", exists, e)
	}
}

// runDel 场景 4：Del/DelBatch 后 miss；对不存在的 key 幂等（不报错）。
func runDel[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	v0, v1 := c.NewValue(0), c.NewValue(1)
	cc := factory(t, unusedSource[T](t))
	ctx := context.Background()

	if err := cc.Set(ctx, "cachetest-d1", v0); err != nil {
		t.Fatalf("Set d1: %v", err)
	}
	if err := cc.Set(ctx, "cachetest-d2", v1); err != nil {
		t.Fatalf("Set d2: %v", err)
	}

	if err := cc.Del(ctx, "cachetest-d1"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	got, err := cc.Get(ctx, "cachetest-d1")
	if err != nil || got != nil {
		t.Fatalf("Del 后应 miss，got (%+v, %v)", got, err)
	}
	if exists, e := cc.Exists(ctx, "cachetest-d1"); e != nil || exists {
		t.Fatalf("Del 后 Exists 应为 false，got exists=%v err=%v", exists, e)
	}

	if err := cc.DelBatch(ctx, "cachetest-d2", "cachetest-never-set"); err != nil {
		t.Fatalf("DelBatch: %v", err)
	}
	got, err = cc.Get(ctx, "cachetest-d2")
	if err != nil || got != nil {
		t.Fatalf("DelBatch 后应 miss，got (%+v, %v)", got, err)
	}

	// 对不存在的 key 删除不报错（幂等）
	if err := cc.Del(ctx, "cachetest-never-set"); err != nil {
		t.Fatalf("Del 不存在的 key 不应报错，got %v", err)
	}
	if err := cc.DelBatch(ctx); err != nil {
		t.Fatalf("空 DelBatch 不应报错，got %v", err)
	}
}

// runGetOrLoad 场景 5：未命中回源并回填；source 调用计数恰 1
// （第二次 GetOrLoad 命中缓存不回源）。
func runGetOrLoad[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	v0 := c.NewValue(0)
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, error) {
		calls++
		return v0, nil
	})
	ctx := context.Background()

	got, err := cc.GetOrLoad(ctx, "cachetest-load")
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("GetOrLoad 应返回 source 值，got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("source 调用计数应恰 1，got %d", calls)
	}
	if exists, e := cc.Exists(ctx, "cachetest-load"); e != nil || !exists {
		t.Fatalf("回填后 Exists 应为 true，got exists=%v err=%v", exists, e)
	}

	// 第二次命中缓存，不回源
	got, err = cc.GetOrLoad(ctx, "cachetest-load")
	if err != nil {
		t.Fatalf("GetOrLoad second: %v", err)
	}
	if got == nil || !c.Equal(got, v0) {
		t.Fatalf("第二次应命中缓存，got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("命中缓存不应回源：source 计数应仍为 1，got %d", calls)
	}
}

// runNullMarker 场景 6：Source 返回 (nil, nil) 时缓存 null 标记防穿透——
// GetOrLoad 返回 (nil, nil)、Exists=true（null 计存在）、source 计数不再
// 增长；Del 后 GetOrLoad 重新回源。
func runNullMarker[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, error) {
		calls++
		return nil, nil
	})
	ctx := context.Background()

	got, err := cc.GetOrLoad(ctx, "cachetest-null")
	if err != nil {
		t.Fatalf("null 场景 GetOrLoad 应无错误，got %v", err)
	}
	if got != nil {
		t.Fatalf("null 场景应返回 nil，got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("source 计数应恰 1，got %d", calls)
	}
	if exists, e := cc.Exists(ctx, "cachetest-null"); e != nil || !exists {
		t.Fatalf("null 标记应计存在（Exists=true），got exists=%v err=%v", exists, e)
	}

	// 命中 null 标记：不再回源（防穿透）
	got, err = cc.GetOrLoad(ctx, "cachetest-null")
	if err != nil || got != nil {
		t.Fatalf("null 标记命中应返回 (nil, nil)，got (%+v, %v)", got, err)
	}
	if calls != 1 {
		t.Fatalf("null 标记防穿透：source 计数不应增长，want 1 got %d", calls)
	}
	// Get 对 null 标记同为 (nil, nil)
	if got, e := cc.Get(ctx, "cachetest-null"); e != nil || got != nil {
		t.Fatalf("Get 对 null 标记应返回 (nil, nil)，got (%+v, %v)", got, e)
	}

	// Del 后重新回源
	if err := cc.Del(ctx, "cachetest-null"); err != nil {
		t.Fatalf("Del null key: %v", err)
	}
	if _, e := cc.GetOrLoad(ctx, "cachetest-null"); e != nil {
		t.Fatalf("Del 后 GetOrLoad: %v", e)
	}
	if calls != 2 {
		t.Fatalf("Del 后应重新回源：source 计数应为 2，got %d", calls)
	}
}

// runRefresh 场景 7：预置缓存 → source 改返回新值 → Refresh 返回新值
// 且缓存已更新为新值。
func runRefresh[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	v0, v1 := c.NewValue(0), c.NewValue(1)
	current := v0
	var calls int
	cc := factory(t, func(ctx context.Context, key string) (*T, error) {
		calls++
		return current, nil
	})
	ctx := context.Background()

	// 预置缓存（绕过 source）
	if err := cc.Set(ctx, "cachetest-refresh", v0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// source 改返回新值
	current = v1
	got, err := cc.Refresh(ctx, "cachetest-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got == nil || !c.Equal(got, v1) {
		t.Fatalf("Refresh 应返回 source 新值，got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("Refresh 应恰好回源一次，got %d", calls)
	}

	// 缓存已更新为新值
	got, err = cc.Get(ctx, "cachetest-refresh")
	if err != nil {
		t.Fatalf("Get after Refresh: %v", err)
	}
	if got == nil || !c.Equal(got, v1) {
		t.Fatalf("Refresh 后缓存应更新为新值，got %+v", got)
	}
}

// runConcurrent 场景 8：N goroutine 混合 Get/GetOrLoad/Set/Del/Exists。
// 只要求无 panic、-race 下无数据竞争，不做弱一致性断言（并发回源次数
// 取决于实现与调度，不作为契约）。
func runConcurrent[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source cache.SourceFunc[T]) cache.CacheAside[T]) {
	t.Helper()
	cc := factory(t, func(ctx context.Context, key string) (*T, error) {
		return c.NewValue(0), nil
	})
	ctx := context.Background()

	const goroutines = 16
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := fmt.Sprintf("cachetest-cc-%d", (id+i)%4)
				// goroutine 内只允许 t.Errorf（t.Fatal 必须在测程 goroutine 调用）
				switch i % 5 {
				case 0:
					if err := cc.Set(ctx, key, c.NewValue(id*opsPerGoroutine+i)); err != nil {
						t.Errorf("Set: %v", err)
					}
				case 1:
					if _, err := cc.Get(ctx, key); err != nil {
						t.Errorf("Get: %v", err)
					}
				case 2:
					if _, err := cc.GetOrLoad(ctx, key); err != nil {
						t.Errorf("GetOrLoad: %v", err)
					}
				case 3:
					if _, err := cc.Exists(ctx, key); err != nil {
						t.Errorf("Exists: %v", err)
					}
				case 4:
					if err := cc.Del(ctx, key); err != nil {
						t.Errorf("Del: %v", err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
