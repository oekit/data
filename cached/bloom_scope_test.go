package cached_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// 本文件锚定写路径 bloom 补录的两条契约：
//   - #204：作用域解析失败只跳过 invalidate，bloomAdd 照常执行——bloom
//     元素刻意不按作用域切分（bloomItem），不消费 scope；Create 是新 ID
//     的唯一补录点，被连坐吞掉 ⇒ 行已提交但点读被 bloomGate 判「绝对
//     不存在」，且 bloom 只增不减、无 TTL 兜底，永不自愈。
//   - #187：后端具备批量能力（BloomMAdder）时一次往返写入，否则逐个 Add。

// toggleScope 可切换状态的作用域解析器：先失败（模拟后台/系统上下文缺
// 租户），后恢复。
type toggleScope struct {
	mu  sync.Mutex
	err error
}

func (s *toggleScope) resolve(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return "t1", nil
}

// countingBloom 在 fakeBloom 之上记录 Add/MAdd 调用形态（#187 断言用）。
type countingBloom struct {
	*fakeBloom
	mu        sync.Mutex
	addCalls  int
	maddCalls int
	maddItems [][]string
}

func (f *countingBloom) Add(ctx context.Context, item string) error {
	f.mu.Lock()
	f.addCalls++
	f.mu.Unlock()
	return f.fakeBloom.Add(ctx, item)
}

func (f *countingBloom) MAdd(ctx context.Context, items ...string) ([]bool, error) {
	f.mu.Lock()
	f.maddCalls++
	cp := make([]string, len(items))
	copy(cp, items)
	f.maddItems = append(f.maddItems, cp)
	f.mu.Unlock()
	f.fakeBloom.mu.Lock()
	defer f.fakeBloom.mu.Unlock()
	for _, item := range items {
		f.fakeBloom.items[item] = true
	}
	return make([]bool, len(items)), nil
}

func (f *countingBloom) stats() (adds, madds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addCalls, f.maddCalls
}

// plainCountingBloom 只实现 BloomChecker（无 MAdd 能力）——退化路径的
// 调用计数（#187：最小接口不被批量能力破坏）。
type plainCountingBloom struct {
	*fakeBloom
	addCalls int
}

func (f *plainCountingBloom) Add(ctx context.Context, item string) error {
	f.mu.Lock()
	f.addCalls++
	f.mu.Unlock()
	return f.fakeBloom.Add(ctx, item)
}

// TestCreateScopeFailureStillRecordsBloom（#204 回归锚点）：作用域失败的
// Create 之后 bloom 必须含新 ID，作用域恢复的 GetByID 必须能读到该行。
func TestCreateScopeFailureStillRecordsBloom(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	scope := &toggleScope{err: errors.New("租户上下文缺失")}
	var ops []string
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
		cached.WithScopeKey[user, int64](scope.resolve),
		cached.WithOnError[user, int64](func(ctx context.Context, op string, keys []string, err error) {
			ops = append(ops, op)
		}),
	)

	if err := cr.Create(ctx, &user{ID: 7, Name: "n"}); err != nil {
		t.Fatalf("Create 应成功（写已提交，作用域失败只跳过失效）: %v", err)
	}
	if !containsScopeOp(ops) {
		t.Fatalf("作用域解析失败未上报，ops = %v", ops)
	}
	if !bf.items["7"] {
		t.Fatal("作用域失败不得连坐 bloomAdd：新 ID 必须进 bloom（bloom 不按作用域切分，不消费 scope）")
	}

	// 作用域恢复后点读必须命中（修复前：bloom 缺 ID ⇒ bloomGate 判
	// 「绝对不存在」⇒ null marker ⇒ ErrNotFound，且永不自愈）。
	scope.mu.Lock()
	scope.err = nil
	scope.mu.Unlock()
	got, err := cr.GetByID(ctx, 7)
	if err != nil || got == nil || got.Name != "n" {
		t.Fatalf("作用域恢复后 GetByID(7) = %v, %v; want 行可见（自愈路径）", got, err)
	}
}

// TestBatchCreateScopeFailureStillRecordsBloom（#204）：批量创建同口径。
func TestBatchCreateScopeFailureStillRecordsBloom(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	scope := &toggleScope{err: errors.New("租户上下文缺失")}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
		cached.WithScopeKey[user, int64](scope.resolve),
	)

	if err := cr.BatchCreate(ctx, []*user{{ID: 7, Name: "a"}, {ID: 8, Name: "b"}}, 2); err != nil {
		t.Fatalf("BatchCreate 应成功: %v", err)
	}
	if !bf.items["7"] || !bf.items["8"] {
		t.Fatalf("作用域失败不得连坐 bloomAdd：items = %v", bf.items)
	}
}

// TestRunInTxScopeFailureStillRecordsBloom（#204）：事务提交路径同口径
// ——作用域失败跳过 invalidate，但事务内 Create 的新 ID 必须补录。
func TestRunInTxScopeFailureStillRecordsBloom(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	scope := &toggleScope{err: errors.New("租户上下文缺失")}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
		cached.WithScopeKey[user, int64](scope.resolve),
	)

	err := cr.RunInTx(ctx, func(rp crud.Repository[user, int64]) error {
		return rp.Create(ctx, &user{Name: "tx"}) // 自增 ID = 1
	})
	if err != nil {
		t.Fatalf("RunInTx 应成功: %v", err)
	}
	if !bf.items["1"] {
		t.Fatalf("事务提交后作用域失败不得吞掉 bloom 补录：items = %v", bf.items)
	}

	scope.mu.Lock()
	scope.err = nil
	scope.mu.Unlock()
	got, err := cr.GetByID(ctx, 1)
	if err != nil || got == nil || got.Name != "tx" {
		t.Fatalf("作用域恢复后 GetByID(1) = %v, %v; want 行可见（自愈路径）", got, err)
	}
}

// TestBloomAddBatchesWhenCapable（#187）：后端具备 MAdd 批量能力时，
// BatchCreate 的 N 个 ID 一次往返写入。
func TestBloomAddBatchesWhenCapable(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &countingBloom{fakeBloom: &fakeBloom{items: map[string]bool{}}}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
	)

	if err := cr.BatchCreate(ctx, []*user{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}, {ID: 3, Name: "c"}}, 3); err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	adds, madds := bf.stats()
	if madds != 1 || adds != 0 {
		t.Fatalf("批量能力下应一次 MAdd 写入：MAdd=%d Add=%d", madds, adds)
	}
	if len(bf.maddItems[0]) != 3 {
		t.Fatalf("MAdd 应携带全部 3 个元素: %v", bf.maddItems[0])
	}
	for _, id := range []string{"1", "2", "3"} {
		if !bf.fakeBloom.items[id] {
			t.Fatalf("元素 %s 未进 bloom", id)
		}
	}
}

// TestBloomAddFallsBackPerID（#187）：后端只有逐个 Add 能力时退化为
// N 次 Add（最小接口不被破坏），元素照常补录。
func TestBloomAddFallsBackPerID(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &plainCountingBloom{fakeBloom: &fakeBloom{items: map[string]bool{}}}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
	)

	if err := cr.BatchCreate(ctx, []*user{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}, 2); err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if bf.addCalls != 2 {
		t.Fatalf("无批量能力应退化为逐个 Add：Add=%d", bf.addCalls)
	}
	if !bf.fakeBloom.items["1"] || !bf.fakeBloom.items["2"] {
		t.Fatalf("退化路径元素照常补录: %v", bf.fakeBloom.items)
	}
}
