// Package all 是 data 生态的聚合入口（umbrella module）：一次 go get 拉齐
// 数据访问全家桶，并承载跨子仓组合而成的胶水件（如两级缓存 TwoLevelCache）。
//
// 拉取本模块即引入以下能力（依赖矩阵见各子仓 README）：
//
//	github.com/oekit/data        —— 缓存抽象 CacheAside[T] + 本地缓存 + cachetest 契约套件
//	github.com/oekit/gormx       —— SQL/GORM 全家（连接池/审计/OTel 插件、框架无关 CRUD）
//	github.com/oekit/redisx      —— Redis 全家（客户端封装、分布式缓存、分布式锁 distlock、限流 ratelimit）
//	github.com/oekit/timertask   —— 分片定时任务（FNV-1a 分片、心跳续约）
//
// 使用方式：go get github.com/oekit/data/all 后，业务代码仍按能力**直接
// import 各子仓的原始包路径**（本包不是 API 门面，不做 re-export）：
//
//	import (
//	    "github.com/oekit/data/all"        // 聚合依赖（blank，保证 require 不被 tidy 清除）
//	    datacache "github.com/oekit/data/cache"
//	    redisxcache "github.com/oekit/redisx/cache"
//	)
//
// 只需要单一能力时，直接 go get 对应子仓即可，不必引入本模块——各子仓
// 独立版本、独立依赖，按需引入依赖面最小。
//
// 本文件以 blank import 固化各子仓依赖：go mod tidy 会清除未被引用的
// require，聚合语义全靠这里的 import 维系（新增子仓时同步补一行）。
package all

import (
	_ "github.com/oekit/gormx"
	_ "github.com/oekit/timertask"
)
