package cachetest

import (
	"testing"

	pkgcache "github.com/oekit/pkg/cache"

	src "github.com/oekit/pkg/cache/cachetest"
)

// Run 跑完整契约场景（8 个子测试）。
func Run[T any](t *testing.T, c Contract[T], factory func(t *testing.T, source pkgcache.SourceFunc[T]) pkgcache.CacheAside[T]) {
	src.Run(t, c, factory)
}

// Comparable 返回可比较类型的现成 Contract。
func Comparable[T comparable]() Contract[T] { return src.Comparable[T]() }
