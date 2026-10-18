package distlock_test

import (
	"errors"
	"testing"
	"time"

	"github.com/oekit/data/distlock"
	"github.com/oekit/data/internal/testsame"
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
}

// semIdentity 仅供编译期 alias 同一性断言：实参必须可赋给源包指针类型。
func semIdentity(_ *redisxdistlock.Semaphore) {}
