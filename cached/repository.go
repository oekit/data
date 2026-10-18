package cached

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
)

// CacheFactory 用装饰器注入的回源函数构造缓存后端。
//
// 装饰器把「键 → ID 解析、ErrNotFound → (nil, nil)」的数据源交给工厂，
// 工厂决定后端形态（LocalCache/RedisCache、是否包 SingleFlightCache）
// 与 TTL——TTL 是删缓存失败时的唯一安全网，务必配置有限值。
type CacheFactory[T any] func(source cache.SourceFunc[T]) cache.CacheAside[T]

// OnErrorFunc 缓存侧异常上报钩子：读降级（op="read"）、回填失败
// （op="backfill"）、指纹失败（op="fingerprint"）、写后失效失败
// （op="invalidate"）。所有场景都是"降级继续 + TTL 兜底"，钩子只管
// 观测（记日志/打点），不能也不需要参与决策。
type OnErrorFunc func(ctx context.Context, op string, keys []string, err error)

// config 是装饰器配置。
type config[T any, ID comparable] struct {
	repo    crud.Repository[T, ID]
	prefix  string
	typ     string
	idFn    func(*T) ID
	encode  func(ID) string
	decode  func(string) (ID, error)
	qc      *QueryCache[T]
	bloom   BloomChecker
	onError OnErrorFunc
}

// CachedOption 配置 CachedRepository（命名避开裸 Option——多包
// co-import 冲突，仓内硬规则；本仓 data/cache 壳已存在 cache.Option）。
type CachedOption[T any, ID comparable] func(*config[T, ID])

// WithID 注入实体 ID 访问器，写路径据此删实体键。
// 缺省时按顺序探测：GetID() 方法（含指针接收者，gormx.BaseModel 系列
// 天然满足；返回类型须与 ID 精确一致）→ 名为 "ID" 的可导出字段（类型
// 须与 ID 一致）；两者都失败时写路径只 bump 查询版本、无法删实体键
// （点读一致性退化为 TTL 兜底）。
func WithID[T any, ID comparable](fn func(*T) ID) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.idFn = fn }
}

// WithIDCodec 注入缓存键中 ID 段的编解码（encode：ID → 键段字符串；
// decode：键段字符串 → ID）。
//
// 缺省编解码：string 直通；全部整型走十进制（与 v0.13 及之前 int64 时代
// 的键格式完全兼容）。其余 comparable 类型（复合键、自定义结构等）的
// 键往返不可逆，必须显式注入——否则 source 解析键时报错（fail-fast，
// 不会静默错回源）。
func WithIDCodec[T any, ID comparable](encode func(ID) string, decode func(string) (ID, error)) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.encode, c.decode = encode, decode }
}

// WithType 覆盖键中的类型名段（默认反射类型名字符串，如 "main.User"）。
// 同一实体跨服务共用缓存前缀时建议显式指定短名。
func WithType[T any, ID comparable](name string) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.typ = name }
}

// WithPrefix 覆盖键前缀（默认 "cached"）。多应用共享一个 Redis 时用
// 应用名做前缀隔离。
func WithPrefix[T any, ID comparable](prefix string) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.prefix = prefix }
}

// WithQueryCache 开启查询缓存（GetOne/List）。传 nil 等价不开启。
func WithQueryCache[T any, ID comparable](qc *QueryCache[T]) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.qc = qc }
}

// WithOnError 注入缓存侧异常上报钩子（读降级/回填失败/失效失败）。
func WithOnError[T any, ID comparable](fn OnErrorFunc) CachedOption[T, ID] {
	return func(c *config[T, ID]) { c.onError = fn }
}

// CachedRepository 是带缓存编排的仓储装饰器，实现 crud.Repository[T, ID]
// 与 crud.TxRepository[T, ID]。可直接作为 crud.NewBaseService 的仓储入参
// ——事务路径经类型断言自动启用，失效在提交后执行。
// ID 为实体主键类型（int64 自增 / string 文本主键等，见 crud.Repository）。
type CachedRepository[T any, ID comparable] struct {
	cfg    config[T, ID]
	entity cache.CacheAside[T]
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
func NewRepository[T any, ID comparable](repo crud.Repository[T, ID], newCache CacheFactory[T], opts ...CachedOption[T, ID]) *CachedRepository[T, ID] {
	cfg := config[T, ID]{
		repo:   repo,
		prefix: "cached",
		typ:    defaultTypeName[T](),
	}
	for _, o := range opts {
		o(&cfg)
	}
	r := &CachedRepository[T, ID]{cfg: cfg}
	r.entity = newCache(r.source)
	return r
}

// defaultTypeName 反射取泛型实参的类型名字符串做键段。
func defaultTypeName[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().String()
}

// --- 键空间与 ID 编解码 ---

func (r *CachedRepository[T, ID]) idKeyPrefix() string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":id:"
}

