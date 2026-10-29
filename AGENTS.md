# AGENTS.md — oekit/data

> 数据访问门面——复刻 ekit data/ 能力面：cache/rediscache/crud/distlock/gormx/redisx/timertask/cachetest 八子包 re-export（类型同一，实现住 pkg 与各子仓）+ cached 缓存读写编排（写后失效/事务提交后失效/查询缓存版本号失效/GetByIDs 批量点读 MGet 快路径/bloom 防穿透）+ twolevel 两级缓存与跨实例失效广播（v0.7.0 起本仓唯二非 re-export 实现）；v0.15.0 起壳跟随源仓演进——cache 壳补 LocalCache 可选形状（EvictPolicy/Sweeper/HitGetter）、cachetest 壳补 5 个契约套件入口（套件 8 → 13 场景）、v0.18.0 起 cache 壳补 PeekGetter 别名 + cachetest 壳补 2 个 stale-if-error 契约入口（套件 13 → 15 场景）、timertask 壳的 Redis 形态随源仓 v0.2.0 迁至嵌套模块 timertask/redis（壳不再转发 RedisBackend/ShardManager，Redis 形态直接依赖该嵌套模块）；all/ 聚合模块已废弃 retract
> 当前版本：`v0.19.6` · module：`github.com/oekit/data` · org：[oekit = Open Easy Kit](https://oekit.github.io)

## 本仓包含
`./`、`cache/`、`cached/`、`cachetest/`、`crud/`、`distlock/`、`gormx/`、`internal/testsame/`、`rediscache/`、`redisx/`、`timertask/`、`twolevel/`

## 硬规则速览（全矩阵适用）

### 分层与依赖
- 依赖严格自底向上，禁止反向依赖；本仓的矩阵内依赖见 README「矩阵总览」表
- 微工具集统一在 `github.com/oekit/pkg`；本仓新增基础工具前先确认 pkg 里没有
- `pkg` 是**零第三方依赖仓**（go.mod 零 require，仅标准库，CI 强制）——需要三方能力的基础工具拆独立仓（先例：kitx）

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

### 版本与发版
- SemVer；0.x 阶段 minor 允许 breaking
- **main 受分支保护，改动一律走 PR，管理员也不例外**（`enforce_admins=true`，
  直推会被 `GH006 Changes must be made through a pull request` 拒掉）
- 发版流程：建分支 → 提交 → 开 PR → CI（`Lint & Test / Lint` + `Lint & Test / Test`）
  绿 → 合入 main → **合入之后**再打 annotated tag 推远端
- tag 不走 PR（`protect-release-tags` ruleset 管的是不可删/不可改，不是审批）；
  tag 必须落在已合入 main 的 commit 上，否则 `go get` 拉到的是没进过 CI 的代码
- module 路径 = `github.com/oekit/<name>`；**改 module 路径必须同步**：
  全部 import、go.mod、README 安装行、logx caller 启发式（若涉及 logx）

### 文档
- 本仓 README 的「矩阵总览」表由脚本生成，结构变化后需重新生成
- 历史完整规范见 ekit 单体仓（git.enjoye.top/enjoydream/ekit，冻结维护）的 AGENTS.md 与 docs/

## 本仓范围

