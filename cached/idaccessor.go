package cached

import (
	"reflect"
)

// 本文件是实体 ID 访问器职责（#179 拆分）：显式注入与缺省反射探测的
// 解析（构造期单源化，#181）与热路径取值。

// entityIDs 取实体 ID（WithID 或构造期解析好的缺省探测访问器）；取不到时
// 返回空——写路径仍会 bump 查询版本，点读一致性退化为 TTL 兜底。
func (r *CachedRepository[T, ID]) entityIDs(entity *T) []ID {
	if id, ok := r.entityID(entity); ok {
		return []ID{id}
	}
	return nil
}

func (r *CachedRepository[T, ID]) entityID(entity *T) (ID, bool) {
	if r.cfg.idAcc != nil {
		return r.cfg.idAcc(entity)
	}
	var zero ID
	return zero, false
}

// resolveIDAccessor 构造期解析 ID 访问器（#181）：显式 WithID 优先；
// 缺省探测（defaultIDAccessor 的反射查找 + 闭包构建）在构造期做一次，
// 写路径与事务记账路径不再逐次反射分配。两者都不可用时返回 nil
// （entityID 退化为 (zero, false)，写路径只 bump 查询版本）。
func resolveIDAccessor[T any, ID comparable](idFn func(*T) ID) func(*T) (ID, bool) {
	if idFn != nil {
		return func(e *T) (ID, bool) { return idFn(e), true }
	}
	if fn, ok := defaultIDAccessor[T, ID](); ok {
		return func(e *T) (ID, bool) { return fn(e), true }
	}
	return nil
}

// defaultIDAccessor 缺省 ID 探测：GetID() 方法优先（在指针类型上查——
// 指针接收者方法只存在于指针方法集，gormx.BaseModel 的 GetID 即是；
// 返回类型须与 ID 精确一致），其次名为 "ID" 的可导出字段（类型须与
// ID 一致）。方法/字段下标在解析期预计算（#181），返回的闭包热路径
// 不再做 MethodByName/FieldByName 名字查找。
func defaultIDAccessor[T any, ID comparable]() (func(*T) ID, bool) {
	var zero ID
	idType := reflect.TypeOf(zero)
	ptrT := reflect.TypeOf((*T)(nil))
	if m, ok := ptrT.MethodByName("GetID"); ok &&
		m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0) == idType {
		mi := methodIndexOf(ptrT, "GetID")
		return func(e *T) ID {
			// e 是 *T，指针值的方法集含值/指针两种接收者方法
			return reflect.ValueOf(e).Method(mi).Call(nil)[0].Interface().(ID)
		}, true
	}
	structT := ptrT.Elem()
	if structT.Kind() != reflect.Struct {
		return nil, false
	}
	if f, ok := structT.FieldByName("ID"); ok && f.IsExported() && f.Type == idType {
		fi := f.Index // 可能是嵌入提升字段的路径（FieldByIndex 语义与 FieldByName 一致）
		return func(e *T) ID {
			return reflect.ValueOf(e).Elem().FieldByIndex(fi).Interface().(ID)
		}, true
	}
	return nil, false
}

// methodIndexOf 方法名 → 方法集下标（仅探测期调用，热路径不出现）。
func methodIndexOf(t reflect.Type, name string) int {
	for i := 0; i < t.NumMethod(); i++ {
		if t.Method(i).Name == name {
			return i
		}
	}
	return -1
}
