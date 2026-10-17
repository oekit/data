# Changelog — oekit/data

由 ekit v0.34.0（git.enjoye.top/enjoydream/ekit，冻结维护）拆分而来。所有 notable 变更记录如下。

## [v0.9.3] — 2026-10-17

- fix(deps): pkg 地板 v0.9.3 → v0.10.0（拆分版，零代码改动）；gormx v0.5.3、redisx v0.7.3、concurrency v0.5.3 地板跟随

## [v0.9.2] — 2026-10-17

- fix(deps): timertask 地板 v0.1.6 → v0.1.7（reliable 租约回扫接入生命周期修复）

## [v0.9.0] — 2026-10-17

- **BREAKING** fix(cache/cachetest): 删除壳残留的原生 AsideCache 族——`cache`
  侧 `AsideCache`/`NewAsideCache`/`AsideOption`/`SourceFuncWithTTL`/`SourceFrom`/
  `TTLCacheAside`/`OnStaleFunc`/`WithStaleIfError`/`WithOnStale`/`WithAsideNullTTL`，
  `cachetest` 侧 `RunTTL`/`RunStaleIfError`。data/cache 回归纯 re-export 门面：
  缓存域已由 pkg/cache 收编（CacheAside 接口族），壳不得保留源侧不存在的符号
  （audit 门禁拦截；data#4 的动态 TTL / stale-if-error 语义归宿在 pkg 侧实现）。
  迁移：改用 `pkg/cache` 的 `CacheAside` 接口族（NewLocalCache/NewSingleFlightCache）
- feat(timertask): 壳同步 timertask v0.1.5——`DeliveryMode` 投递语义类型 +
  `WithAtLeastOnce`（租约 + Ack + 可见性超时）/`WithDeliveryMode`/
  `WithVisibilityTimeout` 可靠投递选项（oekit/timertask#2）
- docs: README 删除 AsideCache 章节，timertask 行补 at-least-once 口径

## [v0.8.2] — 2026-10-17

- fix(deps): 依赖地板 gormx v0.3.0 → v0.5.1（gormx 结构重置新基线，旧版本
  tag 已清空，原地板会导致 `go mod tidy` 与直连拉取失败）；cached 集成测试
  import 对齐 `gormx/gormrepo` 顶层路径（原 `crud/gormrepo`，重置时提升）

## [v0.8.1] — 2026-10-16

独立 code review（v0.7.0/v0.8.0 缓存族全量，探针实证）后的修复批——
一致性核心判定无缺陷，修复均为契约/文档/接线级：

- fix(twolevel): Bus「ctx 取消停止循环」契约兑现——go-redis `Subscribe(ctx)`
  只用 ctx 约束订阅命令，`Channel()` 不会随 ctx 停止；补 watcher goroutine
  （async.GoSafe + ctx.Done → stop）。测试以「第三方实例发布 → 本实例 L1
  是否被驱逐」观测循环死活（正/负控制），修复前该测试为真红
- fix(twolevel): 未知版本广播消息跳过（防未来 v2 语义被旧实例误读）；
  doc 补「TTL 必须有限」前提——LocalCache TTL=0 表示永不过期，多实例
  丢一条广播即该键无界陈旧，两级缓存场景禁止 TTL=0；Start「不报错、
  Redis 不可达时静默重连」行为如实文档化；Bus 单生命周期文档化
- fix(cached): bloom 前置门补齐降级路径——缓存后端故障降级直查同样过门
  （后端故障窗口正是 ID 扫描高发期，不得变成防穿透旁路）
- fix(cache): 门面壳同步 pkg v0.6.0 的 `BatchGetter` alias（壳同步规则
  曾被漏；`testsame` 补接口同一性断言）
- docs: README GetByIDs 段落对齐 v0.8.0 的 MGet 快路径（此前仍描述逐 ID
  行为，与发布主题矛盾）；getbyids 文档实现清单补 SingleFlightCache
- test: bloom 降级门（bloom 判不存在 → 降级路径仓储零调用）、L2 MGet
  错误整批透传

## [v0.8.0] — 2026-10-16

- feat(cached): `GetByIDs` 启用 MGet 快路径——后端具备 pkg/cache
  `BatchGetter[T]` 批量能力（LocalCache/RedisCache/SingleFlightCache 组合/
  TwoLevelCache 组合体均已实现）时一次批量往返拿真值，缺席键逐个回退
  GetOrLoad（真 miss 回源回填、null marker 在后端短路），N 次往返收敛为
  1 次批量 + 有限次回源；批量读失败自动降级逐 ID；输入去重保序
- feat(twolevel): `TwoLevelCache` 新增 `MGet` 跨层批量——L1 批量命中直达，
  L1 缺席交 L2 单次批量，L2 命中逐键回填 L1；任一层无批量能力时退化
  逐键读（装饰器透传规则：内层有批量不得在外层退化）
- docs: README cached 扩展件小节补 MGet 快路径说明；矩阵 DESC 同步
- deps: github.com/oekit/pkg v0.6.0（BatchGetter）、redisx v0.7.0
  （RedisCache.MGet 管道化）

