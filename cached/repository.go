package cached

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
	"github.com/oekit/pkg/errors"
)

// 本文件是装饰器本体与读路径职责（#179 拆分）：CachedRepository 类型、
// 构造入口、回源函数与全部读方法；键空间（keys.go）、ID 访问器
// （idaccessor.go）、配置项（options.go）、写路径与失效（invalidation.go）、
// 事务路径（txcollector.go）各自成文。

// CachedRepository 是带缓存编排的仓储装饰器，实现 crud.Repository[T, ID]
// 与 crud.TxRepository[T, ID]。可直接作为 crud.NewBaseService 的仓储入参
// ——事务路径经类型断言自动启用，失效在提交后执行。
// ID 为实体主键类型（int64 自增 / string 文本主键等，见 crud.Repository）。
type CachedRepository[T any, ID comparable] struct {
	cfg    config[T, ID]
	entity cache.CacheAside[T]

	// bloom 补录失败的漏录账本与降级快照（#224）：bloomAdd 写失败的元素
	// 进 pending，下一次成功补录时合并重放；bloomDown 是「pending 非空」
	// 的 atomic 快照，bloomGate 据此 fail-open 直查库（读热路径无锁）。
	bloomMu      sync.Mutex
	bloomPending map[string]struct{}
	bloomDown    atomic.Bool
}

// 编译期接口契约：装饰器必须完整满足（含事务能力）仓储接口，
// 否则 NewBaseService 的类型断言会静默走无事务回退路径。
var _ = assertRepositoryContract[int64, int64]

func assertRepositoryContract[T any, ID comparable]() {
	var _ crud.TxRepository[T, ID] = (*CachedRepository[T, ID])(nil)
}

// NewRepository 创建缓存编排仓储装饰器。
// newCache 必填：装饰器不代持后端构造，TTL/容量/序列化等后端参数
// 全部留在调用方手里，装饰器只注入回源函数。
//
// 构造期 fail-fast（#184）：repo / newCache 为 nil、工厂产物为 nil 都在
// 装配点 panic（配置错误启动即暴露的对齐纪律，同 timertask.WithRetry），
// 不留「nil 在使用期才 panic、距装配点远且无现场」的坑。构造器只返回
// 指针不留 error（保持既有签名不破坏消费方），因此以 panic 承载装配期
// 契约。与 NewQueryCache「任意为 nil 构造期即拒绝」的口径一致。
func NewRepository[T any, ID comparable](repo crud.Repository[T, ID], newCache CacheFactory[T], opts ...CachedOption[T, ID]) *CachedRepository[T, ID] {
	if repo == nil {
		panic("cached: NewRepository 的底层仓储 repo 不得为 nil——装配期即暴露，否则首个使用点才 nil panic 且无装配现场")
	}
	if newCache == nil {
		panic("cached: NewRepository 的 newCache 工厂不得为 nil——装饰器不代持后端构造（见 CacheFactory），缺工厂无法回填缓存")
	}
	cfg := config[T, ID]{
		repo:   repo,
		prefix: "cached",
		typ:    defaultTypeName[T](),
	}
	for _, o := range opts {
		o(&cfg)
	}
	// 构造期键段立约（#285）：prefix/typ 是写死进每一个键的静态段，含
	// ':' 即与键分层分隔符不可区分（前缀 "a:b"+typ "c" 与前缀 "a"+typ
	// "b:c" 拼出同一 typePrefix，跨实体串键）——装配期即 panic（同本
	// 构造器既有 fail-fast 口径；同族先例 etcdx registry#110/distlock#138、
	// pkg/cache#188）。动态段（scope / ID 编码产物）在运行期校验，见
	// scope() 与 encodeIDSegment。
	if err := checkKeySegment("前缀", cfg.prefix); err != nil {
		panic("cached: NewRepository 装配期拒绝: " + err.Error())
	}
	if err := checkKeySegment("类型名", cfg.typ); err != nil {
		panic("cached: NewRepository 装配期拒绝: " + err.Error())
	}
	// 构造期单源化（#181）：ID 访问器与键段解码器各解析一次，写路径与
	// 缓存 miss 热路径不再逐次反射。
	cfg.idAcc = resolveIDAccessor[T, ID](cfg.idFn)
	cfg.decodeIDFn = resolveIDDecoder[T, ID](cfg.decode)
	// 构造期 fail-fast（#210）：开启 WithBloomFilter 时必须能解析实体 ID
	// （WithID 注入，或 GetID() 方法 / 名为 "ID" 的可导出字段缺省探测命中）。
	// 否则 Create 落库成功后 entityIDs 恒空，bloom 补录零信号空转——新 ID
	// 永不进 bloom，点读被 bloomGate 判「绝对不存在」走 null marker，已
	// 落库的行恒 ErrNotFound 且无自愈路径（bloom 只增不减，Create 是唯一
	// 补录点）。缺 ID 访问器的「TTL 兜底」降级只在未开 bloom 时成立。
	if cfg.bloom != nil && cfg.idAcc == nil {
		panic("cached: 开启 WithBloomFilter 时必须能解析实体 ID（注入 WithID，或实体提供 GetID() 方法 / 名为 ID 的可导出字段）——否则新实体的点读会被布隆前置门永久判为不存在（数据消失级事故），装配期即拒绝")
	}
	r := &CachedRepository[T, ID]{cfg: cfg}
	r.entity = newCache(r.source)
	if r.entity == nil {
		panic("cached: NewRepository 的 newCache 工厂返回了 nil 缓存——首个 GetOrLoad 将 nil panic，装配期即暴露")
	}
	return r
}