// encodeID ID → 键段字符串。缺省 fmt %v（int64 十进制、string 直通，
// 与 int64 时代键格式兼容）。
func (r *CachedRepository[T, ID]) encodeID(id ID) string {
	if r.cfg.encode != nil {
		return r.cfg.encode(id)
	}
	return fmt.Sprintf("%v", id)
}

// decodeID 键段字符串 → ID。缺省支持 string 直通与全部整型（十进制，
// 按 reflect.Bits 限定宽度防溢出）；其余类型 fail-fast 报错并指路
// WithIDCodec——绝不猜测性回源（错解码 = 查错行，比报错严重得多）。
func (r *CachedRepository[T, ID]) decodeID(s string) (ID, error) {
	if r.cfg.decode != nil {
		return r.cfg.decode(s)
	}
	var zero ID
	t := reflect.TypeOf(zero)
	if t == nil {
		return zero, fmt.Errorf("cached: ID 为接口类型，缺省解码不可用，请用 WithIDCodec 注入编解码")
	}
	switch t.Kind() {
	case reflect.String:
		return any(s).(ID), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, t.Bits())
		if err != nil {
			return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
		}
		out := reflect.New(t).Elem()
		out.SetInt(v)
		return out.Interface().(ID), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, t.Bits())
		if err != nil {
			return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
		}
		out := reflect.New(t).Elem()
		out.SetUint(v)
		return out.Interface().(ID), nil
	default:
		return zero, fmt.Errorf("cached: ID 类型 %s 不支持缺省键解码，请用 WithIDCodec 注入编解码", t)
	}
}

func (r *CachedRepository[T, ID]) idKey(id ID) string {
	return r.idKeyPrefix() + r.encodeID(id)
}

func (r *CachedRepository[T, ID]) verKey() string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":ver"
}

func (r *CachedRepository[T, ID]) queryKey(kind, ver, fp string) string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":q:" + kind + ":" + ver + ":" + fp
}

// --- 回源函数（注入缓存后端） ---

