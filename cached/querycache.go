package cached

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
)

// QueryPage 是 List 查询缓存的结果形态（对应仓储 List 的
// ([]T, int64, error) 三元组）。
type QueryPage[T any] struct {
	Items []T
	Total int64
}

// FingerprintFunc 将查询物化形态折算为稳定的指纹字符串。
// 返回 error 或空串时该次查询降级直通（绝不以错误指纹命中错误结果）。
type FingerprintFunc func(spec crud.QuerySpec) (string, error)

// fpVersion 是指纹算法版本号：QuerySpec 结构演进（新增字段等）时递增，
// 避免新旧指纹碰撞——滚动发布窗口内新旧实例共写同一缓存时尤其必要。
const fpVersion = "v1"

// QueryCache 是查询缓存（GetOne/List）的存储面：单条结果、整页结果、
// 表级版本号三个键空间 + 条目 TTL + 指纹算法。
//
// 键由 CachedRepository 组装（"前缀:类型:q:one|list:版本:指纹"），
// entity 键空间与点读键空间（"前缀:类型:id:N"）互不重叠，因此 entity
// 可以复用点读后端的同一个缓存实例（本地缓存直接传同一实例；Redis
// 后端传同前缀实例即可）。
type QueryCache[T any] struct {
	entity      cache.Cache[T]
	page        cache.Cache[QueryPage[T]]
	versions    cache.Cache[string]
	ttl         time.Duration
	fingerprint FingerprintFunc
}

// NewQueryCache 创建查询缓存。
//
//   - entity：GetOne 结果缓存（cache.Cache[T]），可与点读后端共享实例；
//   - page：List 结果缓存；
//   - versions：表级版本号缓存，Set 走其默认 TTL——务必配置长 TTL
//     （远大于条目 TTL；版本键过期只导致多一次重建，不致脏读，但
//     频繁重建徒增回源压力）；
//   - ttl：条目 TTL，必须为正——写后失效（bump 版本/删键）失败时
//     TTL 是唯一安全网，无 TTL 意味着可能永久脏读。
//
// entity/page/versions 任意为 nil 在构造期即拒绝（否则到使用时才
// nil panic）。
func NewQueryCache[T any](
	entity cache.Cache[T],
	page cache.Cache[QueryPage[T]],
	versions cache.Cache[string],
	ttl time.Duration,
) (*QueryCache[T], error) {
	if entity == nil || page == nil || versions == nil {
		return nil, errors.New("cached: QueryCache 的 entity/page/versions 缓存实例均必填")
	}
	if ttl <= 0 {
		return nil, errors.New("cached: QueryCache ttl 必须为正——失效失败时 TTL 是唯一安全网")
	}
	return &QueryCache[T]{
		entity:      entity,
		page:        page,
		versions:    versions,
		ttl:         ttl,
		fingerprint: DefaultFingerprint[T],
	}, nil
}

// WithFingerprint 覆盖默认指纹算法。
func (qc *QueryCache[T]) WithFingerprint(fn FingerprintFunc) *QueryCache[T] {
	qc.fingerprint = fn
	return qc
}

// DefaultFingerprint 默认指纹：JSON(QuerySpec) 的 SHA-256 前 16 hex。
// QuerySpec 字段有序、JSON 对 map key 排序，输出稳定。
//
// 两类参数 fail-closed 拒绝指纹（降级直通，绝不以错误指纹命中错误结果）：
//   - 序列化失败（chan/func 等）；
//   - 序列化成功但**无区分度**——driver.Valuer 风格的非导出字段结构体
//     恒编为 {}、nil 恒编为 null，不同取值会同指纹串数据（这是"查到
//     错误数据"而非"查到旧数据"，绝不接受）。此类工作负载请用
//     WithFingerprint 提供类型感知的指纹。
func DefaultFingerprint[T any](spec crud.QuerySpec) (string, error) {
	for _, c := range spec.Conds {
		for _, arg := range c.Args {
			if err := fingerprintable(arg); err != nil {
				return "", err
			}
		}
	}
	for _, c := range spec.OrConds {
		for _, arg := range c.Args {
			if err := fingerprintable(arg); err != nil {
				return "", err
			}
		}
	}
	for _, p := range spec.Preloads {
		for _, arg := range p.Args {
			if err := fingerprintable(arg); err != nil {
				return "", err
			}
		}
	}
	blob, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("cached: 查询指纹序列化失败: %w", err)
	}
	sum := sha256.Sum256(append([]byte(fpVersion+":"), blob...))
	return hex.EncodeToString(sum[:])[:16], nil
}

// fingerprintable 校验单个查询参数能否进指纹：不可序列化或序列化结果
// 无区分度（{} / null）都不放行。
func fingerprintable(arg any) error {
	blob, err := json.Marshal(arg)
	if err != nil {
		return fmt.Errorf("cached: 查询参数 %T 序列化失败: %w", arg, err)
	}
	if s := string(blob); s == "{}" || s == "null" {
		return fmt.Errorf("cached: 查询参数 %T 序列化为 %s，无法区分取值，拒绝指纹", arg, s)
	}
	return nil
}
