package twolevel_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	datacache "github.com/oekit/data/cache"
	"github.com/oekit/data/twolevel"
)

// fakeLocal 记录操作事件的本地缓存 fake（供双删顺序断言）。
type fakeLocal struct {
	mu     sync.Mutex
	store  map[string]int
	events *[]string
}

func newFakeLocal(events *[]string) *fakeLocal {
	return &fakeLocal{store: map[string]int{}, events: events}
}

func (f *fakeLocal) log(ev string) {
	f.mu.Lock()
	*f.events = append(*f.events, ev)
	f.mu.Unlock()
}

func (f *fakeLocal) Get(ctx context.Context, key string) (*int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.store[key]; ok {
		v := v
		return &v, nil
	}
	return nil, nil
}
func (f *fakeLocal) Set(ctx context.Context, key string, value *int) error {
	f.mu.Lock()
	f.store[key] = *value
	f.mu.Unlock()
	return nil
}
func (f *fakeLocal) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return f.Set(ctx, key, value)
}
func (f *fakeLocal) Del(ctx context.Context, key string) error {
	f.log("local:del:" + key)
	f.mu.Lock()
	delete(f.store, key)
	f.mu.Unlock()
	return nil
}
func (f *fakeLocal) DelBatch(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if err := f.Del(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeLocal) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.store[key]
	return ok, nil
}

// fakeRemote 记录操作事件的远端缓存 fake。
type fakeRemote struct {
	mu     sync.Mutex
	store  map[string]int
	events *[]string
	source datacache.SourceFunc[int]
}

func newFakeRemote(events *[]string, source datacache.SourceFunc[int]) *fakeRemote {
	return &fakeRemote{store: map[string]int{}, events: events, source: source}
}

func (f *fakeRemote) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range *f.events {
		if e == "remote:"+op {
			n++
		}
	}
	return n
}

func (f *fakeRemote) Get(ctx context.Context, key string) (*int, error) {
	f.mu.Lock()
	*f.events = append(*f.events, "remote:Get")
	v, ok := f.store[key]
	f.mu.Unlock()
	if ok {
		v := v
		return &v, nil
	}
	return nil, nil
}
func (f *fakeRemote) Set(ctx context.Context, key string, value *int) error {
	f.mu.Lock()
	f.store[key] = *value
	f.mu.Unlock()
	return nil
}
func (f *fakeRemote) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return f.Set(ctx, key, value)
}
func (f *fakeRemote) Del(ctx context.Context, key string) error {
	f.mu.Lock()
	*f.events = append(*f.events, "remote:Del")
	delete(f.store, key)
	f.mu.Unlock()
	return nil
}
func (f *fakeRemote) DelBatch(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if err := f.Del(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeRemote) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.store[key]
	return ok, nil
}
func (f *fakeRemote) GetOrLoad(ctx context.Context, key string) (*int, error) {
	f.mu.Lock()
	*f.events = append(*f.events, "remote:GetOrLoad")
	v, ok := f.store[key]
	f.mu.Unlock()
	if ok {
		v := v
		return &v, nil
	}
	if f.source == nil {
		return nil, nil
	}
	return f.source(ctx, key)
}
func (f *fakeRemote) Refresh(ctx context.Context, key string) (*int, error) {
	if err := f.Del(ctx, key); err != nil {
		return nil, err
	}
	return f.GetOrLoad(ctx, key)
}

// L1 命中不打 L2；L2 命中回填 L1（第二次读 L1 命中，remote 不再被调）。
func TestTwoLevelReadThroughAndBackfill(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := newFakeLocal(&events)
	source := func(ctx context.Context, key string) (*int, error) {
		v := 42
		return &v, nil
	}
	remote := newFakeRemote(&events, source)

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}

	v, err := c.GetOrLoad(ctx, "k")
	if err != nil || v == nil || *v != 42 {
		t.Fatalf("first GetOrLoad = %v, %v", v, err)
	}
	// L2 命中后必须回填 L1：第二次走 L1，remote.GetOrLoad 只调一次
	v2, err := c.GetOrLoad(ctx, "k")
	if err != nil || v2 == nil || *v2 != 42 {
		t.Fatalf("second GetOrLoad = %v, %v", v2, err)
	}
	if n := remote.count("GetOrLoad"); n != 1 {
		t.Fatalf("remote.GetOrLoad calls = %d, want 1（第二次必须 L1 命中）", n)
	}
	if ok, _ := local.Exists(ctx, "k"); !ok {
		t.Fatal("L2 命中后未回填 L1")
	}
}

