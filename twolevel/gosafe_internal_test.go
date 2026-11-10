package twolevel

// 本文件是 AGENTS.md「禁止裸 goroutine 起手」并发硬规则的回归钉（#169）：
// acquire 的 ctx 收尾必须走 async.GoSafe，收尾里 release → pubsub.Close()
// panic 时进程不崩、panic 进 PanicHandler。用内部包测试注入 panic 的
// pubsub，直接验证该 goroutine 的 recover 行为。

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/redisx"
)

// TestBusAcquireCtxWatcherRecoversClosePanic 钉 #169：acquire 的 ctx 收尾
// goroutine 必须走 async.GoSafe——release 里 pubsub.Close() panic 时进程不
// 崩、panic 进 PanicHandler（裸 goroutine 下 panic 逃逸，整个测试进程崩掉）。
func TestBusAcquireCtxWatcherRecoversClosePanic(t *testing.T) {
	panics := make(chan any, 1) // PanicHandler 全局回调，容量 1 防阻塞
	async.SetPanicHandler(func(_ context.Context, r any, _ []byte) {
		select {
		case panics <- r:
		default:
		}
	})
	t.Cleanup(func() { async.SetPanicHandler(nil) })

	mr := miniredis.RunT(t)
	client, err := redisx.NewClientFromAddr(mr.Addr(), "", 0)
	if err != nil {
		t.Fatalf("redisx client: %v", err)
	}
	b := NewBus(client, "test:watcher-guard")
	ctx, cancel := context.WithCancel(context.Background())
	s, err := b.acquire(ctx, func(string) {})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	real := s.pubsub
	t.Cleanup(func() { _ = real.Close() })
	s.pubsub = (*redis.PubSub)(nil) // 注入 Close() panic（nil 解引用，可 recover）

	cancel() // 触发 ctx 收尾：release → pubsub.Close() panic

	select {
	case <-panics:
		// panic 被 GoSafe recover 并上报——能跑到这里即证进程没崩
	case <-time.After(3 * time.Second):
		t.Fatal("3s 内未观察到 panic 记录：ctx 收尾未走 GoSafe 兜底")
	}
}
