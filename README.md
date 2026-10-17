# data

> oekit 数据访问门面——复刻 ekit 单体库 data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 + cached 缓存读写编排 + twolevel 两级缓存，一个 import 前缀直达

> 🌐 项目主页：**https://oekit.github.io/** · 名称由来：**O**pen **E**asy **Kit**（开源 · 易用 · 工具集合）
> 🤖 AI Agent：本仓规范与硬规则见 [AGENTS.md](AGENTS.md)；机器可读索引见 [llms.txt](https://oekit.github.io/llms.txt)（接口签名以源码为准）。

## 安装

```bash
go get github.com/oekit/data@v0.8.2
```

## 这个仓库做什么

`data` 是**数据访问门面**：复刻 ekit 单体库 `data/` 的能力面，八个 re-export
子包的类型为泛型 type alias（与源包**完全同一类型**，双向赋值互通），实现
分别住在各子仓——

| 门面子包 | 能力 | 实现 |
|---|---|---|
| `cache` | 缓存接口（Cache/CacheAside/SourceFunc）+ LocalCache/SingleFlightCache + 键构造 + Checker | [pkg](https://github.com/oekit/pkg) `cache/` |
| `rediscache` | Redis 分布式缓存（CacheAside 的 Redis 后端） | [redisx](https://github.com/oekit/redisx) `cache/` |
| `crud` | 框架无关 CRUD Service/仓储 | [gormx](https://github.com/oekit/gormx) `crud/` |
| `distlock` | 分布式锁（RedisLocker/WithLock/优雅降级） | [redisx](https://github.com/oekit/redisx) `distlock/` |
| `gormx` | SQL/GORM 增强（Open/审计/OTel 插件/分页/单例仓储） | [gormx](https://github.com/oekit/gormx) |
| `redisx` | Redis 客户端封装（String/Hash/Set/ZSet/Bloom/Batch/PubSub） | [redisx](https://github.com/oekit/redisx) |
| `timertask` | 分片定时任务（FNV-1a 分片、心跳续约、at-least-once 可靠投递） | [timertask](https://github.com/oekit/timertask) |
| `cachetest` | CacheAside 实现契约测试套件（8 场景） | [pkg](https://github.com/oekit/pkg) `cache/cachetest/` |

仅有的两个例外是本仓原生子包：**缓存读写编排**与**两级缓存组合**：

| `cached` | 缓存读写编排装饰器（点读回填 + 写后失效 + 事务提交后失效 + 查询缓存版本号失效 + GetByIDs + bloom 防穿透） | 本仓 `cached/` |
| `twolevel` | 两级缓存组合（L1 本地 + L2 远端读穿写透）+ 跨实例失效广播（Redis pubsub） | 本仓 `twolevel/` |

> 缓存接口与契约套件已下沉 [pkg](https://github.com/oekit/pkg) 的 `cache`
> 子包（v0.4.0）——Redis 等后端实现依赖接口，接口必须位于门面下游。

## 快速使用

```go
import (
    "github.com/oekit/data/cache"
    "github.com/oekit/data/rediscache"
)

// 本地缓存（L1）
local := cache.NewLocalCache(cache.LocalConfig[HotConfig]{
    TTL: 30 * time.Second,
    Source: loadHotConfig, // 未命中回源（Cache-Aside）
})

// Redis 缓存（L2）——与本地缓存实现同一 CacheAside 接口
remote := rediscache.NewRedisCache(redisCli, rediscache.RedisConfig[HotConfig]{
    Prefix: "hot:", TTL: 24 * time.Hour, Source: loadHotConfig,
})
cfg, err := local.GetOrLoad(ctx, "tenant:acme")
```

## cached：缓存 CRUD（增删改失效缓存，查询构建缓存）

把 cache-aside 语义套在 `gormx/crud` 仓储上，一次装饰，CRUD 全部自动获得
缓存一致性编排。工作方式：

| 操作 | 发生什么 |
|---|---|
| `GetByID` | 先查缓存；未命中 → 仓储回源 → 回填（不存在的 ID 缓存 null marker 防穿透，并发回源由 SingleFlight 去重防击穿） |
| `GetOne` / `List` | 默认直通；开启查询缓存后走「表级版本号 + 查询指纹」键（见下） |
| `Create` / `Update` / `Delete` / `UpdateFields` / `Batch*` | **先写库，成功后**删实体键 + bump 表级版本；失效失败不回滚写（错误经 `WithOnError` 上报，TTL 兜底） |
| `RunInTx` | 事务内写只收集失效清单，**提交成功后**统一失效；回滚不失效；事务内读直通（看到事务内状态） |
| `Exists` / `Count` / `GetByCondition` | 永远直通（廉价查询 / 原始条件无法做稳定指纹） |

### 最小接入

```go
import (
    "github.com/oekit/data/cache"
    "github.com/oekit/data/cached"
    "github.com/oekit/data/crud"
)

// rawRepo 是 gormx/crud 的仓储实现（如 gormrepo.NewGormRepository）
repo := cached.NewRepository[User](rawRepo,
    // 后端工厂：装饰器把「键→ID 解析、ErrNotFound→(nil,nil)」的回源函数
    // 注入进来；后端形态与 TTL 由你决定——TTL 必须有限，它是删缓存
    // 失败时的唯一安全网
    func(src cache.SourceFunc[User]) cache.CacheAside[User] {
        return cache.NewSingleFlightCache(cache.NewLocalCache(cache.LocalConfig[User]{
            TTL: 10 * time.Minute, Source: src,
        }))
    },
    // 实体 ID 访问器：写路径据此删实体键。嵌入 gormx.BaseModel 的实体
    // 可省略（自动探测 GetID() 方法 / ID 字段）
    cached.WithID[User](func(u *User) int64 { return u.ID }),
)

// 直接作为 BaseService 的仓储；Update 自动走事务 + FOR UPDATE 行锁，
// 提交后失效缓存
svc := crud.NewBaseService[User, CreateUserReq, UpdateUserReq](repo)
svc.SetToEntity(func(r *CreateUserReq) *User { return &User{Name: r.Name} })
svc.SetUpdateEntity(func(u *User, r *UpdateUserReq) { u.Name = r.Name })

_ = svc // Create/Get/GetOne/List/Update/Delete 全部带缓存编排
```

### 查询缓存（opt-in：GetOne / List）

列表失效是难点：改一行意味着所有相关列表都该失效，按 key 逐个删不现实。
这里用**表级版本号**——任意写操作 bump 版本，旧键自然过期。因此语义是
"改一行 → 该表全部列表缓存失效"，用命中率换一致性；点读不受影响。

```go
// 三个缓存实例：GetOne 结果 / List 整页 / 表级版本号（版本键要配长 TTL）
entityQ := cache.NewLocalCache(cache.LocalConfig[User]{TTL: 5 * time.Minute})
page := cache.NewLocalCache(cache.LocalConfig[cached.QueryPage[User]]{TTL: 5 * time.Minute})
versions := cache.NewLocalCache(cache.LocalConfig[string]{TTL: 24 * time.Hour})

// ttl 必须为正（构造即拒绝 0）：失效失败时条目 TTL 是唯一安全网
qc, err := cached.NewQueryCache[User](entityQ, page, versions, 5*time.Minute)
if err != nil {
    return err
}

repo := cached.NewRepository[User](rawRepo, factory,
    cached.WithID[User](func(u *User) int64 { return u.ID }),
    cached.WithQueryCache[User](qc),
)
```

约束：`ForUpdate` 读永不缓存（锁读必须拿最权威数据）；负结果不缓存；
查询参数含不可区分取值的类型（如非导出字段的 Valuer 结构体，JSON 恒编
`{}`）时拒绝指纹、降级直通——绝不允许不同查询命中同一缓存键。

### Redis 后端

```go
import (
    "github.com/oekit/data/cache"
    "github.com/oekit/data/cached"
    "github.com/oekit/data/rediscache"
    "github.com/redis/go-redis/v9"
)

factory := func(src cache.SourceFunc[User]) cache.CacheAside[User] {
    return cache.NewSingleFlightCache(rediscache.NewRedisCache[User](redisCli,
        rediscache.RedisConfig[User]{
            Prefix: "app:user:", TTL: 30 * time.Minute, Source: src,
        }))
}
repo := cached.NewRepository[User](rawRepo, factory, cached.WithID[User](idOf))
```

### 选项速查

| 选项 | 作用 |
|---|---|
| `WithID` | 实体 ID 访问器；缺省自动探测 `GetID()` 方法（含指针接收者）→ 导出 `ID` 字段 |
| `WithQueryCache` | 开启查询缓存（`*QueryCache[T]`），不传则 GetOne/List 直通 |
| `WithPrefix` | 键前缀，默认 `cached`；多应用共享 Redis 时用应用名隔离 |
| `WithType` | 键中类型名段，默认反射类型名（如 `main.User`） |
| `WithOnError` | 缓存侧异常上报：读降级 `read` / 回填失败 `backfill` / 指纹失败 `fingerprint` / 失效失败 `invalidate`（记日志打点用，不能也不需要参与决策） |

### 键布局

```
{prefix}:{type}:id:{id}              点读实体键（写后删除）
{prefix}:{type}:q:one:{ver}:{fp}     GetOne 查询键（ver=表级版本号）
{prefix}:{type}:q:list:{ver}:{fp}    List 查询键（fp=QuerySpec 指纹，SHA-256 前 16）
{prefix}:{type}:ver                  表级版本号键（随机令牌，写操作 bump）
```

点读键与查询键空间不重叠，`QueryCache` 的 entity 实例可直接复用点读后端。

### 务必知道的语义边界

- **模式是 Cache-Aside（写后删缓存），不是双写**：删缓存 + TTL 兜底自愈；
  双写有部分失败与并发覆盖两类无法根治的不一致
- **TTL 纪律**：后端缓存的默认 TTL 必须为有限值——它是删缓存/失效失败时
  的唯一安全网
- **返回值一律只读**：缓存命中返回的是后端持有的引用（LocalCache 零拷贝
  共享内部对象），改写返回值会污染缓存并与并发读构成 data race——这与裸
  gormrepo「每次返回新对象」的习惯不同，需要可变副本请自行拷贝
- **失效是 best-effort**：写库成功后删缓存失败不回滚写（返回错误会诱发
  重试双写），错误走 `WithOnError`，期间靠 TTL 自愈
- **缓存后端读失败自动降级**：直查仓储，不把缓存故障当数据错误
- 并发回填存在窗口期竞态（miss→回源旧值→并发写→回填旧值），这是
  cache-aside 的固有窗口，由 TTL 兜底——不是本包可根治的问题

### 批量点读 GetByIDs

```go
users, err := repo.GetByIDs(ctx, []int64{1, 2, 3})
// users 只含找到的；不存在的 ID 不在 map 且不算错误（null marker 已缓存，
// 第二轮同类查询仓储零调用）；单个 ID 的仓储硬错误不阻断其余，errors.Join 聚合
```

逐 ID 走点读缓存路径（SingleFlight 去重回源）；Redis 后端是 N 次往返，
大批量场景自行控制批次大小。

### bloom 防穿透（可选件，慎用）

```go
import "github.com/oekit/redisx"

bf, _ := redisCli.NewBloomFilter(ctx, "cached:user:bloom", 0.001, 1_000_000)
repo := cached.NewRepository[User](rawRepo, factory,
    cached.WithID[User](idOf),
    cached.WithBloomFilter[User](bf), // 回源前先问布隆，"绝对不存在"零仓储调用
)
```

**启用前提（违反 = 数据消失级事故）**：所有写入必须走本装饰器（Create
自动 Add 新 ID，事务提交后同样生效）；存量 ID 必须预热；ID 空间单调递增
（自增主键满足）。bloom 读失败 fail-open 放行仓储（绝不 fail-closed）。

## twolevel：两级缓存 + 跨实例失效广播

L1 本地缓存 + L2 任意 CacheAside（典型 RedisCache）读穿写透组合，实现
`cache.CacheAside[T]`——可作为 `cached.NewRepository` 的后端工厂产物，
也可独立使用。多实例部署时挂 `Bus`（Redis pubsub）广播失效，驱逐所有
实例的 L1 副本。

```go
import (
    "github.com/oekit/data/cache"
    "github.com/oekit/data/cached"
    "github.com/oekit/data/twolevel"
    redisx "github.com/oekit/redisx"
    redisxcache "github.com/oekit/redisx/cache"
)

// 每个实例各自组装（local 各是各的，remote 共享同一 Redis）
rdb, _ := redisx.NewClientFromAddr(addr, password, 0)
local := cache.NewLocalCache(cache.LocalConfig[User]{TTL: 30 * time.Second}) // L1 短寿
remote := redisxcache.NewRedisCache[User](redisCli, redisxcache.RedisConfig[User]{
    Prefix: "app:user:", TTL: 30 * time.Minute, Source: src, // L2 长寿 + 回源
})
tlc, _ := twolevel.NewTwoLevelCache[User](local, remote,
    twolevel.WithBus[User](twolevel.NewBus(rdb, "evict:user")))
_ = tlc.Start(ctx)  // 启动订阅循环（无 Bus 时 no-op）
defer tlc.Close(ctx)

// 组合 cached：写路径 DelBatch 自动双删两级 + 广播驱逐全部实例的 L1
repo := cached.NewRepository[User](rawRepo, func(src cache.SourceFunc[User]) cache.CacheAside[User] {
    return tlcFactoryWith(src) // 工厂内构造上面的两级组合（Source 注入 L2）
}, cached.WithID[User](idOf))
```

语义要点：

- **L1 只缓存真值**，null 防穿透完全委托 L2 的 marker（每次 L1 miss 查
  一跳 L2 即被挡住，不回源）
- **Del 先远端后本地**（远端失败时本地已删，读路径回填的是远端旧值，
  短窗口 TTL 兜底；反序则本地旧值活过整个 L1 TTL）
- **SetWithTTL 的显式 TTL 同时作用于两层**（否则 L1 长驻吞掉短 TTL）
- **只广播失效、不广播回填**：Set/回填意味着 L2 有新值，他实例旧 L1 由
  下一次写路径广播驱逐或 TTL 兜底
- 组合体跑过 `cachetest` 完整契约（int/string 双类型），组合不破坏任何
  跨实现语义

## 边界

- **八个壳子包不含实现**：门面只做命名空间聚合（type alias + var 转发）；
  源仓新增导出符号时本仓壳需同步（跟版税由门面化决策承担，dependabot
  只升 pin 不补壳）；`cached` 与 `twolevel` 是仅有的两个例外（本仓实现，
  组合 crud 与 pkg/cache/redisx 的既有接口，不引入新底层依赖）
- 只用单一能力时直接依赖对应子仓（依赖面最小）：gormx/redisx/timertask/pkg
- 只需本地缓存的轻量场景可直接依赖 [pkg](https://github.com/oekit/pkg) 的
  `cache` 子包（不经门面）
- 需要"一键全家桶依赖聚合"（不 import 门面命名空间）的旧行为已废弃：
  `github.com/oekit/data/all` 已 retract（v0.5.0 起由本门面取代）

## 规划

- etcd/consul 后端接入（CacheAside 契约套件已备好准入门槛，等真实基建）


## 矩阵总览（23 活跃模块，自动生成）

| 模块 | 内容 | 矩阵内依赖 |
|---|---|---|
| [pkg](https://github.com/oekit/pkg) | 微工具集 21 合 1（errors/bloomx/contextx/container/validator/stringx/slicex/enumx/convert/encoding/jsonrepair/httpjson/mathx/pathx/pagination/ptrx/timex/uidgen/cache/bufiox/strmap）；cache（v0.4.0 起）含缓存接口 CacheAside + BatchGetter 批量能力（MGet）+ LocalCache/SingleFlightCache + cachetest 契约 + Checker；container（v0.9.0 起，收编原 collectionx）泛型容器 Set/Ring/Queue/SafeMap + Map（sync.Map 背书）/Slice（并发安全）；timex/stringx（v0.7.0 起）时间段边界 BeginningOf/EndOf 七对（分/时/日/周/月/季/年，周起始显式参数）+ trie 多模式替换器 Replacer（最长匹配）/ NewMask 敏感词遮罩；singleflighttest（v0.8.0 起）singleflight 共享契约测试套件（internal 与 concurrency 双侧引用） | — |
| [concurrency](https://github.com/oekit/concurrency) | 并发与流控原语（async/gopool/group/singleflight/progress/syncx/ratelimit/retry）+ window 滑动窗口 / breaker 自适应熔断（规范哨兵 ErrCircuitOpen）/ shedding CPU 自适应降载 / batch 攒批执行器（满条数/满字节/窗口去重）/ timewheel 单级时间轮 | — |
| [logx](https://github.com/oekit/logx) | 统一日志门面（多后端可替换 + Sink + caller 归属）+ 优雅关停；桥接子包 hlog/hertzlog/kitexlog 为嵌套模块按需引入 | — |
| [core](https://github.com/oekit/core) | 应用基建件（governance 治理 schema / config 配置加载 + default/env/range/options tag 校验 / lifecycle 契约 / network IP 工具 / version） | — |
| [metrics](https://github.com/oekit/metrics) | Prometheus 门面 + 弹性指标族 | [core](https://github.com/oekit/core) |
| [tracing](https://github.com/oekit/tracing) | OpenTelemetry 追踪 + trace_id 真相源 | [logx](https://github.com/oekit/logx) |
| [registry](https://github.com/oekit/registry) | 注册发现核心 + Nacos 实现 + Nacos 配置源 + sdbridge | [core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx) |
| [data](https://github.com/oekit/data) | 数据访问门面——复刻 ekit data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 re-export（类型同一，实现住 pkg 与各子仓）+ cached 缓存读写编排（写后失效/事务提交后失效/查询缓存版本号失效/GetByIDs 批量点读 MGet 快路径/bloom 防穿透）+ twolevel 两级缓存与跨实例失效广播（v0.7.0 起本仓唯二非 re-export 实现）；all/ 聚合模块已废弃 retract | [concurrency](https://github.com/oekit/concurrency)、[gormx](https://github.com/oekit/gormx)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg)、[redisx](https://github.com/oekit/redisx)、[timertask](https://github.com/oekit/timertask) |
| [gormx](https://github.com/oekit/gormx) | SQL/GORM 全家——连接池增强、框架无关 CRUD Service、仓储（原 oekit/gorm，v0.3.0 更名） | [logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [redisx](https://github.com/oekit/redisx) | Redis 全家——客户端封装（根包）、分布式缓存 cache/、分布式锁 distlock、分布式限流 ratelimit（Lua 三态 + 故障本地兜底） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [etcdx](https://github.com/oekit/etcdx) | etcd 全家——客户端封装（根包：生产默认值/Health 探活/Close(ctx) 幂等）、registry 注册发现实现（SPI 对接 registry/nacos 链路：Lease KeepAlive 注册 + 丢约指数退避自动重注册、prefix Watch 订阅推送、ConfigStore KV 配置监听）、distlock 分布式锁（Session+concurrency.Mutex 公平排队，接口与 redisx/distlock.Locker 同形可互换，可选无 etcd 降级放行） | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry) |
| [httpx](https://github.com/oekit/httpx) | HTTP 客户端 + 负载均衡——服务发现/熔断（规范哨兵 ErrCircuitOpen）/重试/指标/OTel 透传/一致性哈希（权重感知虚拟副本） | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [network](https://github.com/oekit/network) | 轻量网络客户端——WebSocket/NATS/MQ 抽象、连接与流工具 | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[tracing](https://github.com/oekit/tracing) |
| [timertask](https://github.com/oekit/timertask) | 分片定时任务（FNV-1a、心跳续约） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [middleware](https://github.com/oekit/middleware) | Hertz 自研标准中间件栈 + contrib 桥接 + hmw + SheddingMiddleware 自适应降载 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [k8s](https://github.com/oekit/k8s) | Kubernetes 客户端封装——RESTConfig/Clientset/DynamicClient 构建（显式 kubeconfig → in-cluster 自动 → KUBECONFIG/~/.kube/config 逐级回退，QPS/Burst 限流默认 50/100）+ leaderelection 子包 Lease 选主简化封装（OnStartedLeading/OnStoppedLeading/OnNewLeader 回调，主备单活，仅要求 Lease CRUD 权限） | [logx](https://github.com/oekit/logx) |
| [nns](https://github.com/oekit/nns) | NNS 服务治理客户端——provider 无关发现/配置/实例缓存 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry) |
| [nns-endpoint](https://github.com/oekit/nns-endpoint) | NNS 端点工厂——per-service 熔断、换实例重试、gRPC 池 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[httpx](https://github.com/oekit/httpx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[registry](https://github.com/oekit/registry) |
| [bootstrap](https://github.com/oekit/bootstrap) | 应用生命周期引导——bootstrap.Run 骨架 + healthz | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [hertz](https://github.com/oekit/hertz) | Hertz 运行时集成——日志桥接、生命周期、SSE/health/metrics、适配器、OTel、crudh/validatorh/versionh（原 handler 微库收编） | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[gormx](https://github.com/oekit/gormx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[middleware](https://github.com/oekit/middleware)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [kitex](https://github.com/oekit/kitex) | Kitex 生态——日志桥接、治理热更、生命周期、BizError 桥接、kitexotel、istio（xDS 动态熔断，原 istio 微库收编） | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry) |
| [workflow](https://github.com/oekit/workflow) | 组合式流程编排——串行/并行/异步/条件分支 + Hook AOP | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [kitex-examples](https://github.com/oekit/kitex-examples) | Kitex echo 示例——client/server + IDL + 治理配置 | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[kitex](https://github.com/oekit/kitex)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
> 依赖严格自底向上；实现住 pkg 与各子仓，门面仅 re-export。

> 本仓在矩阵中的位置与依赖见上表「矩阵内依赖」列。历史沿革：源自 ekit 单体库
> （git.enjoye.top/enjoydream/ekit v0.34.0，冻结维护），2026-10 拆分为独立 module，
> 2026-10 门面化（复刻 ekit data/ 能力面，实现下沉 pkg 与各子仓）。

## License

MIT — 见 [LICENSE](LICENSE)
