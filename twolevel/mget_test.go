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

// fakeLocal/fakeRemote 的 MGet 实现（记录事件供计数断言）。
func (f *fakeLocal) MGet(ctx context.Context, keys ...string) (map[string]*int, error) {
	f.mu.Lock()
	*f.events = append(*f.events, "local:MGet")
	out := make(map[string]*int, len(keys))
	for _, k := range keys {
		if v, ok := f.store[k]; ok {
			v := v
			out[k] = &v
		}
	}
	f.mu.Unlock()
	return out, nil
}

func (f *fakeRemote) MGet(ctx context.Context, keys ...string) (map[string]*int, error) {
	f.mu.Lock()
	*f.events = append(*f.events, "remote:MGet")
	if f.mgetErr != nil {
		e := f.mgetErr
		f.mu.Unlock()
		return nil, e
	}
	out := make(map[string]*int, len(keys))
	for _, k := range keys {
		if v, ok := f.store[k]; ok {
			v := v
			out[k] = &v
		}
	}
	f.mu.Unlock()
	return out, nil
}

// 跨层组合：L1 批量命中直达；L1 缺席 → L2 单次批量；L2 命中回填 L1。
func TestTwoLevelMGetCrossLevel(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := newFakeLocal(&events)
	remote := newFakeRemote(&events, nil)

	// 预置：lk 只在 L1；rk 只在 L2
	lv := 1
	local.Set(ctx, "lk", &lv)
	rv := 2
	remote.Set(ctx, "rk", &rv)

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}

	got, err := c.MGet(ctx, "lk", "rk", "absent")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(got) != 2 || got["lk"] == nil || got["rk"] == nil {
		t.Fatalf("MGet = %v, want {lk, rk}", got)
	}
	if remote.count("MGet") != 1 {
		t.Fatalf("remote MGet calls = %d, want 1（L2 必须单次批量）", remote.count("MGet"))
	}
	if ok, _ := local.Exists(ctx, "rk"); !ok {
		t.Fatal("L2 命中未回填 L1")
	}

	// 二次批量：全走 L1，remote 不再被调
	if _, err = c.MGet(ctx, "lk", "rk"); err != nil {
		t.Fatalf("second MGet: %v", err)
	}
	if remote.count("MGet") != 1 {
		t.Fatalf("second round remote MGet calls = %d, want 1（L1 已回填）", remote.count("MGet"))
	}
}

// plainLocal/plainRemote：显式委托、不携带 MGet——探测无批量能力后端的
// 退化路径（逐键读）。
type plainLocal struct{ f *fakeLocal }

func (p *plainLocal) Get(ctx context.Context, key string) (*int, error) {
	return p.f.Get(ctx, key)
}
func (p *plainLocal) Set(ctx context.Context, key string, value *int) error {
	return p.f.Set(ctx, key, value)
}
func (p *plainLocal) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return p.f.SetWithTTL(ctx, key, value, ttl)
}
func (p *plainLocal) Del(ctx context.Context, key string) error {
	return p.f.Del(ctx, key)
}
func (p *plainLocal) DelBatch(ctx context.Context, keys ...string) error {
	return p.f.DelBatch(ctx, keys...)
}
func (p *plainLocal) Exists(ctx context.Context, key string) (bool, error) {
	return p.f.Exists(ctx, key)
}

type plainRemote struct{ f *fakeRemote }

func (p *plainRemote) Get(ctx context.Context, key string) (*int, error) {
	return p.f.Get(ctx, key)
}
func (p *plainRemote) Set(ctx context.Context, key string, value *int) error {
	return p.f.Set(ctx, key, value)
}
func (p *plainRemote) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return p.f.SetWithTTL(ctx, key, value, ttl)
}
func (p *plainRemote) Del(ctx context.Context, key string) error {
	return p.f.Del(ctx, key)
}
func (p *plainRemote) DelBatch(ctx context.Context, keys ...string) error {
	return p.f.DelBatch(ctx, keys...)
}
func (p *plainRemote) Exists(ctx context.Context, key string) (bool, error) {
	return p.f.Exists(ctx, key)
}
func (p *plainRemote) GetOrLoad(ctx context.Context, key string) (*int, error) {
	return p.f.GetOrLoad(ctx, key)
}
func (p *plainRemote) Refresh(ctx context.Context, key string) (*int, error) {
	return p.f.Refresh(ctx, key)
}