// source 是注入后端缓存的 SourceFunc：键 → ID → 仓储回源，
// ErrNotFound 翻译为 (nil, nil) 触发 null marker（防穿透）。
func (r *CachedRepository[T, ID]) source(ctx context.Context, key string) (*T, error) {
	rest, ok := strings.CutPrefix(key, r.idKeyPrefix())
	if !ok {
		return nil, fmt.Errorf("cached: key %q 不属于类型 %q 的 ID 键空间", key, r.cfg.typ)
	}
	id, err := r.decodeID(rest)
	if err != nil {
		return nil, fmt.Errorf("cached: key %q 的 ID 段非法: %w", key, err)
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
	key := r.idKey(id)
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

// GetOne 条件单读：开启查询缓存时走「版本号 + 指纹」键，负结果不缓存，
// ForUpdate 读与指纹失败降级直查。
func (r *CachedRepository[T, ID]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	if r.cfg.qc == nil {
		return r.cfg.repo.GetOne(ctx, q)
	}
	key, ok := r.queryCacheKey(ctx, q, "one")
	if !ok {
		return r.cfg.repo.GetOne(ctx, q)
	}
	v, err := r.cfg.qc.entity.Get(ctx, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		return r.cfg.repo.GetOne(ctx, q)
	}
	if v != nil {
		return v, nil
	}
	entity, err := r.cfg.repo.GetOne(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := r.cfg.qc.entity.SetWithTTL(ctx, key, entity, r.cfg.qc.ttl); err != nil {
		r.report(ctx, "backfill", []string{key}, err)
	}
	return entity, nil
}

// List 条件列表：开启查询缓存时走「版本号 + 指纹」键缓存整页结果，
// 任意写操作 bump 版本整体失效。
func (r *CachedRepository[T, ID]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	if r.cfg.qc == nil {
		return r.cfg.repo.List(ctx, q)
	}
	key, ok := r.queryCacheKey(ctx, q, "list")
	if !ok {
		return r.cfg.repo.List(ctx, q)
	}
	page, err := r.cfg.qc.page.Get(ctx, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		return r.cfg.repo.List(ctx, q)
	}
	if page != nil {
		return page.Items, page.Total, nil
	}
	items, total, err := r.cfg.repo.List(ctx, q)
	if err != nil {
		return nil, 0, err
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
// 版本 miss/读失败以空版本兜底（旧版本键查不到即重建；注意若版本后端
// 持续故障而条目后端正常，空版本键可能命中上一轮降级窗口的旧条目——
// 仍受条目 TTL 约束）。
func (r *CachedRepository[T, ID]) queryCacheKey(ctx context.Context, q *crud.Query, kind string) (string, bool) {
	var spec crud.QuerySpec
	if q != nil {
		spec = q.Spec()
	}
	if spec.ForUpdate {
		return "", false
	}
	fp, err := r.cfg.qc.fingerprint(spec)
	if err != nil || fp == "" {
		r.report(ctx, "fingerprint", nil, err)
		return "", false
	}
	ver, err := r.cfg.qc.versions.Get(ctx, r.verKey())
	if err != nil {
		r.report(ctx, "read", []string{r.verKey()}, err)
	}
	current := ""
	if ver != nil {
		current = *ver
	}
	return r.queryKey(kind, current, fp), true
}

// --- 写路径（先写库，成功后失效） ---

// Create 写库后删实体键（预置主键的 Create 之前可能已有 null marker，
// 必须清）并 bump 查询版本（新行进列表）。
func (r *CachedRepository[T, ID]) Create(ctx context.Context, entity *T) error {
	if err := r.cfg.repo.Create(ctx, entity); err != nil {
		return err
	}
	ids := r.entityIDs(entity)
	r.invalidate(ctx, ids)
	r.bloomAdd(ctx, ids)
	return nil
}

// BatchCreate 写库后逐实体删键 + 一次版本 bump。
func (r *CachedRepository[T, ID]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := r.cfg.repo.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []ID
	for _, e := range entities {
		ids = append(ids, r.entityIDs(e)...)
	}
	r.invalidate(ctx, ids)
	r.bloomAdd(ctx, ids)
	return nil
}

// Update 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T, ID]) Update(ctx context.Context, entity *T) error {
	if err := r.cfg.repo.Update(ctx, entity); err != nil {
		return err
	}
	r.invalidate(ctx, r.entityIDs(entity))
	return nil
}

// UpdateFields 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T, ID]) UpdateFields(ctx context.Context, id ID, fields map[string]any) error {
	if err := r.cfg.repo.UpdateFields(ctx, id, fields); err != nil {
		return err
	}
	r.invalidate(ctx, []ID{id})
	return nil
}

// Delete 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T, ID]) Delete(ctx context.Context, id ID) error {
	if err := r.cfg.repo.Delete(ctx, id); err != nil {
		return err
	}
	r.invalidate(ctx, []ID{id})
	return nil
}

// BatchDelete 写库后批量删键 + 一次版本 bump。
func (r *CachedRepository[T, ID]) BatchDelete(ctx context.Context, ids []ID) error {
	if err := r.cfg.repo.BatchDelete(ctx, ids); err != nil {
		return err
	}
	r.invalidate(ctx, ids)
	return nil
}

// entityIDs 取实体 ID（WithID 或缺省探测）；取不到时返回空——
// 写路径仍会 bump 查询版本，点读一致性退化为 TTL 兜底。
func (r *CachedRepository[T, ID]) entityIDs(entity *T) []ID {
	if id, ok := r.entityID(entity); ok {
		return []ID{id}
	}
	return nil
}

func (r *CachedRepository[T, ID]) entityID(entity *T) (ID, bool) {
	if r.cfg.idFn != nil {
		return r.cfg.idFn(entity), true
	}
	if fn, ok := defaultIDAccessor[T, ID](); ok {
		return fn(entity), true
	}
	var zero ID
	return zero, false
}

// defaultIDAccessor 缺省 ID 探测：GetID() 方法优先（在指针类型上查——
// 指针接收者方法只存在于指针方法集，gormx.BaseModel 的 GetID 即是；
// 返回类型须与 ID 精确一致），其次名为 "ID" 的可导出字段（类型须与
// ID 一致）。
func defaultIDAccessor[T any, ID comparable]() (func(*T) ID, bool) {
	var zero ID
	idType := reflect.TypeOf(zero)
	ptrT := reflect.TypeOf((*T)(nil))
	if m, ok := ptrT.MethodByName("GetID"); ok &&
		m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0) == idType {
		return func(e *T) ID {
			// e 是 *T，指针值的方法集含值/指针两种接收者方法
			return reflect.ValueOf(e).MethodByName("GetID").Call(nil)[0].Interface().(ID)
		}, true
	}
	structT := ptrT.Elem()
	if structT.Kind() != reflect.Struct {
		return nil, false
	}
	if f, ok := structT.FieldByName("ID"); ok && f.IsExported() && f.Type == idType {
		return func(e *T) ID {
			return reflect.ValueOf(e).Elem().FieldByName("ID").Interface().(ID)
		}, true
	}
	return nil, false
}

// invalidate 写库成功后的失效：删实体键（best-effort）+ bump 查询版本。
// 失败不回滚写操作，经 onError 上报，TTL 兜底。
func (r *CachedRepository[T, ID]) invalidate(ctx context.Context, ids []ID) {
	if len(ids) > 0 {
		keys := make([]string, 0, len(ids))
		prefix := r.idKeyPrefix()
		for _, id := range ids {
			keys = append(keys, prefix+r.encodeID(id))
		}
		if err := r.entity.DelBatch(ctx, keys...); err != nil {
			r.report(ctx, "invalidate", keys, err)
		}
	}
	if r.cfg.qc != nil {
		ver := newVersionToken()
		if err := r.cfg.qc.versions.Set(ctx, r.verKey(), &ver); err != nil {
			r.report(ctx, "invalidate", []string{r.verKey()}, err)
		}
	}
}

// report 上报缓存侧异常；无钩子或无错误时静默（miss 不是错误）。
func (r *CachedRepository[T, ID]) report(ctx context.Context, op string, keys []string, err error) {
	if err == nil || r.cfg.onError == nil {
		return
	}
	r.cfg.onError(ctx, op, keys, err)
}

// newVersionToken 生成版本令牌：随机 16 字节 hex。不用自增——
// 并发写下读-改-写会丢 bump，随机令牌无条件覆盖即天然"后写者生效"。
func newVersionToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败几乎只可能是系统级故障，退化为纳秒时间戳
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// --- 事务路径 ---

// RunInTx 事务包装：fn 收到的事务仓储把写操作记录进失效清单，
// 提交成功后统一失效（先删实体键后 bump 版本）；回滚不失效。
// 事务内的读操作直通事务仓储（保证读到事务内状态，缓存只会是旧的）。
func (r *CachedRepository[T, ID]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T, ID]) error) error {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T, ID])
	if !ok {
		return fmt.Errorf("cached: 底层仓储未实现 TxRepository，无法开启事务")
	}
	tw := &txCollector[T, ID]{outer: r}
	err := txRepo.RunInTx(ctx, func(txRepo crud.Repository[T, ID]) error {
		innerTx, ok := txRepo.(crud.TxRepository[T, ID])
		if !ok {
			return fmt.Errorf("cached: 事务内仓储未实现 TxRepository")
		}
		tw.inner = txRepo
		tw.innerTx = innerTx
		return fn(tw)
	})
	if err != nil {
		return err
	}
	// 提交后的失效用 WithoutCancel：调用方在提交瞬间取消 context
	// 不能吞掉失效（失效失败只剩 TTL 兜底，能救一次是一次）。
	r.applyCollected(context.WithoutCancel(ctx), tw)
	return nil
}

