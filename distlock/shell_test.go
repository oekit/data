package distlock_test

import (
	"errors"
	"testing"
	"time"

	"github.com/oekit/data/distlock"
	"github.com/oekit/data/internal/testsame"
	pkgdistlock "github.com/oekit/pkg/distlock"
	redisxdistlock "github.com/oekit/redisx/distlock"
)

// TestTypeIdentity 哨兵值同一性 + 构造 + 接口满足。
func TestTypeIdentity(t *testing.T) {
	if !errors.Is(distlock.ErrLockNotAcquired, redisxdistlock.ErrLockNotAcquired) {
		t.Fatal("ErrLockNotAcquired 应与源包同一")
	}
	if !errors.Is(distlock.ErrLockTimeout, redisxdistlock.ErrLockTimeout) {
		t.Fatal("ErrLockTimeout 应与源包同一")
	}

	l := distlock.NewRedisLocker(nil)
	testsame.Assert[redisxdistlock.Locker](l) // 源接口满足

	// Semaphore alias 同一性：壳构造结果可直接赋给源包指针类型（编译期检查）。
	semIdentity(distlock.NewSemaphore(nil, "quota", 1, time.Second))

	// Lockout alias 同一性：壳 LockoutConfig 可直接喂给源包构造器，壳构造结果可赋给源包指针类型。
	cfg := distlock.LockoutConfig{Threshold: 3, Window: time.Minute, Penalty: time.Minute}
	lockoutIdentity(distlock.NewLockout(nil, cfg))
	lockoutIdentity(redisxdistlock.NewLockout(nil, cfg))
}

// semIdentity 仅供编译期 alias 同一性断言：实参必须可赋给源包指针类型。
func semIdentity(_ *redisxdistlock.Semaphore) {}

// lockoutIdentity 仅供编译期 alias 同一性断言：实参必须可赋给源包指针类型。
func lockoutIdentity(_ *redisxdistlock.Lockout) {}

// TestPkgDistlockContractIdentity（#195）契约单源机器钉：门面哨兵必须与
// pkg/distlock 的契约哨兵同值（errors.Is 跨包成立），Locker 必须与
// pkg/distlock.Locker 同一命名类型——后端无关消费方换后端（etcdx/redis）
// 判定结果不得漂移。redisx 依赖地板回退到迁移提交（e082600）之前时本
// 测试必红，杜绝「errors.Is 静默断裂」再次漏跟。
func TestPkgDistlockContractIdentity(t *testing.T) {
	if !errors.Is(distlock.ErrLockNotAcquired, pkgdistlock.ErrLockNotAcquired) {
		t.Fatal("ErrLockNotAcquired 与 pkg/distlock 契约哨兵不同值（errors.Is 判不出）")
	}
	if !errors.Is(distlock.ErrLockTimeout, pkgdistlock.ErrLockTimeout) {
		t.Fatal("ErrLockTimeout 与 pkg/distlock 契约哨兵不同值（errors.Is 判不出）")
	}
	if !errors.Is(distlock.ErrLockNotHeld, pkgdistlock.ErrLockNotHeld) {
		t.Fatal("ErrLockNotHeld 与 pkg/distlock 契约哨兵不同值（errors.Is 判不出「已丢锁」）")
	}
	l := distlock.NewRedisLocker(nil)
	testsame.Assert[pkgdistlock.Locker](l) // 门面 Locker 即 pkg 契约 Locker
}