## [v0.7.0] — 2026-10-16

- **feat(twolevel)**: 新增两级缓存子包 `twolevel`（恢复 v0.5.0 门面化时随
  all/ 一并删除的 TwoLevelCache，适配现矩阵后重新立项）：
  - `TwoLevelCache[T]`：L1 本地 + L2 任意 CacheAside（典型 RedisCache）
    读穿写透——L1 只缓存真值、null 防穿透委托 L2 marker、Del 先远端后
    本地（反序则本地旧值活过整个 L1 TTL）、SetWithTTL 双层传导；组合体
    跑过 cachetest 完整契约（int/string 双类型）+ miniredis 特性单测
  - `Bus`：跨实例 L1 失效广播（Redis pubsub）——Del/DelBatch 时发布失效
    消息，各实例订阅循环驱逐自己的 L1 副本（含发布者自身，幂等）；只
    广播失效不广播回填；发布 best-effort（失败 TTL 兜底）；坏消息容忍
    不炸订阅循环；Start/Close 生命周期合规（async.GoSafe，禁裸 go func）
- **feat(cached)**: `GetByIDs` 批量点读——逐 ID 走点读缓存路径（SingleFlight
  去重回源），未找到的 ID 不进 map 且不算错误（null marker 防穿透），
  单个 ID 硬错误 errors.Join 聚合返回不阻断其余
- **feat(cached)**: `WithBloomFilter` 布隆防穿透可选件——回源前先问布隆，
  判定"绝对不存在"直接 null marker、仓储零调用；bloom 读失败 fail-open
  放行仓储（绝不 fail-closed）；Create/事务提交后自动 Add 新 ID；文档
  明确启用前提（全量写入走装饰器 + 存量预热，否则误判造成数据不可见窗口）
- docs: README 新增 twolevel 节与 cached 扩展件小节；规划销账（两级缓存/
  失效广播已落地，仅留 etcd/consul 后端待真实基建）；AGENTS/doc.go 同步
  「唯二原生子包」口径；矩阵 DESC 更新（21 仓矩阵表 regen）
- deps: 测试依赖 + github.com/alicebob/miniredis/v2（twolevel/bus 测试）

## [v0.6.0] — 2026-10-16

- **feat(cached)**: 新增缓存读写编排子包 `cached`——`CachedRepository[T]` 装饰器
  （实现 `crud.Repository[T]` + `TxRepository[T]`，可直接作为
  `crud.NewBaseService` 仓储，事务路径经类型断言自动启用）：
  - 点读 GetByID：未命中经后端 `GetOrLoad` 回源回填（null marker 防穿透、
    singleflight 防击穿由后端承担）；ErrNotFound ↔ (nil, nil) 双向翻译；
    缓存读失败降级直查仓储
  - 写后失效：Create/BatchCreate/Update/UpdateFields/Delete/BatchDelete
    写库成功后删实体键 + bump 表级版本；失效失败**不回滚写操作**（返回错误
    会诱发重试双写），经 `WithOnError` 上报，TTL 兜底
  - 事务安全：`RunInTx` 事务内的写操作只收集失效清单，**提交成功后统一
    失效**（事务内删缓存会放跑「回源读到未提交旧值 → 回填旧值」竞态）；
    回滚不失效；事务内读直通仓储
  - 查询缓存 opt-in（`QueryCache`）：GetOne/List 走「表级版本号 +
    QuerySpec 指纹（JSON SHA-256，带算法版本号）」键，任意写整体失效；
    ForUpdate 永不缓存、负结果不缓存、指纹失败降级直通；**ttl 必须为正**
    （构造即拒绝——失效失败时 TTL 是唯一安全网）
  - 缺省 ID 探测：`GetID() int64` 接口（gormx.BaseModel 系天然满足）→
    名为 ID 的可导出 int64 字段；`WithID` 可覆盖；完全取不到 ID 时写路径
    退化为版本 bump / TTL 兜底
  - 键空间：`{prefix}:{type}:id:N` / `…:q:one|list:{ver}:{fp}` / `…:ver`，
    点读键与查询键不重叠（entity 缓存实例可复用）
  - 测试 30 例（点读/写路径/查询缓存/事务时序/嵌套事务 + sqlite×gormrepo×
    BaseService 集成），`-race` 全绿；提交后失效、版本 bump 两项关键行为
    经变异检查；独立 code review（探针实证）后修复以下问题：
  - **fix(cached)**: 嵌套 `RunInTx` 透传 fn 导致内层写绕过失效收集——
    提交后不失效、点读读到 TTL 窗口内旧值；改为重新包装收集器，
    内层写继续记账（探针复现 → 修复 → 嵌套事务测试固化）
  - **fix(cached)**: 缺省 ID 探测在结构体类型上查 `GetID`，而指针接收者
    方法（gormx.BaseModel 的 GetID 即是）只在指针方法集——纯 GetID
    实体（无可导出 ID 字段）失效静默失效；改在指针类型上探测
  - **fix(cached)**: 指纹防碰撞加固——`driver.Valuer` 风格非导出字段参数
    恒编 `{}`/`null`，不同取值同指纹会串数据（查到错误数据而非旧数据），
    此类参数 fail-closed 拒绝指纹降级直通（`DefaultFingerprint` 逐参数
    区分度校验）；chan/func 序列化失败维持原有直通
  - refactor(cached): `Option[T]` → `CachedOption[T]`（仓内硬规则禁裸
    Option；`data/cache` 壳已存在 `cache.Option` 会 co-import 冲突）
  - docs(cached): 包文档拆至 doc.go 并补「别名警示」（LocalCache 零拷贝
    返回共享引用，调用方一律只读）；QueryCache 构造期拒绝 nil 后端实例；
    提交后失效改用 `context.WithoutCancel`（调用方取消不吞失效）；
    版本读失败空版本兜底文档如实化（持续降级窗口可命中上轮空版本条目，
    仍受 TTL 约束）；`QueryPage` 文档签名笔误修正
