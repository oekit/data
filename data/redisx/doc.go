// Package redisx 提供 Redis 客户端封装。
//
// 所有 enjoydream 后端服务共享此包，统一封装：key 前缀管理、OTel 追踪。
//
// # 核心特性
//
//   - key 前缀自动添加（prefix:key 格式）
//   - OpenTelemetry 追踪自动注入
//   - 支持 redis.UniversalClient（兼容单机、集群、哨兵模式）
//   - 提供 Client() 方法获取原生客户端（用于 distlock 等场景）
//
// # 快速接入
//
// 从地址创建：
//
//	client, err := redisx.NewClientFromAddr("localhost:6379", "", 0,
//	    redisx.WithPrefix("myservice"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
// 从配置创建（支持集群）：
//
//	opt := &redis.UniversalOptions{
//	    Addrs:    []string{"redis-1:6379", "redis-2:6379"},
//	    Password: "password",
//	    DB:       0,
//	}
//	client, err := redisx.NewClient(opt, redisx.WithPrefix("myservice"))
//
// # Key 前缀
//
// 所有操作的 key 会自动添加 "prefix:" 前缀：
//
//	client.Set(ctx, "user:123", value, 0)
//	// 实际存储的 key 为 "myservice:user:123"
//
// # 原生客户端
//
// 需要原生 redis.UniversalClient 时（如 distlock、cache 等包）：
//
//	locker := distlock.NewRedisLocker(client.Client(), distlock.WithPrefix("myservice"))
package redisx
