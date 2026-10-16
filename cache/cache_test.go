package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// 契约套件接入（cachetest.Run）见 contract_suite_test.go（package cache_test
// 外部测试包——cachetest 反向 import 本包，内部测试包接入会构成 import cycle）。

// --- LocalCache 特有场景（套件不覆盖的实现专属行为） ---

func TestLocalCache_MaxSize(t *testing.T) {
	cfg := LocalConfig[int]{
		TTL:     5 * time.Minute,
		MaxSize: 2,
	}
	c := NewLocalCache[int](cfg)

	ctx := context.Background()

	v1 := 1
	v2 := 2
	v3 := 3
	_ = c.Set(ctx, "a", &v1)
	_ = c.Set(ctx, "b", &v2)
	_ = c.Set(ctx, "c", &v3) // should evict "a"

	got, _ := c.Get(ctx, "a")
	if got != nil {
		t.Error("expected 'a' to be evicted")
	}

	got, _ = c.Get(ctx, "c")
	if got == nil || *got != 3 {
		t.Errorf("expected 3, got %v", got)
	}
}

func TestLocalCache_Size(t *testing.T) {
	cfg := LocalConfig[int]{}
	c := NewLocalCache[int](cfg)

	ctx := context.Background()

	if c.Size() != 0 {
		t.Errorf("expected 0, got %d", c.Size())
	}

	v := 1
	_ = c.Set(ctx, "a", &v)
	if c.Size() != 1 {
		t.Errorf("expected 1, got %d", c.Size())
	}

	c.Clear()
	if c.Size() != 0 {
		t.Errorf("expected 0 after clear, got %d", c.Size())
	}
}

func TestLocalCache_Close(t *testing.T) {
	cfg := LocalConfig[int]{TTL: 50 * time.Millisecond}
	c := NewLocalCache[int](cfg)
	ctx := context.Background()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 幂等：重复 Close 不 panic、不返回错误（sync.Once 保护）
	if err := c.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLocalCache_GetOrLoad_SourceError(t *testing.T) {
	sentinel := errors.New("source down")
	cfg := LocalConfig[testUser]{
		Source: func(ctx context.Context, key string) (*testUser, error) {
			return nil, sentinel
		},
	}
	c := NewLocalCache[testUser](cfg)
	ctx := context.Background()

	got, err := c.GetOrLoad(ctx, "k1")
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result, got %+v", got)
	}
}

func TestLocalCache_GetOrLoad_NoSource(t *testing.T) {
	// 未配置 Source 时，未命中直接返回 (nil, nil)。
	cfg := LocalConfig[testUser]{}
	c := NewLocalCache[testUser](cfg)
	ctx := context.Background()

	got, err := c.GetOrLoad(ctx, "missing")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestLocalCache_ConcurrentAccess 验证 LocalCache 在并发读写下的正确性。
// 运行 go test -race 时可检测 TOCTOU 竞态。
func TestLocalCache_ConcurrentAccess(t *testing.T) {
	cfg := LocalConfig[int]{TTL: 50 * time.Millisecond}
	c := NewLocalCache[int](cfg)
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	const goroutines = 20
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := "key"
				v := id*opsPerGoroutine + i
				_ = c.Set(ctx, key, &v)
				_, _ = c.Get(ctx, key)
				_, _ = c.Exists(ctx, key)
			}
		}(g)
	}
	wg.Wait()

	// 只要不 panic、不 race detector 报错即为通过
	if c.Size() < 0 {
		t.Error("unexpected negative size")
	}
}

// TestLocalCache_ConcurrentGetOrLoad 验证 GetOrLoad 在并发场景下
// 不会因为 TOCTOU 竞态丢失 Source 返回的值。
func TestLocalCache_ConcurrentGetOrLoad(t *testing.T) {
	var loadCount atomic.Int32
	cfg := LocalConfig[int]{
		TTL: 5 * time.Minute,
		Source: func(ctx context.Context, key string) (*int, error) {
			loadCount.Add(1)
			v := 42
			return &v, nil
		},
	}
	c := NewLocalCache[int](cfg)
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	const goroutines = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.GetOrLoad(ctx, "shared-key")
			if err != nil {
				t.Errorf("GetOrLoad error: %v", err)
				return
			}
			if got == nil || *got != 42 {
				t.Errorf("expected 42, got %v", got)
			}
		}()
	}
	wg.Wait()

	// 不严格要求 loadCount==1（并发可能多次 miss），
	// 但最终缓存必须有值且正确。
	got, _ := c.Get(ctx, "shared-key")
	if got == nil || *got != 42 {
		t.Errorf("final cache value: expected 42, got %v", got)
	}
}

