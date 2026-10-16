package cached_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/oekit/data/cached"
	"github.com/oekit/gormx/crud"
)

// GetByIDs 批量点读：命中的进 map、未找到的不在 map 里（点读缓存已用
// null marker 防穿透）；单个 ID 的仓储硬错误不阻断其余 ID（errors.Join）。
func TestGetByIDs(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	repo.byID[2] = &user{ID: 2, Name: "b"}
	repo.byID[3] = &user{ID: 3, Name: "c"}
	ctx := context.Background()
	cr := newCachedRepo(repo)

	got, err := cr.GetByIDs(ctx, []int64{1, 2, 3, 99})
	if err != nil {
		t.Fatalf("GetByIDs err = %v（未找到不算错误）", err)
	}
	if len(got) != 3 {
		t.Fatalf("map size = %d, want 3（99 未找到不进 map）", len(got))
	}
	if got[1].Name != "a" || got[3].Name != "c" {
		t.Fatalf("map 内容不符: %v", got)
	}
	// 4 次 GetByID 各回源一次（99 由 null marker 记"不存在"）
	if n := repo.count("GetByID"); n != 4 {
		t.Fatalf("repo calls = %d, want 4", n)
	}

	// 第二轮全部命中缓存（含 99 的 null marker），仓储不再被调
	got2, err := cr.GetByIDs(ctx, []int64{1, 2, 3, 99})
	if err != nil {
		t.Fatalf("second GetByIDs: %v", err)
	}
	if len(got2) != 3 || got2[2].Name != "b" {
		t.Fatalf("second map = %v", got2)
	}
	if n := repo.count("GetByID"); n != 4 {
		t.Fatalf("repo calls after second round = %d, want 4（含 null marker 必须全命中）", n)
	}
}

// 单个 ID 的仓储硬错误不阻断其余 ID，错误聚合返回。
func TestGetByIDsJoinsPerIDErrors(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[1] = &user{ID: 1, Name: "a"}
	repo.byID[2] = &user{ID: 2, Name: "b"}
	repo.failIDs = map[int64]error{2: errors.New("row lock timeout")}
	ctx := context.Background()
	cr := newCachedRepo(repo)

	got, err := cr.GetByIDs(ctx, []int64{1, 2})
	if err == nil {
		t.Fatal("硬错误必须聚合返回")
	}
	if got[1] == nil || got[1].Name != "a" {
		t.Fatalf("健康 ID 必须照常返回: %v", got)
	}
	if _, present := got[2]; present {
		t.Fatal("故障 ID 不得进 map")
	}
	if !errors.Is(err, repo.failIDs[2]) {
		t.Fatalf("err = %v, want 包含注入错误", err)
	}
}

// --- bloom 防穿透可选件 ---

type fakeBloom struct {
	mu        sync.Mutex
	items     map[string]bool
	existsErr error
}

func (f *fakeBloom) Exists(ctx context.Context, item string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[item], nil
}

func (f *fakeBloom) Add(ctx context.Context, item string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[item] = true
	return nil
}

func newBloomCachedRepo(repo *fakeRepo[user], bf *fakeBloom) *cached.CachedRepository[user] {
	return cached.NewRepository[user](repo, userCacheFactory,
		cached.WithID[user](func(u *user) int64 { return u.ID }),
		cached.WithBloomFilter[user](bf),
	)
}

// bloom 判定"从未存在"：直接 null marker 挡穿，仓储零调用。
func TestBloomMissGatesSource(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	ctx := context.Background()
	cr := newBloomCachedRepo(repo, bf)

	for i := 0; i < 2; i++ {
		if _, err := cr.GetByID(ctx, 42); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("GetByID #%d err = %v, want ErrNotFound", i+1, err)
		}
	}
	if n := repo.count("GetByID"); n != 0 {
		t.Fatalf("repo calls = %d, want 0（bloom 挡穿，仓储零调用）", n)
	}
}

// bloom 判定"可能存在"：放行仓储正常回源。
func TestBloomHitPassesThrough(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	bf := &fakeBloom{items: map[string]bool{"7": true}}
	ctx := context.Background()
	cr := newBloomCachedRepo(repo, bf)

	got, err := cr.GetByID(ctx, 7)
	if err != nil || got.Name != "alice" {
		t.Fatalf("GetByID = %v, %v", got, err)
	}
	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo calls = %d, want 1", n)
	}
}

// bloom 读失败 fail-open：放行仓储（漏判会错杀真数据，绝不 fail-closed）。
func TestBloomErrorFailsOpen(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	repo.byID[7] = &user{ID: 7, Name: "alice"}
	bf := &fakeBloom{items: map[string]bool{}, existsErr: errors.New("bloom down")}
	ctx := context.Background()
	cr := newBloomCachedRepo(repo, bf)

	got, err := cr.GetByID(ctx, 7)
	if err != nil || got.Name != "alice" {
		t.Fatalf("GetByID = %v, %v（bloom 故障必须放行仓储）", got, err)
	}
	if n := repo.count("GetByID"); n != 1 {
		t.Fatalf("repo calls = %d, want 1", n)
	}
}

// Create 把新 ID 加入 bloom（此后点读能正常穿透到仓储）。
func TestCreateAddsBloom(t *testing.T) {
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)
	bf := &fakeBloom{items: map[string]bool{}}
	ctx := context.Background()
	cr := newBloomCachedRepo(repo, bf)

	if err := cr.Create(ctx, &user{ID: 9, Name: "late"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bf.mu.Lock()
	added := bf.items["9"]
	bf.mu.Unlock()
	if !added {
		t.Fatal("Create 必须把新 ID 加入 bloom")
	}

	// 此后 bloom 放行 + 缓存正常工作
	got, err := cr.GetByID(ctx, 9)
	if err != nil || got.Name != "late" {
		t.Fatalf("GetByID after create = %v, %v", got, err)
	}
}

// 事务内 Create 提交后同样要进 bloom。
func TestTxCreateAddsBloomAfterCommit(t *testing.T) {
	var events []string
	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, &events)
	bf := &fakeBloom{items: map[string]bool{}}
	ctx := context.Background()
	cr := newBloomCachedRepo(repo, bf)

	if err := cr.RunInTx(ctx, func(tx crud.Repository[user]) error {
		return tx.Create(ctx, &user{ID: 12, Name: "tx"})
	}); err != nil {
		t.Fatalf("RunInTx: %v", err)
	}
	bf.mu.Lock()
	added := bf.items["12"]
	bf.mu.Unlock()
	if !added {
		t.Fatal("事务提交后必须把新 ID 加入 bloom")
	}
}
