package redisx

import (
	"context"
	"testing"
	"time"
)

// skipIfNoRedis 跳过需要 Redis 的测试。
func skipIfNoRedis(t *testing.T) *Client {
	t.Helper()
	client, err := NewClientFromAddr("localhost:6379", "", 0)
	if err != nil {
		t.Skip("Redis not available, skipping test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available, skipping test")
	}
	return client
}

func TestBatch_SetGet(t *testing.T) {
	client := skipIfNoRedis(t)
	defer client.Close()

	ctx := context.Background()
	batch := client.NewBatch()

	// 批量 Set
	batch.Set(ctx, "batch:test1", "value1", time.Minute)
	batch.Set(ctx, "batch:test2", "value2", time.Minute)
	batch.Set(ctx, "batch:test3", "value3", time.Minute)

	// 批量 Get
	cmd1 := batch.Get(ctx, "batch:test1")
	cmd2 := batch.Get(ctx, "batch:test2")
	cmd3 := batch.Get(ctx, "batch:test3")

	// 执行
	if _, err := batch.Exec(ctx); err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	// 验证结果
	if v, err := cmd1.Result(); err != nil || v != "value1" {
		t.Errorf("expected 'value1', got %q, err: %v", v, err)
	}
	if v, err := cmd2.Result(); err != nil || v != "value2" {
		t.Errorf("expected 'value2', got %q, err: %v", v, err)
	}
	if v, err := cmd3.Result(); err != nil || v != "value3" {
		t.Errorf("expected 'value3', got %q, err: %v", v, err)
	}

	// 清理
	client.Del(ctx, "batch:test1", "batch:test2", "batch:test3")
}

func TestBatch_Len(t *testing.T) {
	client := skipIfNoRedis(t)
	defer client.Close()

	batch := client.NewBatch()
	if batch.Len() != 0 {
		t.Errorf("expected len 0, got %d", batch.Len())
	}

	ctx := context.Background()
	batch.Set(ctx, "key1", "val1", time.Minute)
	batch.Set(ctx, "key2", "val2", time.Minute)

	if batch.Len() != 2 {
		t.Errorf("expected len 2, got %d", batch.Len())
	}

	batch.Discard()
	if batch.Len() != 0 {
		t.Errorf("expected len 0 after discard, got %d", batch.Len())
	}
}

func TestCounter_IncrDecr(t *testing.T) {
	client := skipIfNoRedis(t)
	defer client.Close()

	ctx := context.Background()
	counter := client.NewCounter("counter:test")

	// 重置
	_ = counter.Reset(ctx)

	// 初始值应该是 0
	v, err := counter.Get(ctx)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if v != 0 {
		t.Errorf("expected 0, got %d", v)
	}

	// 增加
	v, err = counter.Incr(ctx, 5)
	if err != nil {
		t.Fatalf("Incr failed: %v", err)
	}
	if v != 5 {
		t.Errorf("expected 5, got %d", v)
	}

	// 减少
	v, err = counter.Decr(ctx, 2)
	if err != nil {
		t.Fatalf("Decr failed: %v", err)
	}
	if v != 3 {
		t.Errorf("expected 3, got %d", v)
	}

	// 清理
	_ = counter.Reset(ctx)
}

func TestCounter_IncrWithTTL(t *testing.T) {
	client := skipIfNoRedis(t)
	defer client.Close()

	ctx := context.Background()
	counter := client.NewCounter("counter:ttl:test")

	// 重置
	_ = counter.Reset(ctx)

	// 增加并设置 TTL
	v, err := counter.IncrWithTTL(ctx, 1, 10*time.Second)
	if err != nil {
		t.Fatalf("IncrWithTTL failed: %v", err)
	}
	if v != 1 {
		t.Errorf("expected 1, got %d", v)
	}

	// 再次增加（不应该重置 TTL）
	v, err = counter.IncrWithTTL(ctx, 1, 10*time.Second)
	if err != nil {
		t.Fatalf("IncrWithTTL failed: %v", err)
	}
	if v != 2 {
		t.Errorf("expected 2, got %d", v)
	}

	// 清理
	_ = counter.Reset(ctx)
}

func TestBloomFilter_AddExists(t *testing.T) {
	client := skipIfNoRedis(t)
	defer client.Close()

	ctx := context.Background()
	bf, err := client.NewBloomFilter(ctx, "bloom:test", 0.01, 1000)
	if err != nil {
		t.Skip("RedisBloom not available, skipping test")
	}

	// 添加元素
	added, err := bf.Add(ctx, "item1")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !added {
		t.Error("expected item1 to be added")
	}

	// 再次添加（应该返回 false）
	added, err = bf.Add(ctx, "item1")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if added {
		t.Error("expected item1 to not be added again")
	}

	// 检查存在
	exists, err := bf.Exists(ctx, "item1")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("expected item1 to exist")
	}

	// 检查不存在
	exists, err = bf.Exists(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected nonexistent to not exist")
	}
}
