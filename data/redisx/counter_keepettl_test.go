package redisx

import (
	"context"
	"testing"
	"time"
)

// TestCounter_DecrFloor_KeepsTTL 回归测试（M8）：DecrFloor 钳底回写使用
// SET ... KEEPTTL —— 此前裸 SET 清掉 TTL，经 IncrWithTTL 创建的配额键
// 变成永不过期的永久计数器。
func TestCounter_DecrFloor_KeepsTTL(t *testing.T) {
	c, _ := newTestClient(t, "")
	ctx := context.Background()
	counter := c.NewCounter("quota")

	// 创建带 TTL 的配额键
	if _, err := counter.IncrWithTTL(ctx, 5, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	ttl, err := c.Client().TTL(ctx, "quota").Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("expected positive TTL after IncrWithTTL, got %v err=%v", ttl, err)
	}

	// DecrFloor 触发钳底（5 - 10 → 钳到 0）
	val, err := counter.DecrFloor(ctx, 10, 0)
	if err != nil {
		t.Fatalf("DecrFloor: %v", err)
	}
	if val != 0 {
		t.Errorf("expected floored value 0, got %d", val)
	}

	// 回归断言：TTL 必须保留（此前裸 SET 清掉 TTL → -1）
	ttlAfter, err := c.Client().TTL(ctx, "quota").Result()
	if err != nil {
		t.Fatalf("TTL after DecrFloor: %v", err)
	}
	if ttlAfter <= 0 {
		t.Errorf("expected TTL preserved after DecrFloor clamp, got %v (永久键回归)", ttlAfter)
	}

	// 不触发钳底时不重写键，TTL 也不应被清除
	if _, err = counter.IncrWithTTL(ctx, 3, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = counter.DecrFloor(ctx, 1, 0); err != nil {
		t.Fatal(err)
	}
	ttlNormal, err := c.Client().TTL(ctx, "quota").Result()
	if err != nil || ttlNormal <= 0 {
		t.Errorf("expected TTL preserved on non-clamp path, got %v err=%v", ttlNormal, err)
	}
}
