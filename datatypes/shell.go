// Package datatypes 是 data 门面子包：泛型 JSON 列类型（结构化 JSON 列的
// Scan/Value 一次到位）。
//
// re-export 自 github.com/oekit/gormx/datatypes：类型为泛型 type alias（与
// 源包**完全同一类型**，双向赋值互通；底层类型就是 []T / map[K]V，方法随
// alias 走）。源包新增导出符号时本壳需同步（门面跟版税，data 门面化决策
// 已接受）。
//
//	type Channel struct {
//	    gormx.BaseModel
//	    Tags   datatypes.JSONSlice[string] // []string 的 JSON 列
//	    Labels datatypes.JSONMap[string]int
//	}
//
// 失败语义与源包一致：非法 JSON、不可序列化值、非文本来源都显式返回
// error（不吞零值）；NULL 列 Scan 回 nil 零值，与显式写入的空集合可区分。
package datatypes

import src "github.com/oekit/gormx/datatypes"

// JSONSlice 是 []T 的 JSON 列类型（与源包同一类型）。
type JSONSlice[T any] = src.JSONSlice[T]

// JSONMap 是 map[K]V 的 JSON 列类型（与源包同一类型）。
type JSONMap[K comparable, V any] = src.JSONMap[K, V]
