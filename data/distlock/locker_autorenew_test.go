package distlock

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// --- M3: 自动续期最大生命周期 ---

// TestWithMaxAutoRenew 验证选项注入。
func TestWithMaxAutoRenew(t *testing.T) {
	l := NewRedisLocker(nil, WithMaxAutoRenew(30*time.Second))
	if l.maxAutoRenew != 30*time.Second {
		t.Errorf("expected 30s, got %v", l.maxAutoRenew)
	}
}

// TestStartAutoRenew_MaxLifetimeDefaultIsTenTTL 验证未设置 WithMaxAutoRenew 时
// 默认取 10×ttl（懒计算逻辑经由 startAutoRenew 内部，不直接暴露字段）。
// 这里只验证负值 = 不限制的选项语义被原样保留。
func TestStartAutoRenew_MaxLifetimeUnlimited(t *testing.T) {
	l := NewRedisLocker(nil, WithMaxAutoRenew(-1*time.Second))
	if l.maxAutoRenew >= 0 {
		t.Errorf("expected negative (unlimited), got %v", l.maxAutoRenew)
	}
}

// TestAutoRenew_MaxLifetimeExpiry 回归测试（M3）：忘记 Unlock 时，续期
// goroutine 必须在最大生命周期后退出，锁在 TTL 后自然过期 ——
// 此前 context.Background() 无限续期 = 永久锁 + goroutine 泄漏。
func TestAutoRenew_MaxLifetimeExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()

	l := NewRedisLocker(c,
		WithAutoRenew(),
		WithRenewInterval(20*time.Millisecond),
		WithMaxAutoRenew(150*time.Millisecond), // 远小于后续等待
	)
	ctx := context.Background()

	ownerID, err := l.Lock(ctx, "m3-key", time.Second, time.Second)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !mr.Exists("lock:m3-key") {
		t.Fatal("expected lock key to exist")
	}

	// 越过最大生命周期（150ms）后再等一会儿，让最后一次续期与 goroutine 退出完成
	time.Sleep(400 * time.Millisecond)

	// 此刻续期必须已停止：快进 miniredis 时钟越过剩余 TTL，key 应过期。
	// （若无 max lifetime，续期 goroutine 会以 20ms 间隔持续 pexpire，
	// key 永不过期。）
	mr.FastForward(3 * time.Second)
	if mr.Exists("lock:m3-key") {
		t.Error("expected lock to expire after max auto-renew lifetime (renewal must stop)")
	}

	// 后续 Unlock 仍应安全（状态可清理，Lua 返回 0 不误删他人锁）
	if uerr := l.Unlock(ctx, "m3-key", ownerID); uerr != nil {
		t.Errorf("Unlock after expiry: %v", uerr)
	}
}

