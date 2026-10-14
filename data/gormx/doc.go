// Package gormx 提供 GORM 数据库客户端封装。
//
// 所有 enjoydream 后端服务共享此包，统一封装：自定义 Logger、OTel 追踪、连接池配置。
//
// # 快速接入
//
// 基本用法：
//
//	db, err := gorm.Open(mysql.Open(dsn))
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// 自定义连接池：
//
//	db, err := gorm.OpenWithPool(mysql.Open(dsn),
//	    10,              // maxIdleConns
//	    100,             // maxOpenConns
//	    30*time.Minute,  // connMaxLifetime
//	)
//
// # OTel 追踪
//
// Open 和 OpenWithConfig 会自动注入内建 OTel tracing 插件 (NewOtelPlugin)，
// 所有 SQL 查询会自动创建 Span，无需手动配置。
// 仅支持 MySQL 驱动的 server.address 提取，不引入 clickhouse/postgres 依赖。
//
// # 自定义 Logger
//
// 默认使用 ekit 内置 Logger（基于 hertz hlog），支持：
//   - 慢查询阈值：默认 200ms
//   - 日志级别：默认 Info
//   - OTel Span 日志关联
package gormx
