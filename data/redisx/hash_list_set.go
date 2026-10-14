package redisx

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// ==================== Hash 操作 ====================

// HGet 获取 hash 中指定字段的值。
func (c *Client) HGet(ctx context.Context, key, field string) *redis.StringCmd {
	return c.client.HGet(ctx, c.convertKey(key), field)
}

// HSet 设置 hash 中的一个或多个字段。
func (c *Client) HSet(ctx context.Context, key string, values ...any) *redis.IntCmd {
	return c.client.HSet(ctx, c.convertKey(key), values...)
}

// HDel 删除 hash 中的一个或多个字段。
func (c *Client) HDel(ctx context.Context, key string, fields ...string) *redis.IntCmd {
	return c.client.HDel(ctx, c.convertKey(key), fields...)
}

// HGetAll 获取 hash 中所有字段和值。
func (c *Client) HGetAll(ctx context.Context, key string) *redis.MapStringStringCmd {
	return c.client.HGetAll(ctx, c.convertKey(key))
}

// ==================== List 操作 ====================

// LPush 将一个或多个值插入列表头部。
func (c *Client) LPush(ctx context.Context, key string, values ...any) *redis.IntCmd {
	return c.client.LPush(ctx, c.convertKey(key), values...)
}

// RPush 将一个或多个值插入列表尾部。
func (c *Client) RPush(ctx context.Context, key string, values ...any) *redis.IntCmd {
	return c.client.RPush(ctx, c.convertKey(key), values...)
}

// LPop 移除并返回列表头部元素。
func (c *Client) LPop(ctx context.Context, key string) *redis.StringCmd {
	return c.client.LPop(ctx, c.convertKey(key))
}

// RPop 移除并返回列表尾部元素。
func (c *Client) RPop(ctx context.Context, key string) *redis.StringCmd {
	return c.client.RPop(ctx, c.convertKey(key))
}

// LRange 获取列表指定范围的元素。
func (c *Client) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	return c.client.LRange(ctx, c.convertKey(key), start, stop)
}

// LLen 获取列表长度。
func (c *Client) LLen(ctx context.Context, key string) *redis.IntCmd {
	return c.client.LLen(ctx, c.convertKey(key))
}

// ==================== Set 操作 ====================

// SAdd 向集合添加一个或多个成员。
func (c *Client) SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd {
	return c.client.SAdd(ctx, c.convertKey(key), members...)
}

// SMembers 获取集合所有成员。
func (c *Client) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	return c.client.SMembers(ctx, c.convertKey(key))
}

// SIsMember 判断成员是否在集合中。
func (c *Client) SIsMember(ctx context.Context, key string, member any) *redis.BoolCmd {
	return c.client.SIsMember(ctx, c.convertKey(key), member)
}

// SRem 从集合移除一个或多个成员。
func (c *Client) SRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	return c.client.SRem(ctx, c.convertKey(key), members...)
}