// defaultTypeName 反射取泛型实参的类型名字符串做键段。
func defaultTypeName[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().String()
}

// source 是注入后端缓存的 SourceFunc：键 → ID → 仓储回源，
// ErrNotFound 翻译为 (nil, nil) 触发 null marker（防穿透）。
func (r *CachedRepository[T, ID]) source(ctx context.Context, key string) (*T, error) {
	// 作用域段的位置随作用域而变 ⇒ 前缀要按**当前作用域**算。
	// 作用域由 ctx 解析（后端回源时透传同一个 ctx，见 pkg/cache 的
	// SourceFunc 契约）⇒ 这里解析出的作用域与建键时一致。
	scope, err := r.scope(ctx)
	if err != nil {
		return nil, err
	}
	rest, ok := strings.CutPrefix(key, r.idKeyPrefix(scope))
	if !ok {
		return nil, errors.Newf("cached: key %q 不属于类型 %q 的 ID 键空间", key, r.cfg.typ)
	}
	id, err := r.decodeID(rest)
	if err != nil {
		return nil, errors.Wrapf(err, "cached: key %q 的 ID 段非法", key)
	}
	// 布隆前置门（可选件）：判定"绝对不存在"直接走 null marker，仓储零调用
	if r.bloomGate(ctx, id) {
		return nil, nil
	}
	entity, err := r.cfg.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, crud.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return entity, nil
}

// afterRead 读路径作用域门（#179 收口）：作用域解析成功返回 (scope, true)；
// 失败上报 op="scope" 并降级——调用方直查仓储（仓储是权威源，直查不会
// 串数据），**不回落共享缓存键**（那正是跨租户串数据的根因）。
func (r *CachedRepository[T, ID]) afterRead(ctx context.Context) (string, bool) {
	scope, err := r.scope(ctx)
	if err != nil {
		r.report(ctx, "scope", nil, err)
		return "", false
	}
	return scope, true
}

// --- 读路径 ---

