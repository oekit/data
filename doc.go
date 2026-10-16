// Package data 是 oekit 数据访问门面——复刻 ekit 单体库 data/ 的能力面：
// 缓存、CRUD、分布式锁、GORM、Redis、定时任务，一个 import 前缀直达。
//
// 八个子包是纯 re-export 壳：类型为泛型 type alias（与源包完全同一类型，
// 双向赋值互通），实现分别住在 pkg/cache 与 gormx/redisx/timertask 各子仓；
// 门面只做命名空间聚合，不持有实现。只使用单一能力时可直接依赖对应子仓
// （依赖面最小）；全家桶场景 go get github.com/oekit/data 即可。
//
// 两个例外是本仓原生子包（组合 crud + pkg/cache/redisx 的既有接口，
// 不引入新底层依赖）：cached（缓存读写编排，v0.6.0 起）——把 cache-aside
// 语义（写后失效、读未命中回源回填、null marker 防穿透）套在 gormx/crud
// 仓储上，事务路径在提交后统一失效，另含 GetByIDs 批量点读与 bloom
// 防穿透可选件；twolevel（两级缓存 + 失效广播，v0.7.0 起）——L1 本地 +
// L2 远端读穿写透，多实例经 Redis pubsub 广播失效驱逐各实例 L1。
//
// 子包地图（目标 ← 实现仓）：
//
//	cache/      ← pkg/cache            缓存接口 + LocalCache/SingleFlightCache + 键构造 + Checker
//	rediscache/ ← redisx/cache         Redis 分布式缓存（CacheAside 的 Redis 后端）
//	crud/       ← gormx/crud           框架无关 CRUD Service/仓储
//	distlock/   ← redisx/distlock      分布式锁
//	gormx/      ← gormx                SQL/GORM 增强
//	redisx/     ← redisx               Redis 客户端封装
//	timertask/  ← timertask            分片定时任务
//	cachetest/  ← pkg/cache/cachetest  缓存实现契约测试套件
//	cached/     ← 本仓实现             缓存读写编排（仓储装饰器 + GetByIDs/bloom，见其包文档）
//	twolevel/   ← 本仓实现             两级缓存组合 + 跨实例失效广播（见其包文档）
//
// 门面跟版约定：八个壳子包在源仓新增导出符号时需同步（dependabot 只升
// pin 不补壳）；接口与契约的实现演进以源仓 README 为准；cached/twolevel
// 的演进以本仓 README 为准。
package data
