package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// ==================== String 操作 ====================

// Get 获取 key 的值。
func (c *Client) Get(ctx context.Context, key string) *redis.StringCmd {
	return c.client.Get(ctx, c.convertKey(key))
}

// Set 设置 key 的值。
func (c *Client) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	return c.client.Set(ctx, c.convertKey(key), value, expiration)
}

// SetNX 仅当 key 不存在时设置。
func (c *Client) SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd {
	return c.client.SetNX(ctx, c.convertKey(key), value, expiration)
}

// ==================== Key 操作 ====================

// Del 删除一个或多个 key。
func (c *Client) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	return c.client.Del(ctx, c.convertKeys(keys)...)
}

// Exists 检查 key 是否存在。
func (c *Client) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	return c.client.Exists(ctx, c.convertKeys(keys)...)
}

// Expire 设置 key 的过期时间。
func (c *Client) Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	return c.client.Expire(ctx, c.convertKey(key), expiration)
}

// ==================== 计数器操作 ====================

// Incr 将 key 的值加 1。
func (c *Client) Incr(ctx context.Context, key string) *redis.IntCmd {
	return c.client.Incr(ctx, c.convertKey(key))
}

// IncrBy 将 key 的值增加指定增量。
func (c *Client) IncrBy(ctx context.Context, key string, value int64) *redis.IntCmd {
	return c.client.IncrBy(ctx, c.convertKey(key), value)
}

// Decr 将 key 的值减 1。
func (c *Client) Decr(ctx context.Context, key string) *redis.IntCmd {
	return c.client.Decr(ctx, c.convertKey(key))
}

// DecrBy 将 key 的值减少指定减量。
func (c *Client) DecrBy(ctx context.Context, key string, value int64) *redis.IntCmd {
	return c.client.DecrBy(ctx, c.convertKey(key), value)
}
