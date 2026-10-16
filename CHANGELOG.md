# Changelog — oekit/data

由 ekit v0.34.0（git.enjoye.top/enjoydream/ekit，冻结维护）拆分而来。所有 notable 变更记录如下。


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

## [v0.3.2] — 2026-10-15

- v0.3.2

## [v0.3.1] — 2026-10-15

- v0.3.1

## [v0.3.0] — 2026-10-15

- v0.3.0

## [v0.2.1] — 2026-10-15

- v0.2.1

## [v0.2.0] — 2026-10-15

- v0.2.0

## [v0.1.0] — 2026-10-15

- v0.1.0
