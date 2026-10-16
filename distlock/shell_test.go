package distlock_test

import (
	"errors"
	"testing"

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
}
