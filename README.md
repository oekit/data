# data

> oekit 数据访问门面——复刻 ekit 单体库 data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 + cached 缓存读写编排 + twolevel 两级缓存，一个 import 前缀直达

> 🌐 项目主页：**https://oekit.github.io/** · 名称由来：**O**pen **E**asy **Kit**（开源 · 易用 · 工具集合）
> 🤖 AI Agent：本仓规范与硬规则见 [AGENTS.md](AGENTS.md)；机器可读索引见 [llms.txt](https://oekit.github.io/llms.txt)（接口签名以源码为准）。

## 安装

```bash
go get github.com/oekit/data@v0.15.0
```

## 这个仓库做什么

`data` 是**数据访问门面**：复刻 ekit 单体库 `data/` 的能力面，八个 re-export
子包的类型为泛型 type alias（与源包**完全同一类型**，双向赋值互通），实现
分别住在各子仓——

| 门面子包 | 能力 | 实现 |
|---|---|---|
| `cache` | 缓存接口（Cache/CacheAside/SourceFunc）+ LocalCache/SingleFlightCache + 键构造 + Checker；LocalCache 可选形状（pkg v0.16.0 起）：NoBackgroundSweep 无后台清扫 goroutine + CloneOnRead 读命中返回副本 + EvictPolicy 可选 LRU + GetEx 命中标志（新增能力以可选接口 Sweeper/HitGetter 外置，不破坏既有 Cache 接口） | [pkg](https://github.com/oekit/pkg) `cache/` |
| `rediscache` | Redis 分布式缓存（CacheAside 的 Redis 后端） | [redisx](https://github.com/oekit/redisx) `cache/` |
| `crud` | 框架无关 CRUD Service/仓储 | [gormx](https://github.com/oekit/gormx) `crud/` |
| `distlock` | 分布式锁（RedisLocker/WithLock/优雅降级/Renew 续期）+ Semaphore 信号量 + Lockout 防爆破锁 | [redisx](https://github.com/oekit/redisx) `distlock/` |
| `gormx` | SQL/GORM 增强（Open/审计/OTel 插件/分页/单例仓储） | [gormx](https://github.com/oekit/gormx) |
| `redisx` | Redis 客户端封装（String/Hash/Set/ZSet/Bloom/Batch/PubSub） | [redisx](https://github.com/oekit/redisx) |
| `timertask` | 分片定时任务——本地后端（LocalBackend）+ 选项集 + `BackendConfig`/`ResolveOptions`；**Redis 形态自 timertask v0.2.0 起迁至嵌套模块** `timertask/redis`（本壳不再转发 RedisBackend/ShardManager，需 Redis 形态请直接依赖该嵌套模块） | [timertask](https://github.com/oekit/timertask) |
| `cachetest` | CacheAside 实现契约测试套件（13 场景——pkg v0.16.0 起新增 Sweep 手动清扫 / 命中标志 / 读隔离 / 读提升 / 无后台 goroutine 五项） | [pkg](https://github.com/oekit/pkg) `cache/cachetest/` |

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
repo := cached.NewRepository[User, int64](rawRepo,
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
    cached.WithID[User, int64](func(u *User) int64 { return u.ID }),
)

// 直接作为 BaseService 的仓储；Update 自动走事务 + FOR UPDATE 行锁，
// 提交后失效缓存
svc := crud.NewBaseService[User, int64, CreateUserReq, UpdateUserReq](repo)
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

repo := cached.NewRepository[User, int64](rawRepo, factory,
    cached.WithID[User, int64](func(u *User) int64 { return u.ID }),
    cached.WithQueryCache[User, int64](qc),
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
repo := cached.NewRepository[User, int64](rawRepo, factory, cached.WithID[User, int64](idOf))
```

### 选项速查

| 选项 | 作用 |
|---|---|
| `WithID` | 实体 ID 访问器（返回类型 = 主键类型 `ID`）；缺省自动探测 `GetID()` 方法（含指针接收者，返回类型须与 `ID` 一致）→ 导出 `ID` 字段（类型同） |
| `WithIDCodec` | 缓存键中 ID 段的编解码逃生口；缺省支持 string 直通与全部整型（十进制，与 v0.13 及之前 int64 键格式兼容），其余 comparable 类型必须显式注入 |
| `WithQueryCache` | 开启查询缓存（`*QueryCache[T]`），不传则 GetOne/List 直通 |
| `WithPrefix` | 键前缀，默认 `cached`；多应用共享 Redis 时用应用名隔离 |
| `WithType` | 键中类型名段，默认反射类型名（如 `main.User`） |
| `WithOnError` | 缓存侧异常上报：读降级 `read` / 回填失败 `backfill` / 指纹失败 `fingerprint` / 失效失败 `invalidate`（记日志打点用，不能也不需要参与决策） |

> v0.14.0 起全链路主键类型泛型化（`CachedRepository[T, ID comparable]`，跟随 gormx v0.6.0）：
> int64 自增迁移只需在实例化点补 `int64` 实参；string 文本主键 / UUID 实体可直接用。

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
repo := cached.NewRepository[User, int64](rawRepo, factory,
    cached.WithID[User, int64](idOf),
    cached.WithBloomFilter[User, int64](bf), // 回源前先问布隆，"绝对不存在"零仓储调用
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
repo := cached.NewRepository[User, int64](rawRepo, func(src cache.SourceFunc[User]) cache.CacheAside[User] {
    return tlcFactoryWith(src) // 工厂内构造上面的两级组合（Source 注入 L2）
}, cached.WithID[User, int64](idOf))
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


## 矩阵总览（24 活跃模块，自动生成）

| 模块 | 内容 | 矩阵内依赖 |
|---|---|---|
| [pkg](https://github.com/oekit/pkg) | 微工具集 23 合 1（errors/bloomx/contextx/container/stringx/slicex/enumx/convert/encoding/jsonrepair/httpjson/mathx/pathx/pagination/ptrx/timex/cache/bufiox/strmap/singleflighttest/urlguard/cli/pack；v0.10.0 起 uidgen/validator 拆至 kitx）；errors（v0.12.0 起）New/As/Is/Unwrap 与 stdlib 同型透传——下游哨兵/As 判定单 import 即可，类型同一性红线可完整执行；cache（v0.4.0 起）含缓存接口 CacheAside + BatchGetter 批量能力（MGet）+ LocalCache/SingleFlightCache + cachetest 契约（Run 真实时间 + RunWithClock 假时钟确定性 TTL 边界）+ Checker；LocalCache 时钟可注入（LocalConfig.Now——TTL/NullTTL 全路径确定性测试，v0.13.0 起）；v0.16.0 起 LocalCache 可选形状——NoBackgroundSweep 关闭后台清扫 goroutine（嵌入式/短命进程，公开 Sweep 手动清扫口）、CloneOnRead 读命中返回副本（调用方可改写不污染缓存）、EvictPolicy 可选 LRU（默认 FIFO 不变）、GetEx 区分未命中与值为 nil（新增能力以可选接口 Sweeper/HitGetter 外置，不破坏其它后端）；container（v0.9.0 起，收编原 collectionx）泛型容器 Set/Ring/Queue/SafeMap + Map（sync.Map 背书）/Slice（并发安全）；mathx/jsonrepair（v0.11.0）Unstable 单边向上抖动 Upward / 宽松解析 UnmarshalLenient；timex/stringx（v0.7.0 起）时间段边界 BeginningOf/EndOf 七对（分/时/日/周/月/季/年，周起始显式参数）+ trie 多模式替换器 Replacer（最长匹配）/ NewMask 敏感词遮罩；stringx Masker（v0.16.0 起）正则脱敏——按形态分类打码（形态标签进掩码供审计读）+ 保留首尾各 ≤4 字符定位线索且不可复原（保留位封顶不随长度变化），规则表由调用方完全提供；singleflighttest（v0.8.0 起）singleflight 共享契约测试套件（internal 与 concurrency 双侧引用）；urlguard（v0.15.0 起）出站 SSRF 防护原语——CheckURL 字面量校验（私网/loopback/链路本地含云元数据 169.254.169.254/CGNAT/ULA/组播保留、.internal·.local·localhost 域名、scheme 白名单、IPv4-mapped IPv6 归一）+ DialFunc 拨号层预解析校验与实际 RemoteAddr 端点核对闭合 TOCTOU（Happy-Eyeballs 覆盖），构造态豁免 WithAllowPrivate/WithAllowCIDRs/WithAllowDomains，与 httpx WithDialFunc 一行组合（httpx#11）；cli（v0.16.0 起）框架无关可单测的子命令分发原语——App/Command + 退出码 0/1/2 写死在包里、usage 按 Summary 自动生成、不读 os.Args 不调 os.Exit（输出流全由调用方给）；pack（v0.16.0 起）迁移包容器原语——zip + manifest（格式版本/条数/每文件 SHA-256）+ JSONL 流式读写 + Inspect 体检一次列全问题并区分包损坏与版本不认识 | — |
| [concurrency](https://github.com/oekit/concurrency) | 并发与流控原语（async/gopool/group/singleflight/progress/syncx/ratelimit/retry）+ window 滑动窗口 / breaker 自适应熔断（规范哨兵 ErrCircuitOpen）/ shedding CPU 自适应降载 / batch 攒批执行器（满条数/满字节/窗口去重）/ timewheel 单级时间轮；v0.5.0 起 retry 指数退避+抖动（MaxDelay 封顶/溢出饱和）、loop 周期原语（忙时丢 tick/单轮 panic 收编/FirstDelay 可打断）、async labeled RunSafe（panic 按任务标签上报）、syncx.FanInErr 逐槽错误（panic 槽显式标记）、KeyedLimiter 增强（容量护栏/RetryAfter/速率热更）并接入 pkg/singleflighttest 契约测试；v0.8.0 起 singleflight DoShared 折叠可观测（Result.Waiters）、async panic 统一收编 typed *PanicError（errors.As 可辨、携带栈）+ 实例级 Runner panic 路由、retry DelayHint（服务端 Retry-After 建议优先）+ JitterRatio 比例抖动 + retry/httpx Retryable 可重试性判定；v0.9.0 起 breaker 显式三态状态机 closed/open/half-open——冷却（WithProbeTimeout 缺省 1m）期满首个放行即半开探测、探测成功关闸清窗/失败重开、失联超时自动回收 + AbandonGen 显式放弃，GenBreaker 代绑定（AllowGen/SuccessGen/FailureGen/AbandonGen：代仅在探测放行自增、陈旧代回报整体作废、开放期失败不后移冷却）；v0.10.0 起 WithCooldown 独立业务冷却期（缺省回落 probeTimeout）+ WithConsecutiveFailures 连续失败计数触发（与速率型并联，补低 QPS 信号，对齐 Envoy consecutive_5xx）；v0.11.0 起 ratelimit Lockout 防爆破锁（fail2ban 形态：滑窗失败计数→阈值锁定拒绝 LockDuration→成功 Reset 清零/到期自解，CheckLocked 返回剩余时长）；v0.12.0 起 retry/httpx RetryableWithHeaders（403 + Retry-After/X-RateLimit-Remaining 头感知可重试判定，HeaderSource 零依赖抽象）、syncx FanInLimit 受限并发扇出（固定 worker 防下游过载）+ FanInLimitCtx（v0.14.0，ctx 感知：ctx 已取消未启动项 fn 一次不调、剩余槽快速排空 Canceled、在飞项照常落槽）、ratelimit WithTokenClock 时钟注入；v0.13.0 起 breaker 熔断期收敛到探测级（forcePass 强制探活 + 冷却期满半开探测，其余拒绝——SRE 公式退役为 closed→open 触发器，WithConsecutiveFailures 触发后流量真停）+ State()/State 枚举可观测出口；v0.16.0 起 ratelimit NewKeyedLimiterByFunc[K]——factory func(K) Limiter 按 key 定配额（一个管理器按接口维度分档位，NewKeyedLimiterOf 零破坏随迁；未登记 key 拿 factory 兜底档的坑已在 doc 点名，#31）；v0.17.0 起 KeyedLimiter.Create 护栏内原子登记（表满先回收空闲与 Allow 同路、仍满拒绝计 OverCapHits、已存在不覆盖——热更走 Replace；解决 ByFunc factory 库锁内调用致消费方锁序倒置，#33） | [pkg](https://github.com/oekit/pkg) |
| [logx](https://github.com/oekit/logx) | 统一日志门面（多后端可替换 + caller 归属；Sink 已迁嵌套模块 logx/sink，根包纯 stdlib）；WithFilter 脱敏/丢弃包装（FilterKey 命中键替换 ***/FilterFunc 丢整条，子 logger 保持规则）、Slogger/SlogHandler 正向桥（三方 slog 直接消费 logx 生态）、TraceHandler 任意 slog.Handler 自动注入 trace_id、NewAuditLogger 审计留痕（级别锁 Info，不受 LOG_LEVEL 降级）、LevelEnabler 热路径提前判级、ParseLogLevel 返回 (Level, error) + ParseLogLevelOr 配错退默认（v0.7.0）；优雅关停正身在 core/lifecycle.Manager（logx/shutdown 仅余 Deprecated 别名）；桥接子包 hlog/hertzlog/kitexlog 为嵌套模块按需引入 | [core](https://github.com/oekit/core) |
| [core](https://github.com/oekit/core) | 应用基建件——governance 治理 schema、config 配置加载（LoadBootstrap/LoadDotEnv/Get* 系、default/env/range/options tag 校验、OverlaySource 多源叠加热更、ApplyEnvOverlay env 优先 12-factor 形态）、transport 协议无关传输上下文（Transporter/Header/Server·Client ctx 存取，仅标准库）、chain 中间件链（Handler/Middleware/Chain/Recovery/Timeout，与 nns-endpoint.Endpoint 同形）、lifecycle 契约 + Manager 优雅关停（自 logx/shutdown 迁入，日志 *slog.Logger——core 不再依赖 logx）、network IP 工具、version；v0.9.0 起 config FieldSet 显式提供集合（yaml 显式 port:0 不再被 default/env 覆盖——LoadBootstrap 默认接线，免全量指针化）+ require tag（必须 yaml/env 提供，缺失列全并附 env 提示）+ v0.9.1 修 yaml:,inline 字段在 FieldSet 下漏标记（inline 叶子显式零值曾被 default 覆盖）；v0.9.2 env 卫生改纯静态（仅格式/重名，移除依赖进程环境的拼写冒烟检查——误报 PATH/HOME、漏无人设置的真拼错，core#8），命名一致性走 LintEnvNaming 诊断 / WithEnvNamingLint 可选 fail-closed / ignorederived="true" 豁免 | — |
| [kitx](https://github.com/oekit/kitx) | 标识与校验——uidgen（Snowflake 全局单例+Extract 元数据、手写 RFC 4122 v4 UUID 零第三方依赖、RandomHex/RandomString 拒绝采样随机串）+ validator（go-playground/validator v10 接口化门面：struct tag 校验/自定义规则注册/ValidationError Unwrap 为 BizError 400 对接 middleware.Fail）+ hash（v0.2.0 起非密码学哈希助手 Md5Hex/Md5Bytes——缓存键/幂等键/短唯一 code，格式稳定承诺，pkg 2026-09-15 治理移除弱哈希后 kitx 侧收编 pkg#10），自 pkg v0.10.0 拆出 | [pkg](https://github.com/oekit/pkg) |
| [metrics](https://github.com/oekit/metrics) | Prometheus 门面 + 弹性指标族 | [core](https://github.com/oekit/core) |
| [tracing](https://github.com/oekit/tracing) | OpenTelemetry 追踪 + trace_id 真相源 | [logx](https://github.com/oekit/logx) |
| [registry](https://github.com/oekit/registry) | 注册发现核心 + Nacos 实现 + Nacos 配置源 + sdbridge | [core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx) |
| [data](https://github.com/oekit/data) | 数据访问门面——复刻 ekit data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 re-export（类型同一，实现住 pkg 与各子仓）+ cached 缓存读写编排（写后失效/事务提交后失效/查询缓存版本号失效/GetByIDs 批量点读 MGet 快路径/bloom 防穿透）+ twolevel 两级缓存与跨实例失效广播（v0.7.0 起本仓唯二非 re-export 实现）；v0.15.0 起壳跟随源仓演进——cache 壳补 LocalCache 可选形状（EvictPolicy/Sweeper/HitGetter）、cachetest 壳补 5 个契约套件入口（套件 8 → 13 场景）、timertask 壳的 Redis 形态随源仓 v0.2.0 迁至嵌套模块 timertask/redis（壳不再转发 RedisBackend/ShardManager，Redis 形态直接依赖该嵌套模块）；all/ 聚合模块已废弃 retract | [concurrency](https://github.com/oekit/concurrency)、[gormx](https://github.com/oekit/gormx)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg)、[redisx](https://github.com/oekit/redisx)、[timertask](https://github.com/oekit/timertask) |
| [gormx](https://github.com/oekit/gormx) | SQL/GORM 全家——连接池增强、框架无关 CRUD Service、仓储（原 oekit/gorm，v0.3.0 更名） | [logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [redisx](https://github.com/oekit/redisx) | Redis 全家——客户端封装（根包）、分布式缓存 cache/、分布式锁 distlock（Locker 同形可互换；Renew 手动续期（false=锁丢失，不进 Locker 接口保 etcdx 互换）+ WithAutoRenew 覆盖 TryLock 自动续期（v0.8.0 feat!：此前组合不续期）；Semaphore ZSET 槽位信号量（v0.8.0：Acquire 同 identifier 幂等刷新 + Lua 内原子清过期槽崩溃自愈、Renew 仅未过期槽可续、Cleanup 返条数、无 Redis 降级 fail-fast））、分布式限流 ratelimit（Lua 三态 + 故障本地兜底）；v0.9.0 起 distlock Lockout 分布式防爆破锁（Redis 侧 fail2ban：滑窗失败计数→阈值锁定 Penalty→Reset 清零，与 concurrency/ratelimit.Lockout 进程内孪生——多实例共享计数是存在理由；惩罚只按首次触发计算、nil client fail-fast 不降级，#6） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [etcdx](https://github.com/oekit/etcdx) | etcd 全家——客户端封装（根包：生产默认值/Health 探活/Close(ctx) 幂等）、registry 注册发现实现（SPI 对接 registry/nacos 链路：Lease KeepAlive 注册 + 丢约指数退避自动重注册、prefix Watch 订阅推送、ConfigStore KV 配置监听）、distlock 分布式锁（Session+concurrency.Mutex 公平排队，接口与 redisx/distlock.Locker 同形可互换，可选无 etcd 降级放行） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry) |
| [httpx](https://github.com/oekit/httpx) | HTTP 客户端 + 负载均衡——服务发现/熔断（规范哨兵 ErrCircuitOpen）/重试/指标/OTel 透传；loadbalance Selector 形态（RoundRobin/Random/Weighted/一致性哈希权重感知虚拟副本/p2c 自适应——EWMA 经 DoneFunc 回写建立、Err 计 1s 惩罚；WithFilter 按元数据过滤；v0.4.0 起 Balancer 全家族删除无兼容路径）；codec 注册制编解码器（json 内置 + form 表单，DoCodec 请求按 codec 序列化、响应按 Content-Type 反查回退请求 codec）；v0.6.0 起 net/http 桥接层 NewRoundTripper/StdClient——存量标准库外呼一行 Transport 接入熔断/重试/指标/链路（重试缺省关显式开、非 2xx 非错误、hop-by-hop 隔离，#8）；v0.8.0 起 SafeDialFunc/WithSafeDial 出站 SSRF 防护胶水（判定单源 pkg/urlguard v0.15.0：解析 IP 全量预检 + 实际 RemoteAddr 端点核对闭合重解析 TOCTOU、Happy-Eyeballs 覆盖，构造态豁免；v0.7.0 内置的独立 httpx/urlguard 实现收敛删除，#11） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [network](https://github.com/oekit/network) | 轻量网络客户端——WebSocket/NATS/MQ 抽象、连接与流工具 | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[tracing](https://github.com/oekit/tracing) |
| [timertask](https://github.com/oekit/timertask) | 分片定时任务（FNV-1a、心跳续约）；v0.1.5 起可选可靠投递 DeliveryAtLeastOnce（租约 + Ack + 可见性超时；过期租约回扫重投/死信、停机 best-effort 收租，v0.1.7 接入生命周期真正生效）；v0.2.0 起 **Redis 后端迁至嵌套模块 timertask/redis**（根包不再引 go-redis/logx/concurrency，require 仅 pkg——import timertask 与 import timertask/local 的模块依赖面均无 redis，纯 stdlib 嵌入式场景可用）+ **新增 local 子包 cron 调度器 Scheduler**（表达式复用 pkg/timex/cron 不另写一份；并发度/CatchUp 补跑/退出语义明确，FakeClock 假时钟驱动、触发点序列可确定性断言） | [pkg](https://github.com/oekit/pkg) |
| [middleware](https://github.com/oekit/middleware) | Hertz 标准中间件栈（模块根 mw，双层目录已收平）+ contrib 桥接 + hmw + SheddingMiddleware 自适应降载 + chainmw 子包（transport 感知框架无关中间件，基于 core/chain+transport：Recovery 带 panic 隔离上报钩子 WithRecoveryReporter、AccessLog skip+redact+自定义字段、MetadataPropagate——kitex 经 kitex/chainmw.ToKitex 复用同一实现，根包 Recovery/AccessLog 已薄壳化复用 chainmw 内核、下游 handler 可经 transport.FromServerContext 读协议无关传输信息）；v0.6.0 起 accesslog 凭证路径脱敏（WithAccessLogRedactPathPrefixes）、ratelimit 默认 KeyFunc 去端口（连接桶→地址桶）；v0.11.0 起 WithAccessLogSink 落库 sink（AccessRecord{Method,Path,Status,Latency,ClientIP,UserAgent,TraceID} 与日志同源、c 同传可读业务键、error/panic 只记 Warn 不影响主链路、SkipPaths 命中不触发）+ chainmw.WithSink 内核同构（kitex 随 ToKitex 获得）+ StackConfig.DisableResponseWrap 响应自管一键形态（TraceID→Recovery→AccessLog）与 AccessLogOptions 透传 + doc/README「适用边界」段（响应契约统一 vs handler 自管的推荐组合，#18）；v0.12.0 起 AccessLog panic 恢复点产出记录（AccessRecord.Panic 标记 + Err 摘要，Hertz status 按 Recovery 缺省 500）后按原值重抛——panic 请求不再零访问记录，sink/kitex 桥接同步留痕，#20 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [k8s](https://github.com/oekit/k8s) | Kubernetes 客户端封装——RESTConfig/Clientset/DynamicClient 构建（显式 kubeconfig → in-cluster 自动 → KUBECONFIG/~/.kube/config 逐级回退，QPS/Burst 限流默认 50/100）+ leaderelection 子包 Lease 选主简化封装（OnStartedLeading/OnStoppedLeading/OnNewLeader 回调，主备单活，仅要求 Lease CRUD 权限） | [logx](https://github.com/oekit/logx) |
| [nns](https://github.com/oekit/nns) | NNS 服务治理客户端——provider 无关发现/配置/实例缓存 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry) |
| [nns-endpoint](https://github.com/oekit/nns-endpoint) | NNS 端点工厂——per-service 熔断、换实例重试、gRPC 池 | [httpx](https://github.com/oekit/httpx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[registry](https://github.com/oekit/registry) |
| [bootstrap](https://github.com/oekit/bootstrap) | 应用生命周期引导——New(WithServiceName/WithShutdownTimeout/WithBeforeServe) + Run(fn) + healthz；App.Ready/ReadyChan/IsReady 就绪门（幂等并发安全）——嵌套 bootstrap/nacos 自 v0.3.0 起 NNS 注册延迟到 Ready 之后（server 就绪 → userFn 触发 Ready → 才进注册中心）、关停序 nns 反注册先于 server 关闭；Metrics/Tracing 在嵌套模块 bootstrap/obs | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx) |
| [hertz](https://github.com/oekit/hertz) | Hertz 运行时集成（模块根扁平化）——日志桥接、生命周期、SSE/health/metrics、适配器、OTel、crudh/validatorh（基于 kitx/validator）/versionh（原 handler 微库收编）；RunServer 变参就绪门 WithOnReady（全部 HostPort TCP 探活可建连后回调，探活超时按启动失败优雅关机；缺省超时 5s 经 WithReadyProbeTimeout 调整/WithReadyAddr 探活地址），配合 bootstrap Ready 实现 NNS 延迟注册；transportx（hertz→core/transport 同构适配器）+ chainmw 桥接 | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[gormx](https://github.com/oekit/gormx)、[kitx](https://github.com/oekit/kitx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[middleware](https://github.com/oekit/middleware)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [kitex](https://github.com/oekit/kitex) | Kitex 生态——日志桥接、治理热更、生命周期、BizError 桥接、kitexotel、istio（xDS 动态熔断，原 istio 微库收编）；RunServer 变参就绪门（WithOnReady/WithReadyAddr/WithReadyProbeTimeout）+ chainmw.ToKitex（core/chain.Middleware → kitex 中间件，与 hertz 共享 middleware/chainmw 实现）+ transportx | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry) |
| [workflow](https://github.com/oekit/workflow) | 组合式流程编排——串行/并行/异步/条件分支 + Hook AOP | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [kitex-examples](https://github.com/oekit/kitex-examples) | Kitex echo 示例——client/server + IDL + 治理配置 | [bootstrap](https://github.com/oekit/bootstrap)、[core](https://github.com/oekit/core)、[kitex](https://github.com/oekit/kitex)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
> 依赖严格自底向上；实现住 pkg 与各子仓，门面仅 re-export。

> 本仓在矩阵中的位置与依赖见上表「矩阵内依赖」列。历史沿革：源自 ekit 单体库
> （git.enjoye.top/enjoydream/ekit v0.34.0，冻结维护），2026-10 拆分为独立 module，
> 2026-10 门面化（复刻 ekit data/ 能力面，实现下沉 pkg 与各子仓）。

## License

MIT — 见 [LICENSE](LICENSE)