// 无批量能力时退化为逐键读，结果仍然正确。
func TestTwoLevelMGetFallbackWithoutBatch(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := &plainLocal{f: newFakeLocal(&events)}
	remote := &plainRemote{f: newFakeRemote(&events, nil)}
	rv := 7
	remote.Set(ctx, "rk", &rv)

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}

	got, err := c.MGet(ctx, "rk", "absent")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(got) != 1 || got["rk"] == nil || *got["rk"] != 7 {
		t.Fatalf("MGet = %v, want 只含 rk", got)
	}
	for _, e := range events {
		if e == "local:MGet" || e == "remote:MGet" {
			t.Fatalf("无批量能力不得调用 MGet: %v", events)
		}
	}
	if ok, _ := local.Exists(ctx, "rk"); !ok {
		t.Fatal("退化路径同样要回填 L1")
	}
}

// L2 批量读错误整批透传（契约：反序列化/管道失败 = 整批错误）。
func TestTwoLevelMGetPropagatesL2Error(t *testing.T) {
	var events []string
	ctx := context.Background()
	local := newFakeLocal(&events)
	remote := newFakeRemote(&events, nil)
	boom := errors.New("cluster down")
	remote.mgetErr = boom

	c, err := twolevel.NewTwoLevelCache[int](local, remote)
	if err != nil {
		t.Fatalf("NewTwoLevelCache: %v", err)
	}
	if _, err = c.MGet(ctx, "a", "b"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom（L2 批量错误必须透传）", err)
	}
}

// nilLocal 支持显式 nil 真值条目的 L1 fake（store 值为 *int，可为 nil），
// 实现 HitGetter、**不**实现 BatchGetter——非批量路径的形状（data#123）。
type nilLocal struct {
	mu     sync.Mutex
	store  map[string]*int
	events *[]string
}

func (f *nilLocal) Get(ctx context.Context, key string) (*int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.store[key]; ok {
		return v, nil
	}
	return nil, nil
}

// GetEx 与批量路径同口径：键在场即 hit（值可能为 nil）。
func (f *nilLocal) GetEx(ctx context.Context, key string) (*int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.store[key]
	return v, ok, nil
}

// nilLocalBatch 批量形状：内嵌 nilLocal 后额外实现 BatchGetter——同一存储
// 两种能力形状，路径分流由类型断言决定（data#123 的两条路径）。
type nilLocalBatch struct{ *nilLocal }

func (f *nilLocalBatch) MGet(ctx context.Context, keys ...string) (map[string]*int, error) {
	f.mu.Lock()
	*f.events = append(*f.events, "local:MGet")
	out := make(map[string]*int, len(keys))
	for _, k := range keys {
		if v, ok := f.store[k]; ok {
			out[k] = v
		}
	}
	f.mu.Unlock()
	return out, nil
}

func (f *nilLocal) Set(ctx context.Context, key string, value *int) error {
	f.mu.Lock()
	f.store[key] = value
	f.mu.Unlock()
	return nil
}
func (f *nilLocal) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return f.Set(ctx, key, value)
}
func (f *nilLocal) Del(ctx context.Context, key string) error {
	f.mu.Lock()
	delete(f.store, key)
	f.mu.Unlock()
	return nil
}
func (f *nilLocal) DelBatch(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if err := f.Del(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
func (f *nilLocal) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.store[key]
	return ok, nil
}

// keyLogRemote 记录每个键被查询次数的 L2 fake（Get 与 MGet 合计）——
// 断言 nil 真值条目不回源（data#123）。
type keyLogRemote struct {
	mu      sync.Mutex
	store   map[string]*int
	queried map[string]int
}

func newKeyLogRemote() *keyLogRemote {
	return &keyLogRemote{store: map[string]*int{}, queried: map[string]int{}}
}

func (f *keyLogRemote) queryCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queried[key]
}

func (f *keyLogRemote) Get(ctx context.Context, key string) (*int, error) {
	f.mu.Lock()
	f.queried[key]++
	v := f.store[key]
	f.mu.Unlock()
	return v, nil
}

