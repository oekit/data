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
type config[T any] struct {
	repo    crud.Repository[T]
	prefix  string
	typ     string
	idFn    func(*T) int64
	qc      *QueryCache[T]
	onError OnErrorFunc
}

// CachedOption 配置 CachedRepository（命名避开裸 Option——多包
// co-import 冲突，仓内硬规则；本仓 data/cache 壳已存在 cache.Option）。
type CachedOption[T any] func(*config[T])

// WithID 注入实体 ID 访问器，写路径据此删实体键。
// 缺省时按顺序探测：GetID() int64 方法（含指针接收者，gormx.BaseModel
// 系列天然满足）→ 名为 "ID" 的可导出 int64 字段；两者都失败时写路径
// 只 bump 查询版本、无法删实体键（点读一致性退化为 TTL 兜底）。
func WithID[T any](fn func(*T) int64) CachedOption[T] {
	return func(c *config[T]) { c.idFn = fn }
}

// WithType 覆盖键中的类型名段（默认反射类型名字符串，如 "main.User"）。
// 同一实体跨服务共用缓存前缀时建议显式指定短名。
func WithType[T any](name string) CachedOption[T] {
	return func(c *config[T]) { c.typ = name }
}

// WithPrefix 覆盖键前缀（默认 "cached"）。多应用共享一个 Redis 时用
// 应用名做前缀隔离。
func WithPrefix[T any](prefix string) CachedOption[T] {
	return func(c *config[T]) { c.prefix = prefix }
}

// WithQueryCache 开启查询缓存（GetOne/List）。传 nil 等价不开启。
func WithQueryCache[T any](qc *QueryCache[T]) CachedOption[T] {
	return func(c *config[T]) { c.qc = qc }
}

// WithOnError 注入缓存侧异常上报钩子（读降级/回填失败/失效失败）。
func WithOnError[T any](fn OnErrorFunc) CachedOption[T] {
	return func(c *config[T]) { c.onError = fn }
}

// CachedRepository 是带缓存编排的仓储装饰器，实现 crud.Repository[T]
// 与 crud.TxRepository[T]。可直接作为 crud.NewBaseService 的仓储入参
// ——事务路径经类型断言自动启用，失效在提交后执行。
type CachedRepository[T any] struct {
	cfg    config[T]
	entity cache.CacheAside[T]
}

// 编译期接口契约：装饰器必须完整满足（含事务能力）仓储接口，
// 否则 NewBaseService 的类型断言会静默走无事务回退路径。
var _ = assertRepositoryContract[int64]

func assertRepositoryContract[T any]() {
	var _ crud.TxRepository[T] = (*CachedRepository[T])(nil)
}

// NewRepository 创建缓存编排仓储装饰器。
// newCache 必填：装饰器不代持后端构造，TTL/容量/序列化等后端参数
// 全部留在调用方手里，装饰器只注入回源函数。
func NewRepository[T any](repo crud.Repository[T], newCache CacheFactory[T], opts ...CachedOption[T]) *CachedRepository[T] {
	cfg := config[T]{
		repo:   repo,
		prefix: "cached",
		typ:    defaultTypeName[T](),
	}
	for _, o := range opts {
		o(&cfg)
	}
	r := &CachedRepository[T]{cfg: cfg}
	r.entity = newCache(r.source)
	return r
}

// defaultTypeName 反射取泛型实参的类型名字符串做键段。
func defaultTypeName[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().String()
}

// --- 键空间 ---

func (r *CachedRepository[T]) idKeyPrefix() string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":id:"
}

func (r *CachedRepository[T]) idKey(id int64) string {
	return r.idKeyPrefix() + strconv.FormatInt(id, 10)
}

func (r *CachedRepository[T]) verKey() string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":ver"
}

func (r *CachedRepository[T]) queryKey(kind, ver, fp string) string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":q:" + kind + ":" + ver + ":" + fp
}

// --- 回源函数（注入缓存后端） ---

