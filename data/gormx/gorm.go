package gormx

import (
	"fmt"
	"time"

	"github.com/yi-nology/ekit-observability/observability/logx"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

var gormLog = logx.Delegating("gormx")

// OpenOption 配置 Open 的行为（函数选项模式）。
type OpenOption func(*openConfig)

type openConfig struct {
	gormOptions   []gorm.Option
	skipTracing   bool
	maxIdleConns  int
	maxOpenConns  int
	connLifetime  time.Duration
	connIdleTime  time.Duration
	configurePool bool
}

// WithGormOptions 追加底层 gorm.Option。
func WithGormOptions(opts ...gorm.Option) OpenOption {
	return func(c *openConfig) {
		c.gormOptions = append(c.gormOptions, opts...)
	}
}

// WithoutTracing 跳过自动注入 OTel 追踪插件。
// 默认 Open 会注入 NewOtelPlugin()；调用此选项可关闭（如已自行注册或不需要追踪）。
func WithoutTracing() OpenOption {
	return func(c *openConfig) {
		c.skipTracing = true
	}
}

// WithConnectionPool 配置连接池参数（避免 OpenWithPool 的位置参数易错问题）。
func WithConnectionPool(maxIdleConns, maxOpenConns int, connMaxLifetime time.Duration) OpenOption {
	return func(c *openConfig) {
		c.maxIdleConns = maxIdleConns
		c.maxOpenConns = maxOpenConns
		c.connLifetime = connMaxLifetime
		c.configurePool = true
	}
}

// WithConnMaxIdleTime 配置连接最大空闲时间。
// 空闲超过此时间的连接会被关闭，防止 stale 连接积累。
func WithConnMaxIdleTime(d time.Duration) OpenOption {
	return func(c *openConfig) {
		c.connIdleTime = d
		c.configurePool = true
	}
}

// Open 使用指定的 dialector 打开数据库连接，自动注入默认日志和（可选）OTel 追踪插件。
//
// 默认行为：
//   - 注入默认 Logger（Info 级别）
//   - 注入 OTel 追踪插件（可用 WithoutTracing 关闭）
//
// 可通过选项配置连接池等：
//
//	db, err := gormx.Open(
//	    mysql.Open(dsn),
//	    gormx.WithConnectionPool(10, 100, time.Hour),
//	)
//
// 如需关闭追踪（例如已自行注册）：
//
//	db, err := gormx.Open(mysql.Open(dsn), gormx.WithoutTracing())
func Open(dialector gorm.Dialector, opts ...OpenOption) (*gorm.DB, error) {
	cfg := &openConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	defaultLogger := NewDefaultLogger()

	gormCfg := gorm.Config{
		Logger: defaultLogger.LogMode(gormLogger.Info),
	}
	for _, gormOpt := range cfg.gormOptions {
		if err := gormOpt.Apply(&gormCfg); err != nil {
			return nil, fmt.Errorf("apply gorm option: %w", err)
		}
	}

	db, err := gorm.Open(dialector, &gormCfg)
	if err != nil {
		gormLog.Error("failed opening connection to db", "err", err)
		return nil, err
	}

	if cfg.configurePool {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}
		sqlDB.SetMaxIdleConns(cfg.maxIdleConns)
		sqlDB.SetMaxOpenConns(cfg.maxOpenConns)
		sqlDB.SetConnMaxLifetime(cfg.connLifetime)
		if cfg.connIdleTime > 0 {
			sqlDB.SetConnMaxIdleTime(cfg.connIdleTime)
		}
	}

	if !cfg.skipTracing {
		if err := db.Use(NewOtelPlugin()); err != nil {
			gormLog.Error("failed use trace plugin", "err", err)
			return nil, err
		}
	}

	return db, nil
}
