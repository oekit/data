// Package cache 提供泛型缓存门面。
//
// 定义 Cache[T] 和 CacheAside[T] 接口（实现契约统一收拢在 interface.go
// 的接口 godoc：值语义、(nil, nil) 双关、回填 best-effort、TTL 来源、
// 生命周期、null marker），支持多种后端实现：
//   - LocalCache[T]（本包）: 基于内存的本地缓存（写序淘汰——FIFO + 重写
//     提升，读命中不提升；MaxSize、TTL、空值防穿透）
//   - SingleFlightCache[T]（本包）: 装饰器，并发回源合并防击穿
//   - RedisCache[T]（独立模块 github.com/oekit/redis）: 基于 go-redis 的
//     分布式缓存——本包零 Redis 编译依赖，用 LocalCache 的服务无需引入
//     Redis SDK
//
// 新增后端（etcd/consul/...）实现 CacheAside[T] 后必须通过
// github.com/oekit/data/cachetest 契约测试套件（Run 全绿，含 -race）
// 方可接入。
package cache