// SetWithTTL 的显式 TTL 同时作用于 L1 与 L2（否则 L1 长驻吞掉短 TTL）。
func TestSetWithTTLPropagatesToBothLevels(t *testing.T) {
	ctx := context.Background()
	c := newTwoLevelInt(t, time.Hour, nil) // L1/L2 默认 TTL 1h

	v := 7
	if err := c.SetWithTTL(ctx, "short", &v, 30*time.Millisecond); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // 超过显式 TTL

	got, err := c.Get(ctx, "short")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("显式短 TTL 后 L1 仍返回 %v——TTL 未传导到 L1", *got)
	}
	// L1 过期后读穿 L2：L2 的同 TTL 也应已过期（走 GetOrLoad 验证不复活旧值）
	v2, err := c.GetOrLoad(ctx, "short")
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if v2 != nil {
		t.Fatalf("显式短 TTL 后 L2 仍返回 %v——TTL 未传导到 L2", *v2)
	}
}

// Del 双删两级，顺序必须先远端后本地：
// 远端失败时本地已删，读路径回填的是远端旧值（短窗口，TTL 兜底）；
// 反序则本地旧值会活过整个 L1 TTL。
func TestDelDeletesRemoteBeforeLocal(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := newFakeLocal(&events)
	remote := newFakeRemote(&events, nil)

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatalf("Del: %v", err)
	}

	remoteAt, localAt := -1, -1
	for i, e := range events {
		if e == "remote:Del" && remoteAt < 0 {
			remoteAt = i
		}
		if e == "local:del:k" && localAt < 0 {
			localAt = i
		}
	}
	if remoteAt < 0 || localAt < 0 {
		t.Fatalf("两级都必须被删: %v", events)
	}
	if remoteAt > localAt {
		t.Fatalf("必须先删远端后删本地: %v", events)
	}
}

// L2 null marker 防穿透：Source 只被调一次，后续被 L2 marker 挡住。
func TestNullMarkerDelegatedToRemote(t *testing.T) {
	ctx := context.Background()
	sourceCalls := 0
	source := func(ctx context.Context, key string) (*int, error) {
		sourceCalls++
		return nil, nil
	}
	c := newTwoLevelInt(t, time.Minute, source)

	for i := 0; i < 3; i++ {
		v, err := c.GetOrLoad(ctx, "ghost")
		if err != nil || v != nil {
			t.Fatalf("GetOrLoad #%d = %v, %v, want (nil, nil)", i+1, v, err)
		}
	}
	if sourceCalls != 1 {
		t.Fatalf("source calls = %d, want 1（防穿透委托 L2 marker）", sourceCalls)
	}
}

// 远端错误原样透传，不回填 L1。
func TestRemoteErrorPropagatesWithoutBackfill(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := newFakeLocal(&events)
	remote := newFakeRemote(&events, nil)
	// 构造 GetOrLoad 报错的远端：GetOrLoad 先记事件再走 source——
	// 这里用返回错误的 source 模拟远端回源失败
	boom := errors.New("redis down")
	remote.source = func(ctx context.Context, key string) (*int, error) {
		return nil, boom
	}

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if _, err = c.GetOrLoad(ctx, "k"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if ok, _ := local.Exists(ctx, "k"); ok {
		t.Fatal("远端错误时不得回填 L1")
	}
}

// 构造期校验：nil 后端必须被拒绝。
func TestNewTwoLevelCacheRejectsNil(t *testing.T) {
	var events []string
	local := newFakeLocal(&events)
	remote := newFakeRemote(&events, nil)

	if _, err := twolevel.NewTwoLevelCache[int](nil, remote); err == nil {
		t.Fatal("nil local 必须被拒绝")
	}
	if _, err := twolevel.NewTwoLevelCache[int](local, nil); err == nil {
		t.Fatal("nil remote 必须被拒绝")
	}
}

// Close 幂等（无 bus 时只关 L1 清理 goroutine）。
func TestCloseIdempotent(t *testing.T) {
	ctx := context.Background()
	var events []string
	c, err := twolevel.NewTwoLevelCache[int](
		datacache.NewLocalCache(datacache.LocalConfig[int]{TTL: time.Minute}),
		newFakeRemote(&events, nil),
	)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatalf("second Close（必须幂等）: %v", err)
	}
}
