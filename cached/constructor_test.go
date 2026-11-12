package cached_test

import (
	"testing"

	dcache "github.com/oekit/data/cache"
	"github.com/oekit/data/cached"
)

// NewRepository 构造期 fail-fast（#184）：repo / newCache 为 nil、工厂
// 产物为 nil 都在装配点 panic（配置错误启动即暴露的对齐纪律），不留
// 「nil 在使用期才 panic、距装配点远且无现场」的坑。
func TestNewRepositoryFailFast(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: 必须在装配期 fail-fast panic（而非使用期 nil panic）", name)
			}
		}()
		fn()
	}

	repo := newFakeRepo[user](func(u *user) int64 { return u.ID }, nil)

	mustPanic("repo 为 nil", func() {
		cached.NewRepository[user, int64](nil, userCacheFactory)
	})
	mustPanic("newCache 工厂为 nil", func() {
		cached.NewRepository[user, int64](repo, nil)
	})
	mustPanic("工厂返回 nil 缓存", func() {
		cached.NewRepository[user, int64](repo,
			func(src dcache.SourceFunc[user]) dcache.CacheAside[user] { return nil })
	})

	// 合法装配不受 fail-fast 影响。
	_ = cached.NewRepository[user, int64](repo, userCacheFactory)
}