// GetByID 点读：缓存命中直达，未命中经后端 GetOrLoad 回源回填
// （null marker 防穿透、singleflight 防击穿由后端承担）。缓存读失败
// 降级直查仓储。
//
// 已知代价：GetOrLoad 的 error 无法区分"缓存读失败"与"回源仓储失败"
// （CacheAside 接口无错误分类），两者都按读降级处理并上报 op="read"；
// 仓储故障时每个并发等待者各降级查一次库，等于击穿到库——但仓储本来
// 就在报错，多这几击不改变故障性质。
func (r *CachedRepository[T, ID]) GetByID(ctx context.Context, id ID) (*T, error) {
	scope, ok := r.afterRead(ctx)
	if !ok {
		return r.cfg.repo.GetByID(ctx, id)
	}
	key, err := r.idKey(scope, id)
	if err != nil {
		// ID 键段含 ':'（#285）：无法构造无歧义键 ⇒ 降级直查仓储（仓储
		// 是权威源，直查不会串数据），经 onError 上报 op="encode"。与
		// 作用域解析失败同口径：绝不带着歧义键回缓存。
		r.report(ctx, "encode", nil, err)
		return r.cfg.repo.GetByID(ctx, id)
	}
	v, err := r.entity.GetOrLoad(ctx, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		// 降级直查同样过布隆前置门：缓存后端故障（正是 ID 扫描的高发
		// 窗口）不能变成防穿透旁路
		if r.bloomGate(ctx, id) {
			return nil, crud.ErrNotFound
		}
		return r.cfg.repo.GetByID(ctx, id)
	}
	if v == nil {
		return nil, crud.ErrNotFound
	}
	return v, nil
}

// getQueryHit 读查询缓存并区分「未命中」与「命中」：后端实现
// cache.HitGetter 时走 GetEx——hit=true/value=nil 即负标记命中，与
// miss 可辨；不具备该能力时退回 Get，此时 hit 只表示值非 nil，nil
// 一律按 miss 回源（负结果不可缓存，历史行为）。
func getQueryHit[T any](ctx context.Context, c cache.Cache[T], key string) (v *T, hit bool, err error) {
	if hg, ok := c.(cache.HitGetter[T]); ok {
		return hg.GetEx(ctx, key)
	}
	v, err = c.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	return v, v != nil, nil
}

// canCacheNegative 负结果缓存的前提：后端能区分「nil 真值/负标记命中」
// 与「miss」（HitGetter）。无该能力时写 nil 与 miss 不可分，写标记不
// 改变读语义，只会白付一次写。
func canCacheNegative[T any](c cache.Cache[T]) bool {
	_, ok := c.(cache.HitGetter[T])
	return ok
}

// GetOne 条件单读：开启查询缓存时走「版本号 + 指纹」键；后端具备
// HitGetter 能力时负结果写负标记防穿透（#253），否则负结果每次回源；
// ForUpdate 读与指纹失败降级直查。
func (r *CachedRepository[T, ID]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	if r.cfg.qc == nil {
		return r.cfg.repo.GetOne(ctx, q)
	}
	scope, ok := r.afterRead(ctx)
	if !ok {
		return r.cfg.repo.GetOne(ctx, q)
	}
	key, ok := r.queryCacheKey(ctx, scope, q, "one")
	if !ok {
		return r.cfg.repo.GetOne(ctx, q)
	}
	v, hit, err := getQueryHit(ctx, r.cfg.qc.entity, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		return r.cfg.repo.GetOne(ctx, q)
	}
	if hit {
		if v == nil {
			// 负标记命中：GetOne 的 nil 结果没有「nil 真值」语义，
			// 无行即 ErrNotFound（与点读 null marker 同口径）。
			return nil, crud.ErrNotFound
		}
		return v, nil
	}
	entity, err := r.cfg.repo.GetOne(ctx, q)
	if err != nil {
		// 负结果写标记（#253）：仅当后端具备 HitGetter 时写 nil——
		// 无该能力时 nil 与 miss 不可分，写标记不改变读语义（#167）；
		// 条目 TTL 兜底，经本仓储的写操作 bump 版本即失效。
		if errors.Is(err, crud.ErrNotFound) && canCacheNegative(r.cfg.qc.entity) {
			if serr := r.cfg.qc.entity.SetWithTTL(ctx, key, nil, r.cfg.qc.ttl); serr != nil {
				r.report(ctx, "backfill", []string{key}, serr)
			}
		}
		return nil, err
	}
	if entity == nil {
		return nil, nil // 契约外形态 (nil, nil)：原样透传，不写负标记
	}
	if err := r.cfg.qc.entity.SetWithTTL(ctx, key, entity, r.cfg.qc.ttl); err != nil {
		r.report(ctx, "backfill", []string{key}, err)
	}
	return entity, nil
}