// source 是注入后端缓存的 SourceFunc：键 → ID → 仓储回源，
// ErrNotFound 翻译为 (nil, nil) 触发 null marker（防穿透）。
func (r *CachedRepository[T]) source(ctx context.Context, key string) (*T, error) {
	rest, ok := strings.CutPrefix(key, r.idKeyPrefix())
	if !ok {
		return nil, fmt.Errorf("cached: key %q 不属于类型 %q 的 ID 键空间", key, r.cfg.typ)
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("cached: key %q 的 ID 段非法", key)
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
func (r *CachedRepository[T]) GetByID(ctx context.Context, id int64) (*T, error) {
	key := r.idKey(id)
	v, err := r.entity.GetOrLoad(ctx, key)
	if err != nil {
		r.report(ctx, "read", []string{key}, err)
		return r.cfg.repo.GetByID(ctx, id)
	}
	if v == nil {
		return nil, crud.ErrNotFound
	}
	return v, nil
}

// GetOne 条件单读：开启查询缓存时走「版本号 + 指纹」键，负结果不缓存，
// ForUpdate 读与指纹失败降级直查。
func (r *CachedRepository[T]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
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
func (r *CachedRepository[T]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
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
func (r *CachedRepository[T]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	return r.cfg.repo.GetByCondition(ctx, query, args...)
}

// Exists 直通（廉价查询，缓存无收益）。
func (r *CachedRepository[T]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return r.cfg.repo.Exists(ctx, q)
}

// Count 直通（廉价查询，缓存无收益）。
func (r *CachedRepository[T]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return r.cfg.repo.Count(ctx, q)
}

// queryCacheKey 计算查询缓存键：ForUpdate 永不缓存；指纹失败降级直通；
// 版本 miss/读失败以空版本兜底（旧版本键查不到即重建；注意若版本后端
// 持续故障而条目后端正常，空版本键可能命中上一轮降级窗口的旧条目——
// 仍受条目 TTL 约束）。
func (r *CachedRepository[T]) queryCacheKey(ctx context.Context, q *crud.Query, kind string) (string, bool) {
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
func (r *CachedRepository[T]) Create(ctx context.Context, entity *T) error {
	if err := r.cfg.repo.Create(ctx, entity); err != nil {
		return err
	}
	r.invalidate(ctx, r.entityIDs(entity))
	return nil
}

// BatchCreate 写库后逐实体删键 + 一次版本 bump。
func (r *CachedRepository[T]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := r.cfg.repo.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []int64
	for _, e := range entities {
		ids = append(ids, r.entityIDs(e)...)
	}
	r.invalidate(ctx, ids)
	return nil
}

// Update 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T]) Update(ctx context.Context, entity *T) error {
	if err := r.cfg.repo.Update(ctx, entity); err != nil {
		return err
	}
	r.invalidate(ctx, r.entityIDs(entity))
	return nil
}

// UpdateFields 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T]) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	if err := r.cfg.repo.UpdateFields(ctx, id, fields); err != nil {
		return err
	}
	r.invalidate(ctx, []int64{id})
	return nil
}

// Delete 写库后删实体键 + bump 查询版本。
func (r *CachedRepository[T]) Delete(ctx context.Context, id int64) error {
	if err := r.cfg.repo.Delete(ctx, id); err != nil {
		return err
	}
	r.invalidate(ctx, []int64{id})
	return nil
}

// BatchDelete 写库后批量删键 + 一次版本 bump。
func (r *CachedRepository[T]) BatchDelete(ctx context.Context, ids []int64) error {
	if err := r.cfg.repo.BatchDelete(ctx, ids); err != nil {
		return err
	}
	r.invalidate(ctx, ids)
	return nil
}

// entityIDs 取实体 ID（WithID 或缺省探测）；取不到时返回空——
// 写路径仍会 bump 查询版本，点读一致性退化为 TTL 兜底。
func (r *CachedRepository[T]) entityIDs(entity *T) []int64 {
	if id, ok := r.entityID(entity); ok {
		return []int64{id}
	}
	return nil
}

func (r *CachedRepository[T]) entityID(entity *T) (int64, bool) {
	if r.cfg.idFn != nil {
		return r.cfg.idFn(entity), true
	}
	if fn, ok := defaultIDAccessor[T](); ok {
		return fn(entity), true
	}
	return 0, false
}

// defaultIDAccessor 缺省 ID 探测：GetID() int64 方法优先（在指针类型
// 上查——指针接收者方法只存在于指针方法集，gormx.BaseModel 的 GetID
// 即是），其次名为 "ID" 的可导出 int64 字段。
func defaultIDAccessor[T any]() (func(*T) int64, bool) {
	ptrT := reflect.TypeOf((*T)(nil))
	if m, ok := ptrT.MethodByName("GetID"); ok &&
		m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.Int64 {
		return func(e *T) int64 {
			// e 是 *T，指针值的方法集含值/指针两种接收者方法
			return reflect.ValueOf(e).MethodByName("GetID").Call(nil)[0].Int()
		}, true
	}
	structT := ptrT.Elem()
	if structT.Kind() != reflect.Struct {
		return nil, false
	}
	if f, ok := structT.FieldByName("ID"); ok && f.IsExported() && f.Type.Kind() == reflect.Int64 {
		return func(e *T) int64 {
			return reflect.ValueOf(e).Elem().FieldByName("ID").Int()
		}, true
	}
	return nil, false
}

// invalidate 写库成功后的失效：删实体键（best-effort）+ bump 查询版本。
// 失败不回滚写操作，经 onError 上报，TTL 兜底。
func (r *CachedRepository[T]) invalidate(ctx context.Context, ids []int64) {
	if len(ids) > 0 {
		keys := make([]string, 0, len(ids))
		prefix := r.idKeyPrefix()
		for _, id := range ids {
			keys = append(keys, prefix+strconv.FormatInt(id, 10))
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
func (r *CachedRepository[T]) report(ctx context.Context, op string, keys []string, err error) {
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
func (r *CachedRepository[T]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T]) error) error {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T])
	if !ok {
		return fmt.Errorf("cached: 底层仓储未实现 TxRepository，无法开启事务")
	}
	tw := &txCollector[T]{outer: r}
	err := txRepo.RunInTx(ctx, func(txRepo crud.Repository[T]) error {
		innerTx, ok := txRepo.(crud.TxRepository[T])
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
func (r *CachedRepository[T]) GetByIDForUpdate(ctx context.Context, id int64) (*T, error) {
	txRepo, ok := r.cfg.repo.(crud.TxRepository[T])
	if !ok {
		return nil, fmt.Errorf("cached: 底层仓储未实现 TxRepository")
	}
	return txRepo.GetByIDForUpdate(ctx, id)
}

// txCollector 事务内的仓储视图：写操作只记账（收集失效清单），
// 读操作与行锁读直通事务仓储。
type txCollector[T any] struct {
	outer   *CachedRepository[T]
	inner   crud.Repository[T]
	innerTx crud.TxRepository[T]

	mu  sync.Mutex
	ids []int64
}

func (t *txCollector[T]) note(ids []int64) {
	t.mu.Lock()
	t.ids = append(t.ids, ids...)
	t.mu.Unlock()
}

func (t *txCollector[T]) GetByID(ctx context.Context, id int64) (*T, error) {
	return t.inner.GetByID(ctx, id)
}

func (t *txCollector[T]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	return t.inner.GetOne(ctx, q)
}

func (t *txCollector[T]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	return t.inner.GetByCondition(ctx, query, args...)
}

func (t *txCollector[T]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	return t.inner.List(ctx, q)
}

func (t *txCollector[T]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	return t.inner.Exists(ctx, q)
}

func (t *txCollector[T]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	return t.inner.Count(ctx, q)
}

func (t *txCollector[T]) Create(ctx context.Context, entity *T) error {
	if err := t.inner.Create(ctx, entity); err != nil {
		return err
	}
	t.note(t.outer.entityIDs(entity))
	return nil
}

func (t *txCollector[T]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if err := t.inner.BatchCreate(ctx, entities, batchSize); err != nil {
		return err
	}
	var ids []int64
	for _, e := range entities {
		ids = append(ids, t.outer.entityIDs(e)...)
	}
	t.note(ids)
	return nil
}

func (t *txCollector[T]) Update(ctx context.Context, entity *T) error {
	if err := t.inner.Update(ctx, entity); err != nil {
		return err
	}
	t.note(t.outer.entityIDs(entity))
	return nil
}

func (t *txCollector[T]) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	if err := t.inner.UpdateFields(ctx, id, fields); err != nil {
		return err
	}
	t.note([]int64{id})
	return nil
}

func (t *txCollector[T]) Delete(ctx context.Context, id int64) error {
	if err := t.inner.Delete(ctx, id); err != nil {
		return err
	}
	t.note([]int64{id})
	return nil
}

func (t *txCollector[T]) BatchDelete(ctx context.Context, ids []int64) error {
	if err := t.inner.BatchDelete(ctx, ids); err != nil {
		return err
	}
	t.note(ids)
	return nil
}

func (t *txCollector[T]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T]) error) error {
	// 嵌套事务重新包装同一收集器：内层 fn 收到的仍是收集器视图，
	// 写操作继续记账，失效仍由最外层 RunInTx 在提交后统一执行。
	// 若直接透传 fn，底层仓储会给出裸事务仓储，写操作绕过收集——
	// 提交后不失效，点读读到 TTL 窗口内的旧值。
	return t.innerTx.RunInTx(ctx, func(repo crud.Repository[T]) error {
		innerTx, ok := repo.(crud.TxRepository[T])
		if !ok {
			return fmt.Errorf("cached: 事务内仓储未实现 TxRepository")
		}
		t.inner = repo
		t.innerTx = innerTx
		return fn(t)
	})
}

func (t *txCollector[T]) GetByIDForUpdate(ctx context.Context, id int64) (*T, error) {
	return t.innerTx.GetByIDForUpdate(ctx, id)
}

// applyCollected 提交成功后的统一失效。
func (r *CachedRepository[T]) applyCollected(ctx context.Context, t *txCollector[T]) {
	t.mu.Lock()
	ids := t.ids
	t.mu.Unlock()
	r.invalidate(ctx, ids)
}
