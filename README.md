# data

> oekit 数据访问门面——复刻 ekit 单体库 data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包，一个 import 前缀直达

> 🌐 项目主页：**https://oekit.github.io/** · 名称由来：**O**pen **E**asy **Kit**（开源 · 易用 · 工具集合）
> 🤖 AI Agent：本仓规范与硬规则见 [AGENTS.md](AGENTS.md)；机器可读索引见 [llms.txt](https://oekit.github.io/llms.txt)（接口签名以源码为准）。

## 安装

```bash
go get github.com/oekit/data@v0.5.0
```

## 这个仓库做什么

`data` 是**纯 re-export 门面**：复刻 ekit 单体库 `data/` 的能力面，类型为泛型
type alias（与源包**完全同一类型**，双向赋值互通），实现分别住在各子仓——

| 门面子包 | 能力 | 实现 |
|---|---|---|
| `cache` | 缓存接口（Cache/CacheAside/SourceFunc）+ LocalCache/SingleFlightCache + 键构造 + Checker | [pkg](https://github.com/oekit/pkg) `cache/` |
| `rediscache` | Redis 分布式缓存（CacheAside 的 Redis 后端） | [redisx](https://github.com/oekit/redisx) `cache/` |
| `crud` | 框架无关 CRUD Service/仓储 | [gormx](https://github.com/oekit/gormx) `crud/` |
| `distlock` | 分布式锁（RedisLocker/WithLock/优雅降级） | [redisx](https://github.com/oekit/redisx) `distlock/` |
| `gormx` | SQL/GORM 增强（Open/审计/OTel 插件/分页/单例仓储） | [gormx](https://github.com/oekit/gormx) |
| `redisx` | Redis 客户端封装（String/Hash/Set/ZSet/Bloom/Batch/PubSub） | [redisx](https://github.com/oekit/redisx) |
| `timertask` | 分片定时任务（FNV-1a 分片、心跳续约） | [timertask](https://github.com/oekit/timertask) |
| `cachetest` | CacheAside 实现契约测试套件（8 场景） | [pkg](https://github.com/oekit/pkg) `cache/cachetest/` |

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

## 边界

- **门面不含实现**：本仓只做命名空间聚合（type alias + var 转发）；源仓新增
  导出符号时本仓壳需同步（跟版税由门面化决策承担，dependabot 只升 pin 不补壳）
- 只用单一能力时直接依赖对应子仓（依赖面最小）：gormx/redisx/timertask/pkg
- 只需本地缓存的轻量场景可直接依赖 [pkg](https://github.com/oekit/pkg) 的
  `cache` 子包（不经门面）
- 需要"一键全家桶依赖聚合"（不 import 门面命名空间）的旧行为已废弃：
  `github.com/oekit/data/all` 已 retract（v0.5.0 起由本门面取代）

## 规划

- 两级缓存装饰器（Local + Redis 读穿写透，原 all/ 模块的 TwoLevelCache）：
  等真实需求在门面子包立项（可实现为对两个 CacheAside 的组合装饰）
- 两级缓存失效广播（Redis pubsub 主动失效 L1）：单独立项


## 矩阵总览（21 活跃模块，自动生成）

| 模块 | 内容 | 矩阵内依赖 |
|---|---|---|
| [pkg](https://github.com/oekit/pkg) | 微工具集 21 合 1（errors/bloomx/contextx/collectionx/validator/stringx/slicex/enumx/convert/encoding/jsonrepair/httpjson/mathx/pathx/pagination/ptrx/timex/uidgen/cache/bufiox/strmap）；cache（v0.4.0 起）含缓存接口 CacheAside + LocalCache/SingleFlightCache + cachetest 契约 + Checker；collectionx（v0.5.0 起）泛型容器 Set/Ring/Queue/SafeMap | — |
| [concurrency](https://github.com/oekit/concurrency) | 并发与流控原语（async/gopool/group/singleflight/progress/syncx/ratelimit/retry）+ window 滑动窗口 / breaker 自适应熔断（规范哨兵 ErrCircuitOpen）/ shedding CPU 自适应降载 / batch 攒批执行器（满条数/满字节/窗口去重）/ timewheel 单级时间轮 | — |
| [logx](https://github.com/oekit/logx) | 统一日志门面（多后端可替换 + Sink + caller 归属）+ 优雅关停；桥接子包 hlog/hertzlog/kitexlog 为嵌套模块按需引入 | — |
| [core](https://github.com/oekit/core) | 应用基建件（governance 治理 schema / config 配置加载 + default/env/range/options tag 校验 / lifecycle 契约 / network IP 工具 / version） | [logx](https://github.com/oekit/logx) |
| [metrics](https://github.com/oekit/metrics) | Prometheus 门面 + 弹性指标族 | [core](https://github.com/oekit/core) |
| [tracing](https://github.com/oekit/tracing) | OpenTelemetry 追踪 + trace_id 真相源 | [logx](https://github.com/oekit/logx) |
| [registry](https://github.com/oekit/registry) | 注册发现核心 + Nacos 实现 + Nacos 配置源 + sdbridge | [core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx) |
| [data](https://github.com/oekit/data) | 数据访问门面——复刻 ekit data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 re-export（类型同一，实现住 pkg 与各子仓；all/ 聚合模块已废弃 retract） | [concurrency](https://github.com/oekit/concurrency)、[gormx](https://github.com/oekit/gormx)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg)、[redisx](https://github.com/oekit/redisx)、[timertask](https://github.com/oekit/timertask) |
| [gormx](https://github.com/oekit/gormx) | SQL/GORM 全家——连接池增强、框架无关 CRUD Service、仓储（原 oekit/gorm，v0.3.0 更名） | [logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [redisx](https://github.com/oekit/redisx) | Redis 全家——客户端封装（根包）、分布式缓存 cache/、分布式锁 distlock、分布式限流 ratelimit（Lua 三态 + 故障本地兜底） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [httpx](https://github.com/oekit/httpx) | HTTP 客户端 + 负载均衡——服务发现/熔断（规范哨兵 ErrCircuitOpen）/重试/指标/OTel 透传/一致性哈希（权重感知虚拟副本） | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [network](https://github.com/oekit/network) | 轻量网络客户端——WebSocket/NATS/MQ 抽象、连接与流工具、Octopus 推送 | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[tracing](https://github.com/oekit/tracing) |
| [timertask](https://github.com/oekit/timertask) | 分片定时任务（FNV-1a、心跳续约） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [middleware](https://github.com/oekit/middleware) | Hertz 自研标准中间件栈 + contrib 桥接 + hmw + SheddingMiddleware 自适应降载 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
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
