# AGENTS.md — oekit/data

> oekit 数据访问门面——复刻 ekit 单体 data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 re-export（实现住 pkg/cache 与 gormx/redisx/timertask 各子仓）+ cached 缓存读写编排 + twolevel 两级缓存/失效广播（本仓唯二实现子包）
> 当前版本：`v0.5.0` · module：`github.com/oekit/data` · org：[oekit = Open Easy Kit](https://oekit.github.io)

## 本仓包含
纯 re-export 门面 + 一个原生子包。

八个壳子包（type alias 与源包同一类型 + 泛型函数包装）：
`cache/`（← pkg/cache）、`rediscache/`（← redisx/cache）、`crud/`（← gormx/crud）、
`distlock/`（← redisx/distlock）、`gormx/`（← gormx）、`redisx/`（← redisx）、
`timertask/`（← timertask）、`cachetest/`（← pkg/cache/cachetest）。

`cached/`（v0.6.0 起）：缓存读写编排装饰器——套在
`gormx/crud.Repository[T]` 上实现 cache-aside：写库成功后失效缓存（事务路径
在提交后统一失效）、GetByID 未命中经后端 GetOrLoad 回源回填（null marker
防穿透/singleflight 防击穿由 pkg/cache 与 redisx/cache 后端承担）、GetOne/List
查询缓存走「表级版本号 + QuerySpec 指纹」键（opt-in）；另有 GetByIDs 批量
点读与 WithBloomFilter 布隆防穿透可选件（启用前提：全量写入走装饰器 +
存量预热，否则误判会造成数据不可见窗口）。

`twolevel/`（v0.7.0 起）：TwoLevelCache[T] 两级缓存组合——L1 本地只缓存
真值、null 防穿透委托 L2 marker、Del 先远端后本地、SetWithTTL 双层传导；
Bus 失效广播（Redis pubsub）在 Del/DelBatch 时驱逐全部实例的 L1 副本
（只广播失效不广播回填）；组合体必须跑过 cachetest 契约套件。

**门面硬规则**：
- 壳文件（shell.go/generic.go）只做 alias 与转发，禁止实现逻辑；实现演进在源仓
- 源仓新增导出符号时壳需同步（跟版税）；`internal/testsame` 供壳测试做类型同一性断言
- 缓存接口与契约套件的真相源在 `github.com/oekit/pkg` 的 `cache` 子包（v0.4.0 起）
- `all/` 聚合模块已废弃（v0.1.1 retract），由本门面取代

**cached/twolevel 硬规则**：
- 失效必须发生在事务提交后（RunInTx 内删缓存会放跑「回源读到未提交旧值 →
  回填旧值」竞态）；失效失败不回滚写操作，经 WithOnError 上报，TTL 兜底
- QueryCache 的 ttl 必须为正（构造即拒绝 0）——失效失败时 TTL 是唯一安全网
- 点读键与查询键空间不重叠（`…:id:N` vs `…:q:…`），entity 缓存实例可复用
- twolevel 的双删顺序固定先远端后本地（反序本地旧值活过整个 L1 TTL）；
  Bus 发布 best-effort（失败 TTL 兜底）；订阅循环用 async.GoSafe（禁裸 go func）

## 硬规则速览（全矩阵适用）

### 分层与依赖
- 依赖严格自底向上，禁止反向依赖；本仓的矩阵内依赖见 README「矩阵总览」表
- 微工具集统一在 `github.com/oekit/pkg`；本仓新增基础工具前先确认 pkg 里没有

### 错误处理
- 一律使用 `github.com/oekit/pkg/errors`：`errors.Wrap(err, "ctx", kv...)`（nil-safe）、
  `errors.Cause`；业务错误用 BizError（BizCode 与 HTTPCode 双轨，命名工厂 NewNotFoundError 等）
- **类型同一性红线**：同一进程内只允许一套 errors 包。混用旧路径会把 BizError 的 As/Is
  判定打断（Not Found 变 500）
- handler 层统一 `mw.Fail(ctx, c, err)` 出口；所有 error 禁止 `_` 忽略

### 日志
- 门面 `github.com/oekit/logx`：业务服务 `logx.WithService("svc")`；
  **SDK/基础包必须 `logx.Delegating("组件名")` 懒解析**（禁止 Default()/WithService 声明包级 logger）
- kv 结构化风格为默认；`Ctx*` 方法自动携带 trace_id；禁止 printf 风格 `logx.Infof` 作为主要手段
- 后端经 `logx.SetDefault` 替换；`LOG_LEVEL` 环境变量统一生效

### 并发安全
- 禁止裸 `go func`：用 `github.com/oekit/concurrency/async` 的 GoSafe 系列（panic 被 recover）
- 共享状态 mutex / atomic 保护；包级全局可变状态用 `atomic.Pointer[T]`

### 生命周期
- 组件 Close 统一 `Close(ctx) error` 签名 + sync.Once 幂等
- 应用骨架用 `github.com/oekit/bootstrap/bootstrap.Run`；**userFn 返回即服务结束，
  长驻服务必须 `<-app.Context().Done()` 阻塞**

### Option 与命名
- 函数选项模式 `XxxOption`（禁止裸 `Option`，多包 co-import 冲突）
- 导出符号 PascalCase + godoc 注释；包文档放 doc.go

### 测试契约
- cache 新后端（`CacheAside[T]` 实现，etcd/consul/...）**必须**跑过
  `github.com/oekit/data/cachetest` 契约套件（`Run` 全绿，含 -race）方可接入；
  本仓 cache 自身经 `cache/contract_suite_test.go`（外部测试包）接入同一套件

### 版本与发版
- SemVer；0.x 阶段 minor 允许 breaking；发版 = 打 annotated tag 推远端（CI 自动跑 lint+test）
- module 路径 = `github.com/oekit/<name>`；**改 module 路径必须同步**：
  全部 import、go.mod、README 安装行、logx caller 启发式（若涉及 logx）

### 文档
- 本仓 README 的「矩阵总览」表由脚本生成，结构变化后需重新生成
- 历史完整规范见 ekit 单体仓（git.enjoye.top/enjoydream/ekit，冻结维护）的 AGENTS.md 与 docs/

## 本仓范围

