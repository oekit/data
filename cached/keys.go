package cached

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// 本文件是键空间与 ID 编解码职责（#179 拆分）：作用域解析、键构造、
// ID 键段的编解码及其缺省解析器。

// scope 解析当前调用的作用域键。
//
// 未启用作用域隔离时返回空串（键形态与历史一致）。启用后：
//   - 解析出错 ⇒ 返回错误（**不**回落共享键）；
//   - 解析出空串 ⇒ 返回错误（空串等于没开隔离）。
func (r *CachedRepository[T, ID]) scope(ctx context.Context) (string, error) {
	if r.cfg.scopeKey == nil {
		return "", nil
	}
	s, err := r.cfg.scopeKey(ctx)
	if err != nil {
		return "", fmt.Errorf("cached: 解析作用域键失败: %w", err)
	}
	if s == "" {
		return "", errors.New("cached: 作用域键为空——作用域隔离已启用但未解析出作用域（拒绝回落共享键）")
	}
	return s, nil
}

// scopeSegment 作用域段（无作用域时为空串）。
func scopeSegment(scope string) string {
	if scope == "" {
		return ""
	}
	return "scope:" + scope + ":"
}

// typePrefix 类型段前缀（键的公共部分）。
func (r *CachedRepository[T, ID]) typePrefix() string {
	return r.cfg.prefix + ":" + r.cfg.typ + ":"
}

func (r *CachedRepository[T, ID]) idKeyPrefix(scope string) string {
	return r.typePrefix() + scopeSegment(scope) + "id:"
}

// encodeID ID → 键段字符串。缺省 fmt %v（int64 十进制、string 直通，
// 与 int64 时代键格式兼容）。
func (r *CachedRepository[T, ID]) encodeID(id ID) string {
	if r.cfg.encode != nil {
		return r.cfg.encode(id)
	}
	return fmt.Sprintf("%v", id)
}

// decodeID 键段字符串 → ID。解码器在构造期解析一次（见 resolveIDDecoder），
// 缺省支持 string 直通与全部整型（十进制，按类型宽度限定防溢出）；
// 其余类型 fail-fast 报错并指路 WithIDCodec——绝不猜测性回源
// （错解码 = 查错行，比报错严重得多）。
func (r *CachedRepository[T, ID]) decodeID(s string) (ID, error) {
	return r.cfg.decodeIDFn(s)
}

// resolveIDDecoder 构造期解析键段解码器（#181）：显式 WithIDCodec 优先；
// 缺省按 ID 类型一次性选定解码闭包——精确内建类型（string 与全部内建
// 整型）走 strconv + 直接断言的免反射快路径，具名整型退回 reflect 装箱
// （行为与历史一致，仅省去每次 TypeOf/Kind 解析），其余类型 fail-fast
// 报错并指路 WithIDCodec。
func resolveIDDecoder[T any, ID comparable](decode func(string) (ID, error)) func(string) (ID, error) {
	if decode != nil {
		return decode
	}
	var zero ID
	t := reflect.TypeOf(zero)
	if t == nil {
		return func(string) (ID, error) {
			return zero, fmt.Errorf("cached: ID 为接口类型，缺省解码不可用，请用 WithIDCodec 注入编解码")
		}
	}
	// 精确内建类型快路径：strconv 解析 + any 断言（动态类型精确一致，
	// 断言必成功），热路径零反射。
	switch any(zero).(type) {
	case string:
		return func(s string) (ID, error) { return any(s).(ID), nil }
	case int:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, strconv.IntSize)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(int(v)).(ID), nil
		}
	case int8:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 8)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(int8(v)).(ID), nil
		}
	case int16:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 16)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(int16(v)).(ID), nil
		}
	case int32:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(int32(v)).(ID), nil
		}
	case int64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(v).(ID), nil
		}
	case uint:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, strconv.IntSize)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(uint(v)).(ID), nil
		}
	case uint8:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 8)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(uint8(v)).(ID), nil
		}
	case uint16:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 16)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(uint16(v)).(ID), nil
		}
	case uint32:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 32)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(uint32(v)).(ID), nil
		}
	case uint64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			return any(v).(ID), nil
		}
	}
	// 具名整型（type UserID int64 等）退回 reflect 路径：类型与宽度在
	// 构造期已定，仅保留每次装箱（行为与历史完全一致）。
	switch t.Kind() {
	case reflect.String:
		return func(s string) (ID, error) { return any(s).(ID), nil }
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, t.Bits())
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			out := reflect.New(t).Elem()
			out.SetInt(v)
			return out.Interface().(ID), nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, t.Bits())
			if err != nil {
				return zero, fmt.Errorf("cached: 键段 %q 解析为 %s 失败: %w", s, t, err)
			}
			out := reflect.New(t).Elem()
			out.SetUint(v)
			return out.Interface().(ID), nil
		}
	default:
		return func(string) (ID, error) {
			return zero, fmt.Errorf("cached: ID 类型 %s 不支持缺省键解码，请用 WithIDCodec 注入编解码", t)
		}
	}
}

func (r *CachedRepository[T, ID]) idKey(scope string, id ID) string {
	return r.idKeyPrefix(scope) + r.encodeID(id)
}

func (r *CachedRepository[T, ID]) verKey(scope string) string {
	return r.typePrefix() + scopeSegment(scope) + "ver"
}

func (r *CachedRepository[T, ID]) queryKey(scope, kind, ver, fp string) string {
	return r.typePrefix() + scopeSegment(scope) + "q:" + kind + ":" + ver + ":" + fp
}
