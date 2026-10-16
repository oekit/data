package twolevel_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
