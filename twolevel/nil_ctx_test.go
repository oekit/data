package twolevel_test

// nil ctx 全入口归一测试（cachetest 契约第 9 场景
// nil_ctx_treated_as_background，pkg#554，消费方 CI 地板 bump 到 pkg
// v0.55.1 后暴露）：TwoLevelCache 此前对 nil ctx 无兜底，nil 接口值经
// GetOrLoad → L2（redisx RedisCache，go.mod 锁 redisx v0.16.5）透传进
// go-redis v9 连接池，pool.waitTurn 处 nil 解引用 panic。
//
// 契约场景只覆盖 Get/GetOrLoad/Set/Exists/Del 五个入口；本文件补齐其余
// 公开入口（SetWithTTL/DelBatch/MGet/Refresh/Start/Close）的 nil ctx
// 路径。语义口径与 pkg/contextx.OrBackground 一致：nil ==
// context.Background()（只做 nil 归一，不复活取消信号）。

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/oekit/data/twolevel"
)

func TestTwoLevelCache_NilCtxTreatedAsBackground(t *testing.T) {
	t.Run("写读删批量全入口", func(t *testing.T) {
		c := newTwoLevelInt(t, time.Minute, func(_ context.Context, _ string) (*int, error) {
			v := 42
			return &v, nil
		}).(*twolevel.TwoLevelCache[int]) // MGet 在 BatchGetter 上，取具体类型

		if err := c.SetWithTTL(nil, "k1", intPtr(1), time.Minute); err != nil { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx SetWithTTL: %v", err)
		}
		if err := c.Set(nil, "k2", intPtr(2)); err != nil { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Set: %v", err)
		}

		if got, err := c.Get(nil, "k2"); err != nil || got == nil || *got != 2 { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Get: got (%v, %v)", got, err)
		}
		out, err := c.MGet(nil, "k1", "k2") //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
		if err != nil || len(out) != 2 || *out["k1"] != 1 {
			t.Fatalf("nil ctx MGet: got (%v, %v)", out, err)
		}
		if exists, err := c.Exists(nil, "k1"); err != nil || !exists { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Exists: got (exists=%v, err=%v)", exists, err)
		}
		if err := c.DelBatch(nil, "k2"); err != nil { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx DelBatch: %v", err)
		}
		if err := c.Del(nil, "k1"); err != nil { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Del: %v", err)
		}
	})

	t.Run("回源与刷新", func(t *testing.T) {
		c := newTwoLevelInt(t, time.Minute, func(_ context.Context, _ string) (*int, error) {
			v := 7
			return &v, nil
		})

		if got, err := c.GetOrLoad(nil, "lk"); err != nil || got == nil || *got != 7 { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx GetOrLoad: got (%v, %v)", got, err)
		}
		if got, err := c.Refresh(nil, "lk"); err != nil || got == nil || *got != 7 { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Refresh: got (%v, %v)", got, err)
		}
	})

	t.Run("生命周期入口（挂 Bus 打通 bus.acquire(ctx) 路径）", func(t *testing.T) {
		// 不挂 Bus 的 Start 是 no-op，覆盖不到订阅建立路径；这里用
		// newBussedCacheCtx 传 nil ctx，Start(nil) 须归一后才能真正建立起
		// Redis 订阅（否则 go-redis pubsub 处 nil 解引用 panic）。
		mr := miniredis.RunT(t)
		c, _, _ := newBussedCacheCtx(t, mr, "nilctx-bus", nil)

		if err := c.Close(nil); err != nil { //nolint:staticcheck // SA1012：传 nil ctx 正是本测试点
			t.Fatalf("nil ctx Close: %v", err)
		}
	})
}

func intPtr(v int) *int { return &v }