// GetByIDForUpdate 行锁读直通：锁读的语义就是"拿最权威的数据"，
// 过缓存既拿不到锁还会读到旧值。
func (r *CachedRepository[T, ID]) GetByIDForUpdate(ctx context.Context, id ID) (*T, error) {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T, ID])
	if !ok {
		return nil, fmt.Errorf("cached: 底层仓储未实现 TxRepository")
	}
	return txRepo.GetByIDForUpdate(ctx, id)
}

// txCollector 事务内的仓储视图：写操作只记账（收集失效清单），
// 读操作与行锁读直通事务仓储。
type txCollector[T any, ID comparable] struct {
	outer   *CachedRepository[T, ID]
	inner   crud.Repository[T, ID]
	innerTx crud.TxRepository[T, ID]

	mu      sync.Mutex
	ids     []ID
	created []ID
}

func (t *txCollector[T, ID]) note(ids []ID) {
	t.mu.Lock()
	t.ids = append(t.ids, ids...)
	t.mu.Unlock()
}

func (t *txCollector[T, ID]) noteCreated(ids []ID) {
	t.mu.Lock()
	t.created = append(t.created, ids...)
	t.mu.Unlock()
}

func (t *txCollector[T, ID]) GetByID(ctx context.Context, id ID) (*T, error) {
	return t.inner.GetByID(ctx, id)
}

