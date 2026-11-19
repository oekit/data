package cached

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/oekit/pkg/errors"
)

// 本文件是键空间与 ID 编解码职责（#179 拆分）：作用域解析、键构造、
// ID 键段的编解码及其缺省解析器。

// checkKeySegment 单键段边界校验（#285）：cached 键空间按 ':' 分层
// （typePrefix / scopeSegment / idKey / queryKey 均裸 ':' 拼接），段内
// 含 ':' 即与分隔符不可区分——scope "a"+ID "b:c" 与 scope "a:id:b"+ID
// "c" 拼出同一个 idKey，语义不同的两条目共享同一缓存条目（跨实体串
// 缓存键，A 的结果被当成 B 的返回；pkg/cache.Key 同款事故形态 #188）。
// 与 etcdx/internal/keyseg 的键路径段立约同族（registry#110 /
// distlock#138：路径段含分隔符时 fail-fast，把键层级边界变成不变量）。
// kind 仅用于错误文案定位（如 "作用域键" / "ID 键段" / "前缀" / "类型名"）。
func checkKeySegment(kind, seg string) error {
	if strings.Contains(seg, ":") {
		return errors.Newf("cached: %s %q 含 ':'——键段不得含分隔符 ':'（跨实体串键，#285；同族先例 etcdx registry#110/distlock#138、pkg/cache#188）", kind, seg)
	}
	return nil
}

// scope 解析当前调用的作用域键。
//
// 未启用作用域隔离时返回空串（键形态与历史一致）。启用后：
//   - 解析出错 ⇒ 返回错误（**不**回落共享键）；
//   - 解析出空串 ⇒ 返回错误（空串等于没开隔离）；
//   - 解析出含 ':' 的串 ⇒ 返回错误（#285：':' 是键分层分隔符，含 ':'
//     的作用域与相邻键段不可区分，绝不以歧义键触碰缓存）。
func (r *CachedRepository[T, ID]) scope(ctx context.Context) (string, error) {
	if r.cfg.scopeKey == nil {
		return "", nil
	}
	s, err := r.cfg.scopeKey(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "cached: 解析作用域键失败")
	}
	if s == "" {
		return "", errors.New("cached: 作用域键为空——作用域隔离已启用但未解析出作用域（拒绝回落共享键）")
	}
	if err := checkKeySegment("作用域键", s); err != nil {
		return "", err
	}
	return s, nil
}

// scopeSegment 作用域段（无作用域时为空串）。scope 内容已由 r.scope()
// 做过段边界校验（#285），此处只做拼接。
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
//
// ⚠️ 产物是单个键段，不得含 ':'（#285）：缺省编码对整型恒为十进制数字
// （天然安全）；string 直通与 WithIDCodec 自定义编码都可能产出 ':'。
// 进缓存键的路径统一走 encodeIDSegment（含段校验）；bloomItem 是单段
// 等值比较、不参与键拼接，可直接用本函数。
func (r *CachedRepository[T, ID]) encodeID(id ID) string {
	if r.cfg.encode != nil {
		return r.cfg.encode(id)
	}
	return fmt.Sprintf("%v", id)
}

// encodeIDSegment ID → 键段字符串并做段边界校验（#285）：encodeID 产物
// 含 ':' 即与键分隔符不可区分——文本主键 "b:c"（scope "a"）与 ID "c"
// （scope "a:id:b"）拼出同一个 idKey，跨实体串缓存键。逐键校验的代价是
// 一次 strings.Contains（短键段为纳秒级，相对缓存 I/O 可忽略，见
// idaccessor 基准）；含 ':' 时返回错误，调用方降级直查/跳过失效并上报，
// 绝不以歧义键触碰缓存。
func (r *CachedRepository[T, ID]) encodeIDSegment(id ID) (string, error) {
	seg := r.encodeID(id)
	if err := checkKeySegment("ID 键段", seg); err != nil {
		return "", err
	}
	return seg, nil
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
			return zero, errors.New("cached: ID 为接口类型，缺省解码不可用，请用 WithIDCodec 注入编解码")
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
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(int(v)).(ID), nil
		}
	case int8:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 8)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(int8(v)).(ID), nil
		}
	case int16:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 16)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(int16(v)).(ID), nil
		}
	case int32:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(int32(v)).(ID), nil
		}
	case int64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(v).(ID), nil
		}
	case uint:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, strconv.IntSize)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(uint(v)).(ID), nil
		}
	case uint8:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 8)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(uint8(v)).(ID), nil
		}
	case uint16:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 16)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(uint16(v)).(ID), nil
		}
	case uint32:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 32)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			return any(uint32(v)).(ID), nil
		}
	case uint64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
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
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			out := reflect.New(t).Elem()
			out.SetInt(v)
			return out.Interface().(ID), nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(s string) (ID, error) {
			v, err := strconv.ParseUint(s, 10, t.Bits())
			if err != nil {
				return zero, errors.Wrapf(err, "cached: 键段 %q 解析为 %s 失败", s, t)
			}
			out := reflect.New(t).Elem()
			out.SetUint(v)
			return out.Interface().(ID), nil
		}
	default:
		return func(string) (ID, error) {
			return zero, errors.Newf("cached: ID 类型 %s 不支持缺省键解码，请用 WithIDCodec 注入编解码", t)
		}
	}
}

// idKey 实体点读键（"前缀:类型:scope:<作用域>:id:<键段>"）。ID 键段经
// encodeIDSegment 校验（#285），scope 已由调用方经 r.scope() 校验，
// 键内各段与分隔符可辨 ⇒ 键空间无歧义。
func (r *CachedRepository[T, ID]) idKey(scope string, id ID) (string, error) {
	seg, err := r.encodeIDSegment(id)
	if err != nil {
		return "", err
	}
	return r.idKeyPrefix(scope) + seg, nil
}

// verKey 查询缓存版本键。ver 段由 newVersionToken 产出（随机 hex /
// 纳秒时间戳 base36，均不含 ':'），scope 已校验 ⇒ 无需再查（#285）。
func (r *CachedRepository[T, ID]) verKey(scope string) string {
	return r.typePrefix() + scopeSegment(scope) + "ver"
}

// queryKey 查询缓存键（"前缀:类型:scope:<作用域>:q:<kind>:<ver>:<fp>"）。
// kind 限定内部常量（"one"/"list"）、ver 同 verKey、fp 在键的最末段
// （即便自定义指纹产出 ':'，后面再无其他段可被挪位，键空间仍无歧义）
// ——三者都不是外部输入，scope 已校验 ⇒ 无需段校验（#285）。
func (r *CachedRepository[T, ID]) queryKey(scope, kind, ver, fp string) string {
	return r.typePrefix() + scopeSegment(scope) + "q:" + kind + ":" + ver + ":" + fp
}
