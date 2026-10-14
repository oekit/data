package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Client 是带 key 前缀和 OTel 追踪的 Redis 客户端封装。
type Client struct {
	prefix string
	client redis.UniversalClient
}

// RedisClientOption 配置 Client 的可选参数。
type RedisClientOption struct {
	Prefix string
}

// RedisClientOptionFunc 配置函数类型。
type RedisClientOptionFunc func(*RedisClientOption)

// WithPrefix 设置 key 前缀。
// 所有操作的 key 会自动添加 "prefix:" 前缀。
func WithPrefix(prefix string) RedisClientOptionFunc {
	return func(o *RedisClientOption) { o.Prefix = prefix }
}

// NewClient 从 UniversalOptions 创建 Redis 客户端。
// 自动注入 OpenTelemetry 追踪。
func NewClient(opt *redis.UniversalOptions, opts ...RedisClientOptionFunc) (*Client, error) {
	o := &RedisClientOption{}
	for _, fn := range opts {
		fn(o)
	}

	// 生产默认值（调用方显式设置的字段不覆盖）：
	// 拨号/读写超时收紧故障感知窗口；池超时防止慢 Redis 拖垮 worker；
	// 最小空闲连接削峰重拨。go-redis 对未设 PoolSize 默认 10*GOMAXPROCS，
	// 此处不越俎代庖。
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 3 * time.Second
	}
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 2 * time.Second
	}
	if opt.WriteTimeout == 0 {
		opt.WriteTimeout = 2 * time.Second
	}
	if opt.PoolTimeout == 0 {
		opt.PoolTimeout = 3 * time.Second
	}
	if opt.MinIdleConns == 0 {
		opt.MinIdleConns = 2
	}

	client := redis.NewUniversalClient(opt)

	if err := redisotel.InstrumentTracing(client); err != nil {
		// 失败路径必须回收已拨号的连接池，否则每次构造失败泄漏一个 client
		_ = client.Close()
		return nil, err
	}

	return &Client{
		prefix: o.Prefix,
		client: client,
	}, nil
}

// NewClientFromAddr 从地址字符串创建 Redis 客户端（便捷方法）。
func NewClientFromAddr(addr, password string, db int, opts ...RedisClientOptionFunc) (*Client, error) {
	opt := &redis.UniversalOptions{
		Addrs:    []string{addr},
		Password: password,
		DB:       db,
	}
	return NewClient(opt, opts...)
}

// Close 关闭底层 Redis 连接。
func (c *Client) Close() error {
	return c.client.Close()
}

// Ping 测试连接。
func (c *Client) Ping(ctx context.Context) *redis.StatusCmd {
	return c.client.Ping(ctx)
}

// Client 返回底层 redis.UniversalClient（用于 distlock 等需要原生客户端的场景）。
func (c *Client) Client() redis.UniversalClient {
	return c.client
}

func (c *Client) convertKey(key string) string {
	if c.prefix == "" {
		return key
	}
	return c.prefix + ":" + key
}

func (c *Client) convertKeys(keys []string) []string {
	if c.prefix == "" {
		return keys
	}
	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = c.convertKey(key)
	}
	return result
}
