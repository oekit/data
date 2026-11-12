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
//     错误数据"而非"查到旧数据"，绝不接受）。判据是**递归**的（#197）：
//     无区分度元素被切片/map 等容器包住时容器编码不再恰为 {} / null，
//     但容器内的 {} / null 子树同样意味着不同取值会同编码——一并拒绝。
//     此类工作负载请用 WithFingerprint 提供类型感知的指纹。
//
// 整段 spec 只做一次 JSON 序列化（#186）：参数无区分度校验直接在
// 同一份 blob 内定位参数节点，不再逐参数重编一遍。
func DefaultFingerprint[T any](spec crud.QuerySpec) (string, error) {
	blob, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("cached: 查询指纹序列化失败: %w", err)
	}
	if err := argsDistinguishable(spec, blob); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(fpVersion+":"), blob...))
	return hex.EncodeToString(sum[:])[:16], nil
}

// argsDistinguishable 校验 spec 全部查询参数的 JSON 编码携带区分度
// （#197）：参数节点从已序列化的 spec blob 内定位，不再二次序列化
// （#186）。
//
// 定位依赖 QuerySpec/Condition/Preload 的缺省 JSON 布局（无标签字段名
// Conds/OrConds/Preloads 与 Args）。布局漂移防护：walk 到的参数节点数
// 必须与 spec 实际持有的参数数一致，不一致 fail-closed 拒绝指纹——
// 宁可降级直通，不可漏检。
func argsDistinguishable(spec crud.QuerySpec, blob []byte) error {
	var root struct {
		Conds    []argsBlob `json:"Conds"`
		OrConds  []argsBlob `json:"OrConds"`
		Preloads []argsBlob `json:"Preloads"`
	}
	if err := json.Unmarshal(blob, &root); err != nil {
		return fmt.Errorf("cached: 查询指纹自检失败（spec blob 不可解析）: %w", err)
	}
	if len(root.Conds) != len(spec.Conds) || len(root.OrConds) != len(spec.OrConds) ||
		len(root.Preloads) != len(spec.Preloads) {
		return errors.New("cached: 查询指纹自检与 spec 布局不一致（条件条数不匹配），拒绝指纹")
	}
	for i := range spec.Conds {
		if err := checkArgBlobs(root.Conds[i], spec.Conds[i].Args, fmt.Sprintf("Conds[%d]", i)); err != nil {
			return err
		}
	}
	for i := range spec.OrConds {
		if err := checkArgBlobs(root.OrConds[i], spec.OrConds[i].Args, fmt.Sprintf("OrConds[%d]", i)); err != nil {
			return err
		}
	}
	for i := range spec.Preloads {
		if err := checkArgBlobs(root.Preloads[i], spec.Preloads[i].Args, fmt.Sprintf("Preloads[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// argsBlob 是 Condition / Preload 在 spec blob 内的参数载体（两者都以
// "Args" 字段承载查询参数，共用一个解析形态）。
type argsBlob struct {
	Args []json.RawMessage `json:"Args"`
}

// checkArgBlobs 校验单个条件的参数编码：节点数必须与 Go 侧一致
// （布局漂移防护），每个参数再递归判定区分度。
func checkArgBlobs(c argsBlob, args []any, path string) error {
	if len(c.Args) != len(args) {
		return fmt.Errorf("cached: 查询指纹自检与 spec 布局不一致（%s.Args 条数不匹配），拒绝指纹", path)
	}
	for j, raw := range c.Args {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("cached: 查询参数 %s.Args[%d] 编码不可解析，拒绝指纹: %w", path, j, err)
		}
		if !distinguishable(v) {
			return fmt.Errorf("cached: 查询参数 %s.Args[%d] 编码含无区分度子树（{} / null），不同取值会同指纹，拒绝指纹", path, j)
		}
	}
	return nil
}

// distinguishable 递归判定 JSON 值是否携带区分度（#197）：{} 与 null
// 在任何嵌套位置都意味着该子树的取值信息在编码中丢失（不同取值同编码
// ⇒ 同指纹串数据）。空数组/空对象外的数组与对象逐元素下钻，任一子树
// 无区分度即整段无区分度。
func distinguishable(v any) bool {
	switch t := v.(type) {
	case nil:
		return false // null：取值信息丢失
	case map[string]any:
		if len(t) == 0 {
			return false // {}：取值信息丢失（含非导出字段结构体）
		}
		for _, e := range t {
			if !distinguishable(e) {
				return false
			}
		}
		return true
	case []any:
		for _, e := range t {
			if !distinguishable(e) {
				return false
			}
		}
		return true
	default:
		return true
	}
}
