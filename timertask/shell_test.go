package timertask_test

import (
	"context"
	"errors"
	"testing"

	"github.com/oekit/data/internal/testsame"
	"github.com/oekit/data/timertask"
	timertasksrc "github.com/oekit/timertask"
)

// TestTypeIdentity Handler/Task 类型双向赋值。
func TestTypeIdentity(t *testing.T) {
	h := timertask.Handler(func(ctx context.Context, task *timertask.Task) error {
		return nil
	})
	testsame.Assert[timertasksrc.Handler](h)

	task := timertask.Task{ID: "shell"}
	testsame.Assert[*timertasksrc.Task](&task)
}

// TestDeliveryModeConstIdentity（#196）投递语义常量：壳此前漏跟（转发了
// DeliveryMode 类型与 WithDeliveryMode，却没转发两个取值常量——门面侧
// 写不出 WithDeliveryMode(DeliveryAtLeastOnce)）。
func TestDeliveryModeConstIdentity(t *testing.T) {
	if timertask.DeliveryAtMostOnce != timertasksrc.DeliveryAtMostOnce {
		t.Fatal("DeliveryAtMostOnce 与源包不是同一取值")
	}
	if timertask.DeliveryAtLeastOnce != timertasksrc.DeliveryAtLeastOnce {
		t.Fatal("DeliveryAtLeastOnce 与源包不是同一取值")
	}
	// 门面侧现在写得出可靠投递选项（此前只能构造出零值 AtMostOnce）。
	_ = timertask.WithDeliveryMode(timertask.DeliveryAtLeastOnce)
}

// TestRetryOutcomeConstIdentity（#196）重试终态常量：与 RetryOutcome 类型
// 一同补齐——门面消费方才能对重试终态（成功/打断/耗尽）分类。
func TestRetryOutcomeConstIdentity(t *testing.T) {
	if timertask.RetrySuccess != timertasksrc.RetrySuccess ||
		timertask.RetryInterrupted != timertasksrc.RetryInterrupted ||
		timertask.RetryExhausted != timertasksrc.RetryExhausted {
		t.Fatal("重试终态常量与源包不是同一取值")
	}
	// 终态常量必须是 RetryOutcome 类型本身（不是无类型的普通 int）。
	testsame.Assert[timertask.RetryOutcome](timertask.RetrySuccess)
	testsame.Assert[timertasksrc.RetryOutcome](timertask.RetryInterrupted)
}

// TestStartSentinelIdentity（#196）：门面转发的 LocalBackend.Start 会产出
// ErrBackendAlreadyStarted，壳必须转发该哨兵——errors.Is 才判得出
// 「并发重复 Start」（#203 已补 var 转发，本测试钉住消费方视角的判定面）。
func TestStartSentinelIdentity(t *testing.T) {
	if !errors.Is(timertask.ErrBackendAlreadyStarted, timertasksrc.ErrBackendAlreadyStarted) {
		t.Fatal("ErrBackendAlreadyStarted 与源包不是同一哨兵")
	}
	wrapped := errors.Join(timertask.ErrBackendAlreadyStarted)
	if !errors.Is(wrapped, timertask.ErrBackendAlreadyStarted) {
		t.Fatal("wrap 后 errors.Is 不命中门面哨兵")
	}
}
