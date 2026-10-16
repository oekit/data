package cachetest_test

import (
	"testing"

	"github.com/oekit/data/cachetest"
	pkgcache "github.com/oekit/pkg/cache"
)

// TestRunViaFacade 经门面跑完整契约（LocalCache 自证，同时验证套件类型同一）。
func TestRunViaFacade(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), func(t *testing.T, source pkgcache.SourceFunc[int]) pkgcache.CacheAside[int] {
		return pkgcache.NewLocalCache(pkgcache.LocalConfig[int]{Source: source})
	})
}
