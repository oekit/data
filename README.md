# ekit-data

> 存储层——GORM 增强、通用 CRUD、缓存、分布式锁、定时任务。

本仓库是 [ekit](https://git.enjoye.top/enjoydream/ekit)（`git.enjoye.top/enjoydream/ekit` v0.34.0）
按框架耦合分层拆分出的独立 Go module，属于 **yi-nology** ekit 子项目矩阵。

## 安装

```bash
go get github.com/yi-nology/ekit-data@v0.1.0
```

## 包清单

- `data/gormx/` — 连接池（函数选项）、Paginate、事务、FilterBuilder
- `data/crud/` — 框架无关 CRUD Service（`TxRepository` 事务 + FOR UPDATE 防丢更新）；`crud/gormrepo/` — GORM 仓储实现
- `data/cache/` — LocalCache（LRU O(1)、TOCTOU 修复）+ SingleFlight；`cache/redis/` — Redis 后端
- `data/redisx/` — Redis 客户端封装
- `data/distlock/` — Redis 分布式锁
- `data/timertask/` — 分片定时任务（FNV-1a hash）

## 依赖关系

子项目矩阵与依赖方向（自底向上）：

```
ekit-core                      纯工具层（零内部依赖）
  └─ ekit-observability        日志/追踪/指标门面
       └─ ekit-registry        注册发现 + 配置
            ├─ ekit-data       存储层
            ├─ ekit-network    网络客户端
            │    └─ ekit-nns   服务治理客户端
            │         └─ ekit-bootstrap  应用引导
            ├─ ekit-hertz      Hertz 生态
            ├─ ekit-kitex      Kitex 生态
            └─ ekit-workflow   流程编排
```

依赖：[ekit-core](https://github.com/yi-nology/ekit-core)、[ekit-observability](https://github.com/yi-nology/ekit-observability)。

## 与 ekit 单体仓库的关系

- 代码与 ekit v0.34.0 等价，仅 module 路径从 `git.enjoye.top/enjoydream/ekit/...` 改为 `github.com/yi-nology/ekit-data/...`
- 后续演进以各子仓库独立发版（SemVer）；ekit 单体仓库保留完整历史与架构文档（docs/PLATFORM_CONTRACT.md）
- 开发规范（命名/错误处理/并发安全/日志门面）沿用 ekit AGENTS.md 约定

## License

见 [LICENSE](LICENSE)
