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
		f.items[item] = true
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
		if !bf.items[id] {
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
	if !bf.items["1"] || !bf.items["2"] {
		t.Fatalf("退化路径元素照常补录: %v", bf.items)
	}
}

// —— registry#224 同族：data cached bloom 漏录降级与自愈 ——

// flakyBloom 前 failNext 次写失败（模拟 Redis 抖动），并记录写批次与
// Exists 调用数（区分降级期/恢复期 gate 是否走 bloom 查询）。
type flakyBloom struct {
	*fakeBloom
	mu          sync.Mutex
	failNext    int
	maddBatches [][]string
	existsCalls int
}

func (f *flakyBloom) Exists(ctx context.Context, item string) (bool, error) {
	f.mu.Lock()
	f.existsCalls++
	f.mu.Unlock()
	return f.fakeBloom.Exists(ctx, item)
}

func (f *flakyBloom) MAdd(ctx context.Context, items ...string) ([]bool, error) {
	f.mu.Lock()
	cp := make([]string, len(items))
	copy(cp, items)
	f.maddBatches = append(f.maddBatches, cp)
	if f.failNext > 0 {
		f.failNext--
		f.mu.Unlock()
		return nil, errors.New("redis: connection reset")
	}
	f.mu.Unlock()
	f.fakeBloom.mu.Lock()
	defer f.fakeBloom.mu.Unlock()
	for _, item := range items {
		f.items[item] = true
	}
	return make([]bool, len(items)), nil
}

// TestBloomAddFailureDegradesGateAndSelfHeals（#224 回归锚点）：补录写
// 失败后点读不得被 gate 拒成 ErrNotFound（数据消失级），且漏录 ID 必须在
// 下一次成功补录时合并重放、前置门随后恢复。
func TestBloomAddFailureDegradesGateAndSelfHeals(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &flakyBloom{fakeBloom: &fakeBloom{items: map[string]bool{}}}
	cr := cached.NewRepository[user, int64](repo, userCacheFactory,
		cached.WithID[user, int64](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user, int64](bf),
	)

	// ① 首次 Create：行落库成功、bloom 补录失败（Redis 抖动）。
	bf.mu.Lock()
	bf.failNext = 1
	bf.mu.Unlock()
	if err := cr.Create(ctx, &user{ID: 7, Name: "n"}); err != nil {
		t.Fatalf("Create 应成功（写已提交，bloom 是 best-effort）: %v", err)
	}

	// ② 降级期点读：bloom 漏录会让 gate 判「绝对不存在」——必须 fail-open
	// 直查库读到行（修复前：null marker ⇒ ErrNotFound，数据消失且不自愈）。
	got, err := cr.GetByID(ctx, 7)
	if err != nil || got == nil || got.Name != "n" {
		t.Fatalf("★ #224 漏录降级期点读必须命中（此前被 gate 拒成 ErrNotFound）: got=%v err=%v", got, err)
	}
	bf.mu.Lock()
	gateCalls := bf.existsCalls
	bf.mu.Unlock()
	if gateCalls != 0 {
		t.Fatalf("降级期不应走 bloom 查询（fail-open 直查库）, Exists 调用=%d", gateCalls)
	}

	// ③ 写恢复后的下一次 Create：本批与漏录账本合并重放（一次 MAdd）。
	if err = cr.Create(ctx, &user{ID: 8, Name: "m"}); err != nil {
		t.Fatalf("第二次 Create: %v", err)
	}
	bf.mu.Lock()
	lastBatch := bf.maddBatches[len(bf.maddBatches)-1]
	bf.mu.Unlock()
	replayed := false
	for _, item := range lastBatch {
		if item == "7" {
			replayed = true
		}
	}
	if !replayed {
		t.Fatalf("★ 漏录 ID 必须在下次成功补录时合并重放, last batch=%v", lastBatch)
	}
	if !bf.items["7"] {
		t.Fatal("重放后 bloom 应含漏录 ID 7")
	}

	// ④ 前置门恢复：未知 ID 重新走 bloom 查询并被拦截。
	_, err = cr.GetByID(ctx, 999)
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("恢复后 gate 应恢复拦截, err=%v", err)
	}
	bf.mu.Lock()
	callsAfter := bf.existsCalls
	bf.mu.Unlock()
	if callsAfter == 0 {
		t.Fatal("★ 恢复后点读应重新走 bloom 查询（前置门已恢复）")
	}
}
