package cached

import (
	"context"
	"testing"
	"time"

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

// newBenchRepo 仅解析访问器/解码器：仓储与缓存后端用空实现/nil，
// 被测路径（entityID/decodeID）不触达它们。
func newBenchRepo() *CachedRepository[benchModel, int64] {
	return NewRepository[benchModel, int64](nil,
		func(src cache.SourceFunc[benchModel]) cache.CacheAside[benchModel] {
			return nopAside{}
		})
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
	r := NewRepository[benchModel, benchNamedID](nil,
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
