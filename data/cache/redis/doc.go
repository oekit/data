// Package redis 提供基于 go-redis 的 Cache[T] 分布式缓存实现。
//
// 与根包内存实现（LocalCache）的关系：根包 data/cache 只保留接口
// （Cache/CacheAside/SourceFunc）与内存实现，本子包是 Redis 后端——
// import 根包不再传递编译依赖 go-redis。
//
//	import (
//		cache "github.com/yi-nology/ekit-data/data/cache"
//		rediscache "github.com/yi-nology/ekit-data/data/cache/redis"
//	)
//
//	c := rediscache.NewRedisCache[User](rdb, rediscache.RedisConfig[User]{
//	    Prefix: "app:user:",
//	    TTL:    10 * time.Minute,
//	    Source: loadUserFromDB, // Cache-Aside 未命中回源
//	})
//	// c 满足 cache.Cache[User] / cache.CacheAside[User]
//
// 空值缓存语义（防穿透）见 RedisConfig.NullTTL 字段注释，与 LocalCache
// 保持一致：0 = 默认开启（1 分钟），负值 = 显式禁用。
package redis
