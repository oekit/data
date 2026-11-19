package cached

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/gormx/crud"
	"github.com/oekit/pkg/cache"
)

// 本文件基准对照（#181）：缺省 ID 访问器/键段解码器在构造期单源化前后
// 的热路径成本。访问器旧路径 = 每次写操作 MethodByName/FieldByName 反射
// 查找 + 新分配闭包；新路径 = NewRepository 构造期解析一次，热路径仅
// 闭包调用。解码器旧路径 = 每次缓存 miss 做 TypeOf/Kind 解析 + reflect
// 装箱；新路径 = 精确内建类型 strconv + any 断言（零反射）。

// benchModel 带 GetID 方法的实体（缺省探测走方法路径，gormx.BaseModel 同款形状）。
type benchModel struct {
	ID   int64
	Name string
}

func (m benchModel) GetID() int64 { return m.ID }

// nopAside 空转后端：基准只测访问器/解码路径，不触达缓存与仓储。
type nopAside struct{}

func (nopAside) Get(context.Context, string) (*benchModel, error) { return nil, nil }
func (nopAside) Set(context.Context, string, *benchModel) error   { return nil }
func (nopAside) SetWithTTL(context.Context, string, *benchModel, time.Duration) error {
	return nil
}
func (nopAside) Del(context.Context, string) error            { return nil }
func (nopAside) DelBatch(context.Context, ...string) error    { return nil }
func (nopAside) Exists(context.Context, string) (bool, error) { return false, nil }
func (nopAside) GetOrLoad(context.Context, string) (*benchModel, error) {
	return nil, nil
}
func (nopAside) Refresh(context.Context, string) (*benchModel, error) { return nil, nil }

// benchStubRepo 基准用最小仓储桩：被测路径（entityID/decodeID/键构造）
// 不触达仓储，仅满足 NewRepository 的非 nil 装配契约——此前传 nil，
// 基准一跑即触发装配期 panic（#285 补基准时顺带修正，基准自此可运行）。
type benchStubRepo struct{}

func (benchStubRepo) GetByID(context.Context, int64) (*benchModel, error) {
	return nil, nil
}
func (benchStubRepo) GetOne(context.Context, *crud.Query) (*benchModel, error) {
	return nil, nil
}
func (benchStubRepo) GetByCondition(context.Context, any, ...any) (*benchModel, error) {
	return nil, nil
}
func (benchStubRepo) List(context.Context, *crud.Query) ([]benchModel, int64, error) {
	return nil, 0, nil
}
func (benchStubRepo) Exists(context.Context, *crud.Query) (bool, error) { return false, nil }
func (benchStubRepo) Count(context.Context, *crud.Query) (int64, error) { return 0, nil }
func (benchStubRepo) Create(context.Context, *benchModel) error         { return nil }
func (benchStubRepo) BatchCreate(context.Context, []*benchModel, int) error {
	return nil
}
func (benchStubRepo) Update(context.Context, *benchModel, ...crud.UpdateOption) (int64, error) {
	return 0, nil
}
func (benchStubRepo) UpdateFields(context.Context, int64, map[string]any) (int64, error) {
	return 0, nil
}
func (benchStubRepo) Delete(context.Context, int64) (int64, error) { return 0, nil }
func (benchStubRepo) BatchDelete(context.Context, []int64) (int64, error) {
	return 0, nil
}

// newBenchRepo 仅解析访问器/解码器：仓储与缓存后端用桩/空实现，
// 被测路径（entityID/decodeID）不触达它们。
func newBenchRepo() *CachedRepository[benchModel, int64] {
	return NewRepository[benchModel, int64](benchStubRepo{},
		func(src cache.SourceFunc[benchModel]) cache.CacheAside[benchModel] {
			return nopAside{}
		})
}

// benchNamedStubRepo 具名整型 ID（benchNamedID）的装配桩：ID 出现在方法
// 签名里的四个方法换成 benchNamedID 形参，其余沿用 benchStubRepo。
type benchNamedStubRepo struct {
	benchStubRepo
}

func (benchNamedStubRepo) GetByID(context.Context, benchNamedID) (*benchModel, error) {
	return nil, nil
}
func (benchNamedStubRepo) UpdateFields(context.Context, benchNamedID, map[string]any) (int64, error) {
	return 0, nil
}
func (benchNamedStubRepo) Delete(context.Context, benchNamedID) (int64, error) {
	return 0, nil
}
func (benchNamedStubRepo) BatchDelete(context.Context, []benchNamedID) (int64, error) {
	return 0, nil
}

// BenchmarkDefaultIDAccessorPerCall 旧热路径基线：每次写操作重新反射构建
// 缺省访问器（含 MethodByName 查找 + 闭包分配 + reflect.Call）。
func BenchmarkDefaultIDAccessorPerCall(b *testing.B) {
	e := &benchModel{ID: 42}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fn, ok := defaultIDAccessor[benchModel, int64]()
		if !ok {
			b.Fatal("缺省访问器未解析到")
		}
		if id := fn(e); id != 42 {
			b.Fatalf("id = %d, want 42", id)
		}
	}
}

// BenchmarkEntityIDResolved 现状热路径：构造期解析好的访问器逐实体调用。
func BenchmarkEntityIDResolved(b *testing.B) {
	r := newBenchRepo()
	e := &benchModel{ID: 42}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		id, ok := r.entityID(e)
		if !ok || id != 42 {
			b.Fatalf("id = %d ok=%v, want 42 true", id, ok)
		}
	}
}

// BenchmarkDecodeIDDefault 缺省解码快路径（int64：strconv + any 断言）。
func BenchmarkDecodeIDDefault(b *testing.B) {
	r := newBenchRepo()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		id, err := r.decodeID("42")
		if err != nil || id != 42 {
			b.Fatalf("id = %d err = %v, want 42 nil", id, err)
		}
	}
}

// BenchmarkDecodeIDNamedInt 具名整型解码（reflect 装箱路径，行为与历史一致）。
type benchNamedID int64

func BenchmarkDecodeIDNamedInt(b *testing.B) {
	r := NewRepository[benchModel, benchNamedID](benchNamedStubRepo{},
		func(src cache.SourceFunc[benchModel]) cache.CacheAside[benchModel] {
			return nopAside{}
		})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		id, err := r.decodeID("42")
		if err != nil || id != 42 {
			b.Fatalf("id = %d err = %v, want 42 nil", id, err)
		}
	}
}

// --- #285 键段边界校验的逐键成本 ---

// BenchmarkEncodeIDRaw 裸编码基线（#285 对照）：encodeID 不含段校验。
func BenchmarkEncodeIDRaw(b *testing.B) {
	r := newBenchRepo()
	id := int64(1234567)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if seg := r.encodeID(id); seg != "1234567" {
			b.Fatalf("seg = %q, want 1234567", seg)
		}
	}
}

// BenchmarkEncodeIDSegmentGuarded 逐键段校验后的编码成本（#285）：
// 与 BenchmarkEncodeIDRaw 的差值即一次 strings.Contains 的开销
// （短键段纳秒级，相对缓存 I/O 可忽略——逐键校验不构成热点负担）。
func BenchmarkEncodeIDSegmentGuarded(b *testing.B) {
	r := newBenchRepo()
	id := int64(1234567)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		seg, err := r.encodeIDSegment(id)
		if err != nil || seg != "1234567" {
			b.Fatalf("seg = %q err = %v, want 1234567 nil", seg, err)
		}
	}
}