func (f *keyLogRemote) MGet(ctx context.Context, keys ...string) (map[string]*int, error) {
	f.mu.Lock()
	out := make(map[string]*int, len(keys))
	for _, k := range keys {
		f.queried[k]++
		if v, ok := f.store[k]; ok {
			out[k] = v
		}
	}
	f.mu.Unlock()
	return out, nil
}

func (f *keyLogRemote) Set(ctx context.Context, key string, value *int) error {
	f.mu.Lock()
	f.store[key] = value
	f.mu.Unlock()
	return nil
}
func (f *keyLogRemote) SetWithTTL(ctx context.Context, key string, value *int, ttl time.Duration) error {
	return f.Set(ctx, key, value)
}
func (f *keyLogRemote) Del(ctx context.Context, key string) error {
	f.mu.Lock()
	delete(f.store, key)
	f.mu.Unlock()
	return nil
}
func (f *keyLogRemote) DelBatch(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if err := f.Del(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
func (f *keyLogRemote) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.store[key]
	return ok, nil
}
func (f *keyLogRemote) GetOrLoad(ctx context.Context, key string) (*int, error) {
	return f.Get(ctx, key)
}
func (f *keyLogRemote) Refresh(ctx context.Context, key string) (*int, error) {
	if err := f.Del(ctx, key); err != nil {
		return nil, err
	}
	return f.GetOrLoad(ctx, key)
}

// TestTwoLevelMGetNilHitConsistencyAcrossPaths data#123：L1 存在 nil 真值
// 条目时，MGet 的批量/非批量两条路径必须同口径——键进结果 map（值为 nil）、
// 该键不回源 L2，仅真缺席键走 L2。修复前非批量路径用 `v != nil` 判命中，
// nil 真值被当 miss 回源 L2，与批量路径行为相反。
func TestTwoLevelMGetNilHitConsistencyAcrossPaths(t *testing.T) {
	run := func(t *testing.T, newLocal func(*[]string) datacache.Cache[int]) {
		t.Helper()
		var events []string
		ctx := context.Background()
		local := newLocal(&events)
		remote := newKeyLogRemote()

		// L1 预置 nil 真值条目；remote 预置缺席键的值。
		if err := local.Set(ctx, "nilkey", nil); err != nil {
			t.Fatalf("local.Set(nil): %v", err)
		}
		rv := 9
		if err := remote.Set(ctx, "absent", &rv); err != nil {
			t.Fatalf("remote.Set: %v", err)
		}

		c, err := twolevel.NewTwoLevelCache[int](local, remote)
		if err != nil {
			t.Fatalf("NewTwoLevelCache: %v", err)
		}

		got, err := c.MGet(ctx, "nilkey", "absent")
		if err != nil {
			t.Fatalf("MGet: %v", err)
		}
		// nil 真值条目：键在场、值为 nil——两条路径同口径
		v, ok := got["nilkey"]
		if !ok {
			t.Fatalf("MGet = %v：nilkey 必须进结果 map（命中真值条目，值为 nil）", got)
		}
		if v != nil {
			t.Fatalf("MGet[nilkey] = %v, want nil", *v)
		}
		// 真缺席键照常从 L2 取回
		if av := got["absent"]; av == nil || *av != 9 {
			t.Fatalf("MGet[absent] = %v, want 9", got["absent"])
		}
		// nil 真值条目不得回源 L2；缺席键恰好查询一次
		if n := remote.queryCount("nilkey"); n != 0 {
			t.Fatalf("L2 对 nilkey 查询 %d 次, want 0（nil 真值条目是命中，不回源）", n)
		}
		if n := remote.queryCount("absent"); n != 1 {
			t.Fatalf("L2 对 absent 查询 %d 次, want 1", n)
		}
	}

	t.Run("L1有BatchGetter", func(t *testing.T) {
		run(t, func(events *[]string) datacache.Cache[int] {
			return &nilLocalBatch{&nilLocal{store: map[string]*int{}, events: events}}
		})
	})
	t.Run("L1无BatchGetter走GetEx", func(t *testing.T) {
		run(t, func(events *[]string) datacache.Cache[int] {
			return &nilLocal{store: map[string]*int{}, events: events}
		})
	})
}
