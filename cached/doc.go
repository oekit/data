// Package cached 是 oekit 的缓存读写编排层：把 cache-aside 语义
// （写后失效、读未命中回源回填、null marker 防穿透）套在
// gormx/crud 仓储之上，让 CRUD 自动获得缓存一致性编排。
//
// # 语义边界（务必理解后再接入）
//
//   - 模式是 Cache-Aside（写库成功后删缓存），不是 write-through
//     双写：删缓存 + TTL 兜底是自愈的，双写存在部分失败与并发覆盖
//     两类无法根治的不一致。
//   - 点读（GetByID）默认缓存：miss → 仓储回源 → 回填；ErrNotFound
//     翻译为 (nil, nil) 交给后端缓存 null marker（防穿透）；缓存
//     命中 null marker 时返回 crud.ErrNotFound。TTL 由后端缓存的
//     默认 TTL 决定——务必配置有限 TTL，它是删缓存失败时的唯一安全网。
//   - 查询缓存（GetOne/List）经 WithQueryCache 显式开启：键 =
//     「表级版本号 + QuerySpec 指纹」，任意写操作 bump 版本让旧键
//     自然过期（改一行即全表列表失效，代价是列表命中率换一致性）。
//     GetOne/List 的负结果不缓存（普通 Cache 接口无 null marker
//     通道）。ForUpdate 读永不缓存。Exists/Count/GetByCondition
//     永远直通。
//   - 事务安全：CachedRepository 实现 TxRepository——RunInTx 事务内
//     的写操作只收集失效清单，提交成功后统一执行失效（嵌套事务同样
//     收集，由最外层提交后执行）；回滚不失效。失效必须发生在提交后：
//     事务内删缓存会放跑"回源读到未提交旧值 → 回填旧值"的竞态。
//     事务内读直通仓储（保证读到事务内状态）。提交后的失效以
//     WithoutCancel 上下文执行——调用方取消不吞失效。
//   - 失效是 best-effort：写库成功后删缓存/bump 版本失败不回滚写
//     （返回错误会让调用方误判写失败而重试），错误经 WithOnError
//     上报，TTL 兜底。缓存后端读失败时读路径降级直查仓储。
//   - 多租户/多作用域：缓存键原本**没有作用域维度** ⇒ 底层仓储按租户
//     隔离时（RLS / 每租户连接）同 ID 在不同租户下是不同的行却共用一个
//     键 ⇒ 跨租户读泄漏，且某租户「不存在」的 null marker 会污染其他
//     租户。用 WithScopeKey 注入作用域解析器（按 ctx 解析）⇒ 键变为
//     「前缀:类型:scope:<作用域>:id:N」，一个仓储实例即可服务多个租户。
//     作用域解析失败 ⇒ 读路径降级直查仓储、写路径跳过失效（TTL 兜底），
//     **绝不**回落共享键。
//
// # 别名警示（调用方必读）
//
// 缓存命中时返回的是后端缓存持有的引用：零拷贝型后端（如 LocalCache）
// 直接共享内部对象——调用方**禁止改写**返回的 *T / []T 内容，改写会
// 污染缓存并与并发读构成 data race；序列化型后端（如 RedisCache）每
// 次反序列化返回新副本，天然无别名。两类后端共用本装饰器，因此调用方
// 一律按"只读"对待返回值（契约同 github.com/oekit/pkg/cache 包级文档）。
// 注意这与裸 gormrepo 的习惯不同——仓储每次查询返回新对象，而缓存路径
// 不是。需要可变副本时自行拷贝。
//
// # 用法
//
//	repo := cached.NewRepository[User](rawRepo,
//	    func(src cache.SourceFunc[User]) cache.CacheAside[User] {
//	        return cache.NewSingleFlightCache(cache.NewLocalCache(cache.LocalConfig[User]{
//	            TTL: 10 * time.Minute, Source: src, // ← 装饰器注入回源函数
//	        }))
//	    },
//	    cached.WithID[User](func(u *User) int64 { return u.ID }),
//	)
//	svc := crud.NewBaseService[User, CreateUserReq, UpdateUserReq](repo)
//
// Source 由装饰器注入是刻意的：null marker 防穿透与 singleflight
// 防击穿都长在后端缓存的 GetOrLoad 里，装饰器必须借道它回源。
package cached
