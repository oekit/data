package redisx

import (
	"context"
	"fmt"
)

// RedisConfig 是 Redis 连接配置，可直接从 YAML 反序列化。
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// NewClientFromConfig 从配置创建 Redis 客户端（含 OTel tracing）。
//
// 用法：
//
//	client, err := redis.NewClientFromConfig(ctx, redis.RedisConfig{
//	    Host: cfg.Redis.Host, Port: cfg.Redis.Port,
//	    Password: cfg.Redis.Password, DB: cfg.Redis.DB,
//	})
func NewClientFromConfig(ctx context.Context, cfg RedisConfig) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := NewClientFromAddr(addr, cfg.Password, cfg.DB)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx).Err(); err != nil {
		// Ping 失败也要回收已创建的 client（连接池已拨号）
		_ = client.Client().Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return client, nil
}
