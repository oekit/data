package cache

import (
	"context"
	"time"
)

// Cache 是泛型缓存接口，面向接口编程。
// 只定义通用的缓存操作，不绑定具体实现或模式。
//
// # 实现契约（所有 Cache[T]/CacheAside[T] 实现必须遵守）
//
// 值语义（别名警告）：
//
//	实现**可以**返回缓存内部共享引用（零拷贝热路径，如 LocalCache），
//	调用方**禁止改写**返回的 *T 指向的值——改写会直接污染缓存条目，
//	并与并发读构成 data race；需要可变副本时调用方自行拷贝。
//	序列化型实现（如 Redis 后端）每次反序列化返回新副本，天然无别名。
//	同一接口允许两种别名语义，因此调用方一律按"只读"对待返回值。
//	Set/SetWithTTL 传入的 value 同理：实现不拷贝时调用方此后不得改写。
//
// (nil, nil) 双关：
//
//	Get/GetOrLoad 返回 (nil, nil) 表示"数据不存在"——key miss 或命中
//	null marker（见 SourceFunc）二者之一；error 非 nil 时值无效。
//	miss 不是 error，不应记错误日志。
//
// 回填 best-effort：
//
//	GetOrLoad/Refresh 从 Source 取到数据后的回填写入失败**不阻断**返回
//	已取到的数据（实现自行记日志）；下次请求会重新回源。
//
// TTL 来源：
//
//   - Set：使用实现构造时配置的默认 TTL；
//   - SetWithTTL：显式传参；
//   - GetOrLoad 回填：使用默认 TTL。
//
// TTL<=0 的语义（0 = 不过期还是实现默认值）由各实现的配置文档定义
// （LocalCache：0 = 不过期；Redis 后端见各自 Config）。
//
// 生命周期：
//
//	接口不含 Close——生命周期属于具体实现的资源管理，不由接口约束。
//	需要停止后台资源的实现自行提供 Close，并遵循仓库硬规则签名
//	Close(ctx context.Context) error + sync.Once 幂等。
type Cache[T any] interface {
	// Get 获取缓存。
	// 返回 (nil, nil) 表示数据不存在（见包级"实现契约"）。
	Get(ctx context.Context, key string) (*T, error)

	// Set 设置缓存（使用实现构造时的默认 TTL）。
	Set(ctx context.Context, key string, value *T) error

	// SetWithTTL 设置缓存（显式 TTL）。
	SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error

	// Del 删除缓存。key 不存在时不得报错（幂等）。
	Del(ctx context.Context, key string) error

	// DelBatch 批量删除缓存。同 Del 幂等。
	DelBatch(ctx context.Context, keys ...string) error

	// Exists 检查缓存是否存在。
	// 对 null marker 返回 true（"缓存知道该 key 不存在"也是一种存在）。
	Exists(ctx context.Context, key string) (bool, error)
}

// SourceFunc 缓存未命中时的数据获取函数。
//
// 返回 (nil, nil) 表示数据不存在——实现会缓存 null marker 防穿透：
// 后续 GetOrLoad 命中 null marker 时直接返回 (nil, nil)，不再调用 Source；
// Exists 对 null marker 返回 true。
//
// null 标记的 TTL 语义由各实现的配置定义
// （LocalCache：NullTTL 0 = 默认 1 分钟，负值 = 显式禁用空值缓存）。
//
// Source 返回的 *T 所有权随值语义契约：实现回填后调用方与实现共享引用
// 或各持副本，取决于实现（同"实现契约"的值语义，调用方一律只读对待）。
type SourceFunc[T any] func(ctx context.Context, key string) (*T, error)

// CacheAside 是 Cache-Aside 模式扩展接口。
// 在 Cache 基础上增加数据源回填、刷新等能力。
//
// GetOrLoad/Refresh 遵循包级"实现契约"：miss 非 error、回填 best-effort、
// null marker 防穿透。
//
// 新增后端（etcd/consul/...）必须实现本接口并通过
// github.com/oekit/data/cachetest 契约测试套件（Run 全绿）方可接入。
type CacheAside[T any] interface {
	Cache[T]

	// GetOrLoad 获取缓存，未命中时通过 Source 加载并回填。
	// Source 返回 (nil, nil) 时缓存 null marker（防穿透，TTL 见 SourceFunc）；
	// 后续调用命中 null marker 直接返回 (nil, nil)，不再调用 Source。
	GetOrLoad(ctx context.Context, key string) (*T, error)

	// Refresh 刷新缓存（删除后重新从 Source 加载）。
	Refresh(ctx context.Context, key string) (*T, error)
}
