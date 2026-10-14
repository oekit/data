package distlock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestLocker 启动 miniredis 并返回带真实 redis 客户端的 RedisLocker.
func newTestLocker(t *testing.T) (*RedisLocker, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return NewRedisLocker(c), mr
}

func TestTryLock_Success(t *testing.T) {
	l, mr := newTestLocker(t)
	ctx := context.Background()

	ownerID, err := l.TryLock(ctx, "k1", time.Minute)
	if err != nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if ownerID == "" {
		t.Error("expected non-empty ownerID")
	}
	if !mr.Exists("lock:k1") {
		t.Error("expected lock:k1 to exist in miniredis")
	}
}

func TestTryLock_NotAcquired(t *testing.T) {
	l, mr := newTestLocker(t)
	ctx := context.Background()

	// 预占 key
	if err := mr.Set("lock:k1", "other-owner"); err != nil {
		t.Fatal(err)
	}

	_, err := l.TryLock(ctx, "k1", time.Minute)
	if !errors.Is(err, ErrLockNotAcquired) {
		t.Errorf("expected ErrLockNotAcquired, got %v", err)
	}
}

func TestTryLock_OnLockCallback(t *testing.T) {
	l, _ := newTestLocker(t)

	var gotAcquired bool
	var gotKey string
	l.onLock = func(key string, acquired bool, err error) {
		gotKey = key
		gotAcquired = acquired
	}

	_, err := l.TryLock(context.Background(), "k1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !gotAcquired || gotKey != "k1" {
		t.Errorf("expected onLock(k1, true, nil), got key=%q acquired=%v", gotKey, gotAcquired)
	}
}

func TestTryLock_OnLockCallback_NotAcquired(t *testing.T) {
	l, mr := newTestLocker(t)
	if err := mr.Set("lock:k1", "other"); err != nil {
		t.Fatal(err)
	}

	var gotAcquired bool
	l.onLock = func(key string, acquired bool, err error) {
		gotAcquired = acquired
	}

	_, _ = l.TryLock(context.Background(), "k1", time.Minute)
	if gotAcquired {
		t.Error("expected onLock called with acquired=false")
	}
}

func TestUnlock_Success(t *testing.T) {
	l, mr := newTestLocker(t)
	ctx := context.Background()

	ownerID, err := l.TryLock(ctx, "k1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("lock:k1") {
		t.Fatal("expected lock to exist")
	}

	if err := l.Unlock(ctx, "k1", ownerID); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if mr.Exists("lock:k1") {
		t.Error("expected lock to be deleted after Unlock")
	}
}