- docs: README/doc.go/AGENTS 同步 cached 子包；矩阵 DESC 更新（21 仓矩阵表 regen）；
  补录 v0.5.0 节（此前漏记）

## [v0.5.0] — 2026-10-16

- **breaking** feat!: data 门面化——八子包 re-export 复刻 ekit data/ 能力面
  （cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest，类型为
  泛型 type alias 与源包同一类型；实现住 pkg 与各子仓）
- **breaking** feat(all)!: `all/` 聚合模块全部版本 retract——一键全家桶由本
  门面取代（期间短暂引入的 all/ 聚合嵌套模块一并废弃）
- fix(ci): all 模块测试放开 GOTOOLCHAIN=auto（共享 workflow 固定 local 的冲突）
- fix(cachetest): 修 golangci-lint v2.14 存量违规（2 处 govet shadow + 2 处 unparam）

## [v0.4.1] — 2026-10-17

- **docs**：边界段 Redis 后端指针跟随模块改名（oekit/redis → oekit/redisx，import
  `github.com/oekit/redisx/cache`）；新增「规划」节（etcd/consul 后端接入模板、两级缓存单独立项）

## [v0.4.0] — 2026-10-16

- **breaking** refactor(cache): 目录正名 `data/cache` → `cache`——import 路径
  `github.com/oekit/data/data/cache` → `github.com/oekit/data/cache`（包名保持
  `cache` 不变）；零已知消费方，redis 模块（github.com/oekit/redis）的 import
  更新由并行任务同步
- **breaking** feat(cache): `LocalCache.Close()` → `Close(ctx context.Context) error`
  对齐仓库生命周期硬规则——sync.Once 幂等保留，stop 后台 cleanup goroutine，
  ctx 预留（当前实现同步返回 nil，ctx 不阻塞）；零已知消费方
- feat(cache): 接口契约上收——把散在实现注释里的语义统一收进 interface.go 接口
  godoc：值语义（共享引用/序列化副本两种别名语义，调用方一律只读）、(nil, nil)
  双关（miss 非 error）、回填 best-effort、TTL 来源、接口不含 Close 的生命周期
  立场、null marker 契约（Source (nil,nil) → 缓存 null 标记，Exists=true）
- feat(cachetest): 新增公开契约测试套件 `github.com/oekit/data/cachetest`——8 场景
  固化 CacheAside 实现必须一致的语义（miss/往返/显式 TTL/Del 幂等/回填计数/
  null marker 防穿透/Refresh/并发混合）；**新后端（etcd/consul/...）必须跑过
  Run 方可接入**；本仓 cache 自身测试改造接入（LocalCache 直连 +
  SingleFlightCache×LocalCache 组合，经外部测试包避免 import cycle），
  删除与套件重复的用例，LocalCache 特有场景保留
- docs: Checker 幽灵修正收尾——README/AGENTS 的 Checker 提法改纯指引
  （真身在 github.com/oekit/pkg 的 cache 子包）；LRU 措辞正名（实际行为为
  写序淘汰 FIFO + 重写提升，读命中不提升，行为早已由测试固化）；边界段更新
  （Redis 后端在独立模块 github.com/oekit/redis，新后端接入须过 cachetest）

## [v0.3.4] — 2026-10-16

- docs: 修正幽灵宣称——README/AGENTS 中的 `cache.NewChecker`（Checker 刷新检查器）
  自拆分起从未在本仓存在（实现在 [pkg](https://github.com/oekit/pkg) 的 `cache` 子包，
  签名与原示例一致），删除失真宣称与编译必挂的示例，改为指向 pkg/cache；
  快速使用换为 LocalCache 真实示例

## [v0.3.3] — 2026-10-16

- build: 最低 Go 版本降至 1.24；docs: README 深化 + matrix-tools 自动化补齐（补录）

## [v0.1.0] — 2026-10-15

自 ekit v0.34.0（git.enjoye.top/enjoydream/ekit，冻结维护）拆分而来的初始公开版本，完成拆分稳定化：模块路径统一为 `github.com/oekit/data`，多轮拆分/合并收敛至当前模块边界，组织基建（共享 CI workflow、golangci v2、Dependabot）就位。