// TestAutoRenew_UnlimitedKeepsLock 验证负值 = 不限制（兼容旧行为）：
// 在观察窗口内锁始终被续期保活。
func TestAutoRenew_UnlimitedKeepsLock(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()

	l := NewRedisLocker(c,
		WithAutoRenew(),
		WithRenewInterval(20*time.Millisecond),
		WithMaxAutoRenew(-1*time.Second), // 不限制
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ownerID, err := l.Lock(ctx, "unlimited-key", 200*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// 观察 300ms：TTL 只有 200ms，若续期停止 key 会过期；
	// 不限制模式下应始终存活。
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !mr.Exists("lock:unlimited-key") {
			t.Fatal("expected lock to be kept alive with unlimited renew (negative option)")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 收尾：解锁并等待续期 goroutine 退出，避免泄漏影响后续测试
	if uerr := l.Unlock(ctx, "unlimited-key", ownerID); uerr != nil {
		t.Errorf("Unlock: %v", uerr)
	}
}

// --- M4: Unlock 的 LoadAndDelete-first 竞态修复 ---

// TestLocker_StaleOwnerUnlockDoesNotDeleteNewState 回归测试（M4）：
// 陈旧持有者 Unlock（LoadAndDelete 已把新持有者状态取出）后必须回填，
// 新持有者的续期状态不得丢失或被误杀。
func TestLocker_StaleOwnerUnlockDoesNotDeleteNewState(t *testing.T) {
	l := NewRedisLocker(nil, WithAutoRenew(), WithGracefulDegradation())

	newOwnerState := &lockState{ownerID: "owner-B", stopChan: make(chan struct{})}
	l.locks.Store("test-key", newOwnerState)

	// 陈旧持有者 A 解锁
	_ = l.Unlock(context.TODO(), "test-key", "owner-A")

	// 新持有者 B 的状态必须被回填且未被停止
	val, ok := l.locks.Load("test-key")
	if !ok {
		t.Fatal("expected new owner's state to be restored after stale Unlock")
	}
	if val.(*lockState) != newOwnerState {
		t.Error("expected exactly the new owner's state to be restored")
	}
	select {
	case <-newOwnerState.stopChan:
		t.Error("stale Unlock must not close new owner's stopChan")
	default:
	}

	// 真正的持有者 B 解锁后状态才被清理
	_ = l.Unlock(context.TODO(), "test-key", "owner-B")
	if _, ok := l.locks.Load("test-key"); ok {
		t.Error("expected state removed after real owner's Unlock")
	}
	select {
	case <-newOwnerState.stopChan:
	default:
		t.Error("expected stopChan closed after real owner's Unlock")
	}
}

// TestLocker_StaleUnlockDoesNotOverwriteThirdState 验证 M4 修复的回填约束：
// 陈旧持有者 LoadAndDelete 与回填之间若已有第三个状态存入（他人重新获锁），
// 回填不得覆盖第三个状态（LoadOrStore 语义）。
func TestLocker_StaleUnlockDoesNotOverwriteThirdState(t *testing.T) {
	l := NewRedisLocker(nil, WithAutoRenew(), WithGracefulDegradation())

	staleState := &lockState{ownerID: "owner-A", stopChan: make(chan struct{})}
	l.locks.Store("test-key", staleState)

	// 模拟竞态窗口：直接调用底层的 LoadAndDelete（模拟 Unlock 第一步）
	v, ok := l.locks.LoadAndDelete("test-key")
	if !ok || v.(*lockState) != staleState {
		t.Fatal("expected to load stale state")
	}

	// 窗口期内第三个持有者存入新状态
	thirdState := &lockState{ownerID: "owner-C", stopChan: make(chan struct{})}
	l.locks.Store("test-key", thirdState)

	// 陈旧持有者的回填路径（Unlock 内部用 LoadOrStore）——
	// 通过 Unlock 触发完整路径：此时 ownerID 匹配的是我们取出的 staleState
	_ = l.Unlock(context.TODO(), "test-key", "owner-A")

	// 第三个状态必须原样保留
	val, ok := l.locks.Load("test-key")
	if !ok {
		t.Fatal("expected third state to survive stale Unlock's restore")
	}
	if val.(*lockState) != thirdState {
		t.Error("expected third state not to be overwritten by stale restore")
	}
	select {
	case <-thirdState.stopChan:
		t.Error("third owner's renewal must not be stopped by stale Unlock")
	default:
	}
}

// TestLocker_StaleUnlockRace 验证多 goroutine 并发陈旧 Unlock 时状态不被误杀
// （-race 覆盖，M4 回归）。
func TestLocker_StaleUnlockRace(t *testing.T) {
	l := NewRedisLocker(nil, WithAutoRenew(), WithGracefulDegradation())
	ctx := context.TODO()

	newOwnerState := &lockState{ownerID: "owner-B", stopChan: make(chan struct{})}
	l.locks.Store("race-key", newOwnerState)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Unlock(ctx, "race-key", "stale-owner")
		}()
	}
	wg.Wait()

	// 所有陈旧 Unlock 结束后：新持有者状态必须仍然在（回填保持）且未被停止
	val, ok := l.locks.Load("race-key")
	if !ok {
		t.Fatal("expected new owner's state to survive concurrent stale unlocks")
	}
	if val.(*lockState) != newOwnerState {
		t.Error("expected restored state to be the new owner's state")
	}
	select {
	case <-newOwnerState.stopChan:
		t.Error("new owner's renewal must not be stopped by stale unlocks")
	default:
	}

	// 真正持有者解锁收尾
	_ = l.Unlock(ctx, "race-key", "owner-B")
}