// TestSingleFlightCache_ConcurrentGetOrLoad 验证 SingleFlightCache
// 在并发请求下只调用一次 Source（dedup 有效性）。
// 契约套件不做弱一致性断言，dedup 是本实现专属强保证，留在此处。
func TestSingleFlightCache_ConcurrentGetOrLoad(t *testing.T) {
	var sourceCalls atomic.Int32
	inner := NewLocalCache[int](LocalConfig[int]{
		TTL: time.Hour,
		Source: func(ctx context.Context, key string) (*int, error) {
			sourceCalls.Add(1)
			time.Sleep(30 * time.Millisecond) // 模拟慢查询
			v := 99
			return &v, nil
		},
	})
	ctx := context.Background()
	defer func() { _ = inner.Close(ctx) }()

	sf := NewSingleFlightCache[int](inner)

	const goroutines = 30
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := sf.GetOrLoad(ctx, "hot-key")
			if err != nil {
				t.Errorf("GetOrLoad error: %v", err)
				return
			}
			if val == nil || *val != 99 {
				t.Errorf("expected 99, got %v", val)
			}
		}()
	}
	wg.Wait()

	// SingleFlightCache 应将并发请求去重，Source 最多调用 1 次
	if got := sourceCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 source call (dedup), got %d", got)
	}
}

// TestLocalCache_RemoveMiddleEntry 验证 orderIdx O(1) 移除中间元素的正确性。
func TestLocalCache_RemoveMiddleEntry(t *testing.T) {
	cfg := LocalConfig[int]{TTL: 5 * time.Minute, MaxSize: 3}
	c := NewLocalCache[int](cfg)
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	v1, v2, v3 := 1, 2, 3
	_ = c.Set(ctx, "a", &v1)
	_ = c.Set(ctx, "b", &v2)
	_ = c.Set(ctx, "c", &v3)

	// 删除中间元素
	_ = c.Del(ctx, "b")

	// 剩余元素应可访问
	got, _ := c.Get(ctx, "a")
	if got == nil || *got != 1 {
		t.Errorf("expected a=1, got %v", got)
	}
	got, _ = c.Get(ctx, "c")
	if got == nil || *got != 3 {
		t.Errorf("expected c=3, got %v", got)
	}

	// 新元素应可插入（MaxSize=3，删了一个后有空间）
	v4 := 4
	_ = c.Set(ctx, "d", &v4)
	if c.Size() != 3 {
		t.Errorf("expected size 3, got %d", c.Size())
	}
}

// TestLocalCache_SetOrderEviction 验证写序淘汰（FIFO + 重写提升，读命中
// 不提升——非严格 LRU）。re-Set 的 key 会被移到队列尾部（最新），
// 淘汰时从头部移除。
func TestLocalCache_SetOrderEviction(t *testing.T) {
	cfg := LocalConfig[int]{TTL: 5 * time.Minute, MaxSize: 3}
	c := NewLocalCache[int](cfg)
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()

	v1, v2, v3 := 1, 2, 3
	_ = c.Set(ctx, "a", &v1)
	_ = c.Set(ctx, "b", &v2)
	_ = c.Set(ctx, "c", &v3)

	// Re-Set "b" to promote it (moves to tail)
	_ = c.Set(ctx, "b", &v2)

	// 插入新元素触发淘汰
	v4 := 4
	_ = c.Set(ctx, "d", &v4)

	// "a" 是队头（最早且未 re-Set），应被淘汰
	got, _ := c.Get(ctx, "a")
	if got != nil {
		t.Error("expected a to be evicted")
	}
	// "b" 被 re-Set 过，应存活
	got, _ = c.Get(ctx, "b")
	if got == nil || *got != 2 {
		t.Errorf("expected b=2 (re-set promoted), got %v", got)
	}
}