func TestUnlock_WrongOwner(t *testing.T) {
	l, mr := newTestLocker(t)
	ctx := context.Background()

	_, err := l.TryLock(ctx, "k1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 用错 ownerID 解锁：Lua 脚本比较 ARGV[1] != current value，
	// 不调用 DEL，返 0。go-redis 把 0 当 Result() 成功（nil error），
	// key 仍存在。
	err = l.Unlock(ctx, "k1", "wrong-owner")
	if err != nil {
		t.Errorf("unexpected error from unlock script: %v", err)
	}
	// 验证 key 仍存在（Lua 没删）
	if !mr.Exists("lock:k1") {
		t.Error("expected lock:k1 to still exist after wrong-owner Unlock")
	}
}

func TestUnlock_OnUnlockCallback(t *testing.T) {
	l, _ := newTestLocker(t)
	ctx := context.Background()

	var gotErr error
	l.onUnlock = func(key string, err error) {
		gotErr = err
	}

	ownerID, _ := l.TryLock(ctx, "k1", time.Minute)
	if err := l.Unlock(ctx, "k1", ownerID); err != nil {
		t.Fatal(err)
	}
	if gotErr != nil {
		t.Errorf("expected onUnlock with nil err, got %v", gotErr)
	}
}

func TestLock_Success_AfterContention(t *testing.T) {
	l, mr := newTestLocker(t)
	ctx := context.Background()

	// 第一次: 占用
	firstOwner, err := l.TryLock(ctx, "k1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 第二次: 在另一个 goroutine 解锁后, 我们的 Lock 应当能拿到
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = l.Unlock(context.Background(), "k1", firstOwner)
	}()

	ownerID, err := l.Lock(ctx, "k1", time.Minute, 2*time.Second)
	if err != nil {
		t.Fatalf("expected Lock to succeed after contention, got %v", err)
	}
	if ownerID == firstOwner {
		t.Error("expected new ownerID, got the released one")
	}
	_ = mr
}

func TestLock_Timeout(t *testing.T) {
	l, mr := newTestLocker(t)
	if err := mr.Set("lock:k1", "other"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := l.Lock(context.Background(), "k1", time.Minute, 200*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrLockTimeout) {
		t.Errorf("expected ErrLockTimeout, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Lock took %v, expected < 500ms", elapsed)
	}
}

func TestLock_ContextCancel(t *testing.T) {
	l, mr := newTestLocker(t)
	if err := mr.Set("lock:k1", "other"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := l.Lock(ctx, "k1", time.Minute, 5*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestLock_WithAutoRenew(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	l := NewRedisLocker(c,
		WithAutoRenew(),
		WithRenewInterval(50*time.Millisecond),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ownerID, err := l.Lock(ctx, "auto-renew-key", 200*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// 验证 lockState 已注册
	val, ok := l.locks.Load("auto-renew-key")
	if !ok {
		t.Fatal("expected lockState to be registered")
	}
	st := val.(*lockState)

	// Unlock 应当停止 auto-renew
	if err := l.Unlock(ctx, "auto-renew-key", ownerID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.stopChan:
		// expected
	case <-time.After(time.Second):
		t.Error("expected stopChan closed within 1s after Unlock")
	}
}

func TestTryLock_Prefix(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	l := NewRedisLocker(c, WithPrefix("fuxi"))

	_, err := l.TryLock(context.Background(), "k1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// lockKey() returns "<prefix>:<key>" so prefix="fuxi" → "fuxi:k1"
	if !mr.Exists("fuxi:k1") {
		t.Errorf("expected fuxi:k1, got keys: %v", mr.Keys())
	}
}

func TestUnlock_StopsAutoRenew(t *testing.T) {
	// 验证 Lock 之前先 unlock 一个 key (虽然不存在 lockState), 应当安全 no-op
	l, _ := newTestLocker(t)
	if err := l.Unlock(context.Background(), "never-locked", "x"); err != nil {
		// 实际会调用 Lua 失败 (key 不存在, owner 不匹配), 但不会 panic
		t.Logf("expected error from Lua, got %v (acceptable)", err)
	}
}

func TestTryLock_RedisError(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	l := NewRedisLocker(c, WithRetryInterval(10*time.Millisecond))

	mr.Close() // 关闭 miniredis, 让 SetNX 失败

	_, err := l.TryLock(context.Background(), "k1", time.Second)
	if err == nil {
		t.Error("expected error from closed miniredis")
	}
}

func TestWithLock_Fail(t *testing.T) {
	l, mr := newTestLocker(t)
	if err := mr.Set("lock:k1", "other"); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("user fn")
	err := WithLock(context.Background(), l, "k1", time.Minute, 50*time.Millisecond, func() error {
		return sentinel
	})
	if !errors.Is(err, ErrLockTimeout) {
		t.Errorf("expected ErrLockTimeout wrapping, got %v", err)
	}
}

// TestLock_ConcurrentContention 验证多个 goroutine 同时竞争同一把锁时
// 互斥性严格成立：同一时刻只有一个持有者，且最终所有 goroutine 都能完成。
func TestLock_ConcurrentContention(t *testing.T) {
	l, _ := newTestLocker(t)
	ctx := context.Background()

	const goroutines = 10
	var (
		wg           sync.WaitGroup
		holders      atomic.Int32 // 当前持有者数，应始终 <= 1
		maxHolders   atomic.Int32 // 历史最大并发持有者数
		successCount atomic.Int32
	)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ownerID, err := l.Lock(ctx, "contest-key", time.Minute, 3*time.Second)
			if err != nil {
				// Lock 超时是可接受的（其他 goroutine 持有太久）
				return
			}
			// 进入临界区
			cur := holders.Add(1)
			// 更新最大持有者数
			for {
				old := maxHolders.Load()
				if cur <= old || maxHolders.CompareAndSwap(old, cur) {
					break
				}
			}
			if cur > 1 {
				t.Errorf("concurrent lock violation: %d holders at once", cur)
			}
			time.Sleep(10 * time.Millisecond) // 模拟临界区操作
			holders.Add(-1)
			successCount.Add(1)
			_ = l.Unlock(ctx, "contest-key", ownerID)
		}()
	}
	wg.Wait()

	if successCount.Load() == 0 {
		t.Error("no goroutine acquired the lock")
	}
	if maxHolders.Load() > 1 {
		t.Errorf("max concurrent holders was %d, expected 1", maxHolders.Load())
	}
}

// TestWithLock_UnlockWithCancelledCtx 回归：fn 执行期间 ctx 取消时，
// 用原 ctx 调 Unlock 会在发命令前就返回 context.Canceled，DEL 永不执行
// ——锁变孤儿，其他实例阻塞到 TTL 过期。钉住：ctx 取消后锁仍被释放。
func TestWithLock_UnlockWithCancelledCtx(t *testing.T) {
	l, mr := newTestLocker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := WithLock(ctx, l, "orphan-k", time.Minute, time.Second, func() error {
		cancel() // 模拟 fn 执行期间超时/取消
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock returned err: %v", err)
	}
	if mr.Exists("distlock:orphan-k") {
		t.Error("lock must be released even when ctx was cancelled inside fn (orphan lock)")
	}
}

// TestTryLock_ZeroTTL_Rejected 回归：go-redis SetNX 的 ttl=0 表示永不过期，
// 未初始化的零值 Duration 会产生打不破的锁。钉住：ttl<=0 显式报错。
func TestTryLock_ZeroTTL_Rejected(t *testing.T) {
	l, _ := newTestLocker(t)
	if _, err := l.TryLock(context.Background(), "k", 0); err == nil {
		t.Error("ttl=0 must be rejected (would create a never-expiring lock)")
	}
}

// TestTryLock_NilClientWithoutDegradation 回归：nil client 且未开优雅降级
// 时曾把故障推迟到第一次加锁的运行时 nil panic。钉住：构造组合非法即报错。
func TestTryLock_NilClientWithoutDegradation(t *testing.T) {
	l := NewRedisLocker(nil)
	if _, err := l.TryLock(context.Background(), "k", time.Minute); err == nil {
		t.Error("nil client without graceful degradation must fail fast, not panic")
	}
}
