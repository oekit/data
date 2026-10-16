// Package cache 提供泛型缓存门面。
//
// 定义 Cache[T] 和 CacheAside[T] 接口，支持多种后端实现：
//   - LocalCache[T]（本包）: 基于内存的本地缓存（LRU、MaxSize、TTL）
//   - SingleFlightCache[T]（本包）: 装饰器，防缓存击穿
//   - RedisCache[T]（子包 data/cache/redis）: 基于 go-redis 的分布式缓存；
//     拆分子包后本包不传递编译依赖 go-redis，用 LocalCache 的服务无需引入 Redis SDK
package cache
