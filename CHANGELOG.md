# Changelog — oekit/data

由 ekit v0.34.0（git.enjoye.top/enjoydream/ekit，冻结维护）拆分而来。所有 notable 变更记录如下。


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