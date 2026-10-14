// Package distlock 提供分布式锁门面。
//
// 定义 Locker 接口，支持多种后端实现（Redis、etcd、ZooKeeper 等）。
// 当前提供 RedisLocker 实现。
//
// # 核心特性
//
//   - Locker 接口，面向接口编程
//   - SetNX 加锁 + Lua 原子释放（防止误删他人的锁）
//   - 支持单次尝试（TryLock）和带超时重试（Lock）
//   - 便捷包装 WithLock（加锁→执行→自动释放）
//   - 可选无 Redis 时降级放行（单实例部署场景）
//   - 支持 redis.UniversalClient（兼容单机、集群、哨兵模式）
//
// # 快速接入（推荐配合 ekit/data/redisx 使用）
//
//	redisClient, _ := redisx.NewClientFromAddr("localhost:6379", "", 0)
//	locker := distlock.NewRedisLocker(redisClient.Client(), distlock.WithPrefix("fuxi"), distlock.WithGracefulDegradation())
//
//	// 便捷包装（推荐）
//	err := distlock.WithLock(ctx, locker, "deploy:myapp:dev", 5*time.Minute, 10*time.Second, func() error {
//	    return doDeploy()
//	})
//
// # 完整服务集成示例
//
// 服务启动时初始化 Locker，建议作为全局变量或依赖注入：
//
//	var locker distlock.Locker
//
//	func init() {
//	    redisClient, err := redisx.NewClientFromAddr("localhost:6379", "", 0)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    locker = distlock.NewRedisLocker(
//	        redisClient.Client(),
//	        distlock.WithPrefix("myservice"),
//	        distlock.WithGracefulDegradation(),
//	        distlock.WithOnLock(func(key string, acquired bool, err error) {
//	            if err != nil {
//	                log.Printf("[distlock] lock error: key=%s err=%v", key, err)
//	            }
//	        }),
//	    )
//	}
//
// # 使用场景
//
// 场景一：定时任务互斥（推荐 WithLock）
//
// 多实例部署时，确保只有一个实例执行定时任务：
//
//	err := distlock.WithLock(ctx, locker, "cron:daily-report", 10*time.Minute, 30*time.Second, func() error {
//	    return generateDailyReport()
//	})
//	if errors.Is(err, distlock.ErrLockTimeout) {
//	    log.Println("其他实例正在执行，跳过")
//	    return nil
//	}
//
// 场景二：资源操作保护（TryLock 单次尝试）
//
// 防止并发操作同一资源，失败立即返回：
//
//	ownerID, err := locker.TryLock(ctx, "order:"+orderID, 30*time.Second)
//	if errors.Is(err, distlock.ErrLockNotAcquired) {
//	    return errors.New("订单正在处理中，请稍后重试")
//	}
//	if err != nil {
//	    return err
//	}
//	defer locker.Unlock(ctx, "order:"+orderID, ownerID)
//	return processOrder(orderID)
//
// 场景三：部署流水线（Lock 带超时）
//
// 等待锁释放后执行部署：
//
//	ownerID, err := locker.Lock(ctx, "deploy:"+env, 5*time.Minute, 10*time.Minute)
//	if err != nil {
//	    return fmt.Errorf("获取部署锁失败: %w", err)
//	}
//	defer locker.Unlock(ctx, "deploy:"+env, ownerID)
//	return runDeploy(env)
//
// # 错误处理
//
// 包定义了两个哨兵错误，用于判断锁获取失败的原因：
//
//   - [ErrLockNotAcquired]：TryLock 时锁被占用（非阻塞失败）
//   - [ErrLockTimeout]：Lock 超时未获取到锁
//
// 其他错误表示 Redis 通信问题，应记录日志并适当处理。
//
// # TTL 与超时建议
//
//   - TTL（锁持有时间）：应大于业务执行时间，防止锁提前过期
//   - Timeout（获取超时）：根据业务容忍度设置，建议为 TTL 的 2-3 倍
//   - 重试间隔：默认 100ms，可通过 WithRetryInterval 调整
//
// # 降级模式
//
// 启用 WithGracefulDegradation 后，当 Redis 不可用时（client 为 nil）：
//   - TryLock 返回 ("no-redis", nil)
//   - Lock 返回 ("no-redis", nil)
//   - Unlock 返回 nil
//
// 适用于开发环境或单实例部署场景，不建议在生产集群中使用。
package distlock
