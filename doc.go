// Package data 是 oekit 数据访问门面——复刻 ekit 单体库 data/ 的能力面：
// 缓存、CRUD、分布式锁、GORM、Redis、定时任务，一个 import 前缀直达。
//
// 八个子包是纯 re-export 壳：类型为泛型 type alias（与源包完全同一类型，
// 双向赋值互通），实现分别住在 pkg/cache 与 gormx/redisx/timertask 各子仓；
// 门面只做命名空间聚合，不持有实现。只使用单一能力时可直接依赖对应子仓
// （依赖面最小）；全家桶场景 go get github.com/oekit/data 即可。
//
// 唯一例外是 cached：缓存读写编排层（v0.6.0 起），把 cache-aside 语义
// （写后失效、读未命中回源回填、null marker 防穿透）套在 gormx/crud
// 仓储上——增删改写库后失效缓存，查询未命中构建缓存；事务路径在提交后
// 统一失效。实现住本仓（组合 crud + pkg/cache 接口，无新底层依赖）。
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
//	cached/     ← 本仓实现             缓存读写编排（仓储装饰器，见其包文档）
//
// 门面跟版约定：八个壳子包在源仓新增导出符号时需同步（dependabot 只升
// pin 不补壳）；接口与契约的实现演进以源仓 README 为准；cached 的演进
// 以本仓 README 为准。
package data