// List 条件列表：开启查询缓存时走「版本号 + 指纹」键缓存整页结果，
// 任意写操作 bump 版本整体失效；后端具备 HitGetter 能力时空页负结果
// 也写标记防穿透（#253），否则空页每次回源。
func (r *CachedRepository[T, ID]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	if r.cfg.qc == nil {
		return r.cfg.repo.List(ctx, q)
	}
	scope, ok := r.afterRead(ctx)
	if !ok {
		return r.cfg.repo.List(ctx, q)
	}
	key, ok := r.queryCacheKey(ctx, scope, q, "list")
	if !ok {
		return r.cfg.repo.List(ctx, q)
	}
	page, hit, err := getQueryHit(ctx, r.cfg.qc.page, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		return r.cfg.repo.List(ctx, q)
	}
	if hit && page != nil {
		return page.Items, page.Total, nil
	}
	items, total, err := r.cfg.repo.List(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	if len(items) == 0 && total == 0 && !canCacheNegative(r.cfg.qc.page) {
		// 后端无法区分 miss 与空页真值：空页以正形态落盘会与「有行」不可分
		// （#167 历史口径），保持每次回源。
		return items, total, nil
	}
	if err := r.cfg.qc.page.SetWithTTL(ctx, key, &QueryPage[T]{Items: items, Total: total}, r.cfg.qc.ttl); err != nil {
		r.report(ctx, "backfill", []string{key}, err)
	}
	return items, total, nil
}

// GetByCondition 原始条件读永远直通：raw where 无法做稳定指纹。
func (r *CachedRepository[T, ID]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	return r.cfg.repo.GetByCondition(ctx, query, args...)
}

// Exists 直通（廉价查询，缓存无收益）。
func (r *CachedRepository[T, ID]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return r.cfg.repo.Exists(ctx, q)
}

// Count 直通（廉价查询，缓存无收益）。
func (r *CachedRepository[T, ID]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return r.cfg.repo.Count(ctx, q)
}

// queryCacheKey 计算查询缓存键：ForUpdate 永不缓存；指纹失败降级直通；
// 版本键缺失/过期时签发新令牌（旧令牌下的条目自然作废），版本读失败
// 降级直通——**空版本键绝不参与命中**（#250）：空版本键上的历史条目
// 不在任何 bump 的失效范围内，命中即写后读旧值。
func (r *CachedRepository[T, ID]) queryCacheKey(ctx context.Context, scope string, q *crud.Query, kind string) (string, bool) {
	var spec crud.QuerySpec
	if q != nil {
		spec = q.Spec()
	}
	if spec.ForUpdate {
		return "", false
	}
	fp, err := r.cfg.qc.fingerprint(spec)
	if err != nil {
		r.report(ctx, "fingerprint", nil, err)
		return "", false
	}
	if fp == "" {
		// 空串是契约允许的降级形态而非 error：必须以可识别哨兵上报，
		// 否则 onError 因 err==nil 静默，降级在观测面上完全不可见（#251）。
		r.report(ctx, "fingerprint", nil, ErrFingerprintDegraded)
		return "", false
	}
	verKey := r.verKey(scope)
	ver, err := r.cfg.qc.versions.Get(ctx, verKey)
	if err != nil {
		// 版本读失败时无法确认当前令牌：用空版本建键会回落到历史条目
		// （空版本键不受任何 bump 约束），必须降级直通（#250）。
		r.report(ctx, "read", []string{verKey}, err)
		return "", false
	}
	current := ""
	if ver != nil {
		current = *ver
	}
	if current == "" {
		// 版本键缺失/过期/被容量淘汰：签发新令牌替代空版本（#250）。
		// 旧令牌下的条目自然脱离当前键空间（受各自 TTL 回收），版本
		// 过期只导致一次重建，不致脏读——versions TTL 无需强制 ≥ 条目
		// TTL。先 Set 后回源：若与写路径 bump 竞争，覆盖发生后本次回源
		// 必然读到提交后的数据，不会把写前旧值写进当前令牌。
		token := newVersionToken()
		if err := r.cfg.qc.versions.Set(ctx, verKey, &token); err != nil {
			r.report(ctx, "read", []string{verKey}, err)
			return "", false
		}
		current = token
	}
	return r.queryKey(scope, kind, current, fp), true
}
