// Package cachetest 提供 cache.CacheAside[T] 实现的公开契约测试套件。
//
// # 扩展接入契约
//
// 任何新的 CacheAside 后端（etcd/consul/...）**必须**跑过本套件（Run 全绿，
// 含 -race）方可接入。套件固化 cache 包接口 godoc 承诺的实现语义——
// miss 非 error、(nil, nil) 双关、Set/Get 往返与覆盖、显式 TTL、Del 幂等、
// GetOrLoad 回填计数、null marker 防穿透、Refresh 更新、并发安全——
// 保证所有后端对调用方行为一致。
//
// # 用法
//
//	cachetest.Run(t, cachetest.Comparable[int](),
//		func(t *testing.T, source cache.SourceFunc[int]) cache.CacheAside[int] {
//			c := cache.NewLocalCache(cache.LocalConfig[int]{Source: source})
//			t.Cleanup(func() { _ = c.Close(context.Background()) })
//			return c
//		})
//
// # 设计原则（对齐 ekit-registry/registrymocks 公开测试包先例）
//
//   - 值类型无关：Contract 抽象测试值工厂与相等性；Comparable 覆盖
//     整数/字符串系可比类型，其余类型自定义 Contract
//   - 生命周期归 factory：套件不假设实现有 Close（接口契约不含生命周期），
//     factory 用 t.Cleanup 自行清理后台资源
//   - 有界等待：TTL 场景用短 TTL（50ms）+ 固定有界 sleep（120ms 量级）
//     验证过期，不做无界轮询；极端负载下由量级余量兜底
//
// 组织风格：本包只依赖被测接口（github.com/oekit/data/cache）与标准库，
// 不反向依赖任何具体实现，实现包反向 import 本包跑套件。
package cachetest
