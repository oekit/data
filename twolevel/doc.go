// Package twolevel 是 oekit 的两级缓存组合件：L1 本地缓存 + L2 任意
// CacheAside 远端缓存（典型为 redisx/cache.RedisCache）的读穿写透装饰器，
// 以及跨实例 L1 失效广播（Bus，Redis pubsub）。
//
// # TwoLevelCache 语义分层
//
//   - L1 只缓存真值（命中即返回，不打 L2）；null 防穿透完全委托 L2 的
//     null marker（Source 返回 (nil, nil) 时 marker 落在 L2，后续
//     GetOrLoad 每次 L1 miss 后查一跳 L2 即被 marker 挡住，不回源）；
//   - GetOrLoad/Get 在 L2 命中后回填 L1（TTL 归 L1 自身配置）；
//   - Set/SetWithTTL 写穿两级；SetWithTTL 的显式 TTL 同时作用于 L1 与
//     L2（否则 L1 长驻会吞掉显式短 TTL 的过期语义）；
//   - Del/DelBatch 双删两级，顺序固定先远端后本地：远端失败时本地已删，
//     读路径回填的是远端旧值（短窗口，TTL 兜底）；反序则本地旧值会活过
//     整个 L1 TTL；
//   - Refresh 走 L2 删除重载（marker 一并清除），新值回填 L1。
//
// # Bus 失效广播（多实例部署必备）
//
// 单实例的 Del 只能删掉自己的 L1——其他实例的 L1 副本要靠 TTL 才能过期。
// Bus 在 Del/DelBatch 时向 Redis 频道发布失效消息，各实例的订阅循环收到
// 后驱逐自己的 L1 副本（含发布者自身，幂等）。只广播失效、不广播回填：
// Set/回填意味着 L2 有了新值，他实例旧 L1 由下一次写路径广播驱逐或 TTL
// 兜底——这与 cache-aside 的整体语义一致。
//
// # 与 cached 编排的组合
//
// TwoLevelCache 实现 CacheAside[T]，可直接作为 cached.NewRepository 后端
// 工厂的产物：cached 管「写后失效编排」，twolevel 管「后端形态」——
//
//	repo := cached.NewRepository[User](rawRepo, func(src cache.SourceFunc[User]) cache.CacheAside[User] {
//	    return twolevel.NewTwoLevelCache(localL1, redisL2With(src), twolevel.WithBus[User](bus))
//	}, ...)
//
// cached 写路径的 DelBatch 会经 TwoLevelCache 双删两级并广播给全部实例。
//
// 值语义沿用 pkg/cache 实现契约：返回值一律只读对待（L1 共享内部引用，
// L2 序列化型后端每次新副本）；回填 best-effort，失败记日志不阻断。
//
// # TTL 前提（务必遵守）
//
// 上述一切"短窗口 + TTL 兜底"的论证都建立在 **L1/L2 TTL 均为有限值**
// 之上：LocalCache 的 TTL=0 表示永不过期——多实例部署下丢失一条失效
// 广播，该键的 L1 陈旧就不再有界。两级缓存场景禁止 TTL=0。
package twolevel
