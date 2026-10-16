# data

> 缓存抽象与本地缓存——LocalCache/SingleFlightCache/缓存键构造/契约测试套件 cachetest；
> 嵌套聚合模块 `all/` 一键拉齐数据全家桶并承载 TwoLevelCache 两级缓存

> 🌐 项目主页：**https://oekit.github.io/** · 名称由来：**O**pen **E**asy **Kit**（开源 · 易用 · 工具集合）
> 🤖 AI Agent：本仓规范与硬规则见 [AGENTS.md](AGENTS.md)；机器可读索引见 [llms.txt](https://oekit.github.io/llms.txt)（接口签名以源码为准）。

## 安装

```bash
go get github.com/oekit/data@v0.4.1      # 缓存抽象 + 本地缓存（零数据库驱动依赖）
go get github.com/oekit/data/all@v0.1.0  # 聚合全家桶：data + gormx + redisx + timertask
```

## 这个仓库做什么

`data` 提供**缓存抽象与本地缓存**：`CacheAside` 接口 + `LocalCache`（TTL/容量上限/
空值防穿透）+ `SingleFlightCache`（并发回源合并防击穿）+ 缓存键构造 +
`cachetest` 契约测试套件。

**嵌套聚合模块 [all/](https://github.com/oekit/data/tree/main/all)** 是数据访问
全家桶入口：一次引入 `data + gormx + redisx + timertask` 四个模块，并承载跨子仓
组合的胶水件（`TwoLevelCache` 两级缓存）。只需要单一能力时直接 go get 对应子仓
（依赖面最小），各子仓独立版本；`all/` 的依赖 pin 由 dependabot 每周自动跟随
各子仓发版。业务代码仍直接 import 各子仓原始包路径——`all/` 不是 API 门面。

> 需要刷新检查器（TTL 后台刷新/failTTL）？见 [pkg](https://github.com/oekit/pkg) 的 `cache` 子包。

## 快速使用

```go
import "github.com/oekit/data/cache"

c := cache.NewLocalCache(cache.LocalConfig[HotConfig]{
    TTL:     time.Minute, // 过期时间；MaxSize 容量上限；NullTTL 空值防穿透
    MaxSize: 1024,
    Source: func(ctx context.Context, key string) (*HotConfig, error) {
        return loadHotConfig(ctx, key) // 未命中回源（Cache-Aside）
    },
})
cfg, err := c.GetOrLoad(ctx, "tenant:acme")
```

两级缓存（L1 本地 + L2 Redis 读穿写透，`all/` 模块）：

```go
import (
    datacache "github.com/oekit/data/cache"
    "github.com/oekit/data/all"
    redisxcache "github.com/oekit/redisx/cache"
)

tl := all.NewTwoLevelCache(
    datacache.NewLocalCache(datacache.LocalConfig[HotConfig]{TTL: 30 * time.Second}), // L1 短寿
    redisxcache.NewRedisCache(redisCli, redisxcache.RedisConfig[HotConfig]{
        Prefix: "hot:", TTL: 24 * time.Hour, Source: loadHotConfig, // L2 + 回源
    }),
)
defer tl.Close(ctx)
cfg, err := tl.GetOrLoad(ctx, "tenant:acme") // L1 命中不打 L2；null 防穿透由 L2 marker 承担
```

## 边界

- Redis 后端（同一接口的 Redis 实现）在独立模块 [redisx](https://github.com/oekit/redisx)
- 新后端（etcd/consul 等）实现 `CacheAside[T]` 并跑过 [cachetest](https://github.com/oekit/data/tree/main/cachetest)
  契约套件（`Run` 全绿）方可接入
- SQL/GORM 在 [gormx](https://github.com/oekit/gormx)；定时任务在 [timertask](https://github.com/oekit/timertask)
- 根模块不依赖任何数据库驱动；`all/` 聚合模块经子仓引入 gorm/go-redis

## 规划

- 新缓存后端（etcd/consul 等）：实现 `CacheAside[T]` + 跑过 cachetest 套件即可接入，等真实需求立项
- 两级缓存失效广播（Redis pubsub 主动失效 L1）：单独立项


## 矩阵总览（23 活跃模块，自动生成）

| 模块 | 内容 | 矩阵内依赖 |
|---|---|---|
| [pkg](https://github.com/oekit/pkg) | 微工具集 20 合 1（errors/bloomx/contextx/validator/stringx/slicex/enumx/convert/encoding/jsonrepair/httpjson/mathx/pathx/pagination/ptrx/timex/uidgen/cache/bufiox/strmap） | — |
| [concurrency](https://github.com/oekit/concurrency) | 并发与流控原语（async/gopool/group/singleflight/progress/syncx/ratelimit/retry）+ window 滑动窗口 / breaker 自适应熔断（规范哨兵 ErrCircuitOpen）/ shedding CPU 自适应降载 | — |
| [logx](https://github.com/oekit/logx) | 统一日志门面（多后端可替换 + Sink + caller 归属）+ 优雅关停；桥接子包 hlog/hertzlog/kitexlog 为嵌套模块按需引入 | — |
| [core](https://github.com/oekit/core) | 应用基建件（governance 治理 schema / config 配置加载 + default/env/range/options tag 校验 / lifecycle 契约 / network IP 工具 / version） | [logx](https://github.com/oekit/logx) |
| [istio](https://github.com/oekit/istio) | Istio Proxyless——Kitex xDS 动态熔断 | — |
| [metrics](https://github.com/oekit/metrics) | Prometheus 门面 + 弹性指标族 | [core](https://github.com/oekit/core) |
| [tracing](https://github.com/oekit/tracing) | OpenTelemetry 追踪 + trace_id 真相源 | [logx](https://github.com/oekit/logx) |
| [registry](https://github.com/oekit/registry) | 注册发现核心 + Nacos 实现 + Nacos 配置源 + sdbridge | [core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx) |
| [data](https://github.com/oekit/data) | LocalCache（TTL/容量/空值防穿透）+ SingleFlightCache + 缓存键构造；嵌套聚合模块 all/ 一键拉齐数据全家桶 + TwoLevelCache 两级缓存（Checker 在 pkg/cache） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [gormx](https://github.com/oekit/gormx) | SQL/GORM 全家——连接池增强、框架无关 CRUD Service、仓储（原 oekit/gorm，v0.3.0 更名） | [logx](https://github.com/oekit/logx)、[pkg](https://github.com/oekit/pkg) |
| [redisx](https://github.com/oekit/redisx) | Redis 全家——客户端封装（根包）、分布式缓存 cache/、分布式锁 distlock、分布式限流 ratelimit（Lua 三态 + 故障本地兜底） | [concurrency](https://github.com/oekit/concurrency)、[data](https://github.com/oekit/data)、[logx](https://github.com/oekit/logx) |
| [httpx](https://github.com/oekit/httpx) | HTTP 客户端 + 负载均衡——服务发现/熔断（规范哨兵 ErrCircuitOpen）/重试/指标/OTel 透传 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [network](https://github.com/oekit/network) | 轻量网络客户端——WebSocket/NATS/MQ 抽象、连接与流工具、Octopus 推送 | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx)、[tracing](https://github.com/oekit/tracing) |
| [timertask](https://github.com/oekit/timertask) | 分片定时任务（FNV-1a、心跳续约） | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [middleware](https://github.com/oekit/middleware) | Hertz 自研标准中间件栈 + contrib 桥接 + hmw + SheddingMiddleware 自适应降载 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [nns](https://github.com/oekit/nns) | NNS 服务治理客户端——provider 无关发现/配置/实例缓存 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[registry](https://github.com/oekit/registry) |
| [nns-endpoint](https://github.com/oekit/nns-endpoint) | NNS 端点工厂——per-service 熔断、换实例重试、gRPC 池 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[httpx](https://github.com/oekit/httpx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[registry](https://github.com/oekit/registry) |
| [bootstrap](https://github.com/oekit/bootstrap) | 应用生命周期引导——bootstrap.Run 骨架 + healthz | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [handler](https://github.com/oekit/handler) | HTTP handler 层——通用 CRUD Handler、参数绑定、版本端点 | [concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[gormx](https://github.com/oekit/gormx)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[middleware](https://github.com/oekit/middleware)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [hertz](https://github.com/oekit/hertz) | Hertz 运行时集成——日志桥接、生命周期、SSE/health/metrics、适配器、OTel | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
| [kitex](https://github.com/oekit/kitex) | Kitex 生态——日志桥接、治理热更、生命周期、BizError 桥接、kitexotel | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry) |
| [workflow](https://github.com/oekit/workflow) | 组合式流程编排——串行/并行/异步/条件分支 + Hook AOP | [concurrency](https://github.com/oekit/concurrency)、[logx](https://github.com/oekit/logx) |
| [kitex-examples](https://github.com/oekit/kitex-examples) | Kitex echo 示例——client/server + IDL + 治理配置 | [bootstrap](https://github.com/oekit/bootstrap)、[concurrency](https://github.com/oekit/concurrency)、[core](https://github.com/oekit/core)、[kitex](https://github.com/oekit/kitex)、[logx](https://github.com/oekit/logx)、[metrics](https://github.com/oekit/metrics)、[nns](https://github.com/oekit/nns)、[pkg](https://github.com/oekit/pkg)、[registry](https://github.com/oekit/registry)、[tracing](https://github.com/oekit/tracing) |
> 依赖严格自底向上；微库已合并为 pkg，旧微库仓已删除。

> 本仓在矩阵中的位置与依赖见上表「矩阵内依赖」列。历史沿革：源自 ekit 单体库
> （git.enjoye.top/enjoydream/ekit v0.34.0，冻结维护），2026-10 拆分为独立 module。

## License

MIT — 见 [LICENSE](LICENSE)
