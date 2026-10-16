package cache_test

// cache 契约套件接入点。放外部测试包（package cache_test）而非内部包：
// cachetest 反向 import cache（被测接口），内部测试包再 import cachetest
// 会构成 import cycle；外部测试包允许同时 import 两者。
//
// 后续新增 CacheAside 后端（etcd/consul/...，可能在他仓）直接 import
// github.com/oekit/data/cachetest 跑 Run 即可，无需复刻本文件。

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/data/cache"
	cachetest "github.com/oekit/data/cachetest"
)

// TestContract_LocalCache 跑 cachetest 契约套件（LocalCache 直连）。
// 通用语义（miss (nil,nil)、往返/覆盖、显式 TTL、Del 幂等、回填计数、
// null marker、Refresh、并发混合）由套件固化，本包不再重复编写。
func TestContract_LocalCache(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), func(t *testing.T, source cache.SourceFunc[int]) cache.CacheAside[int] {
		c := cache.NewLocalCache(cache.LocalConfig[int]{TTL: 5 * time.Minute, Source: source})
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	})
}

// TestContract_SingleFlightCache_LocalCache 跑契约套件
// （SingleFlightCache 包装 LocalCache 组合场景——装饰器必须透明满足同一契约）。
func TestContract_SingleFlightCache_LocalCache(t *testing.T) {
	cachetest.Run(t, cachetest.Comparable[int](), func(t *testing.T, source cache.SourceFunc[int]) cache.CacheAside[int] {
		inner := cache.NewLocalCache(cache.LocalConfig[int]{TTL: 5 * time.Minute, Source: source})
		t.Cleanup(func() { _ = inner.Close(context.Background()) })
		return cache.NewSingleFlightCache(inner)
	})
}