func (t *txCollector[T, ID]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	return t.inner.GetOne(ctx, q)
}

func (t *txCollector[T, ID]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	return t.inner.GetByCondition(ctx, query, args...)
}

func (t *txCollector[T, ID]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	return t.inner.List(ctx, q)
}

func (t *txCollector[T, ID]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return t.inner.Exists(ctx, q)
}

func (t *txCollector[T, ID]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return t.inner.Count(ctx, q)
}

func (t *txCollector[T, ID]) Create(ctx context.Context, entity *T) error {
	if err := t.inner.Create(ctx, entity); err != nil {
		return err
	}
	ids := t.outer.entityIDs(entity)
	t.note(ids)
	t.noteCreated(ids)
	return nil
}

func (t *txCollector[T, ID]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := t.inner.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []ID
	for _, e := range entities {
		ids = append(ids, t.outer.entityIDs(e)...)
	}
	t.note(ids)
	t.noteCreated(ids)
	return nil
}

func (t *txCollector[T, ID]) Update(ctx context.Context, entity *T) error {
	if err := t.inner.Update(ctx, entity); err != nil {
		return err
	}
	t.note(t.outer.entityIDs(entity))
	return nil
}

func (t *txCollector[T, ID]) UpdateFields(ctx context.Context, id ID, fields map[string]any) error {
	if err := t.inner.UpdateFields(ctx, id, fields); err != nil {
		return err
	}
	t.note([]ID{id})
	return nil
}

func (t *txCollector[T, ID]) Delete(ctx context.Context, id ID) error {
	if err := t.inner.Delete(ctx, id); err != nil {
		return err
	}
	t.note([]ID{id})
	return nil
}

func (t *txCollector[T, ID]) BatchDelete(ctx context.Context, ids []ID) error {
	if err := t.inner.BatchDelete(ctx, ids); err != nil {
		return err
	}
	t.note(ids)
	return nil
}

func (t *txCollector[T, ID]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T, ID]) error) error {
	// 嵌套事务重新包装同一收集器：内层 fn 收到的仍是收集器视图，
	// 写操作继续记账，失效仍由最外层 RunInTx 在提交后统一执行。
	// 若直接透传 fn，底层仓储会给出裸事务仓储，写操作绕过收集——
	// 提交后不失效，点读读到 TTL 窗口内的旧值。
	return t.innerTx.RunInTx(ctx, func(repo crud.Repository[T, ID]) error {
		innerTx, ok := repo.(crud.TxRepository[T, ID])
		if !ok {
			return fmt.Errorf("cached: 事务内仓储未实现 TxRepository")
		}
		t.inner = repo
		t.innerTx = innerTx
		return fn(t)
	})
}

func (t *txCollector[T, ID]) GetByIDForUpdate(ctx context.Context, id ID) (*T, error) {
	return t.innerTx.GetByIDForUpdate(ctx, id)
}

// applyCollected 提交成功后的统一失效。
func (r *CachedRepository[T, ID]) applyCollected(ctx context.Context, t *txCollector[T, ID]) {
	t.mu.Lock()
	ids := t.ids
	created := t.created
	t.mu.Unlock()
	r.invalidate(ctx, ids)
	r.bloomAdd(ctx, created)
}
