package redisx_test

import (
	"reflect"
	"testing"

	"github.com/oekit/data/internal/testsame"
	"github.com/oekit/data/redisx"
	redisxsrc "github.com/oekit/redisx"
)

// TestTypeIdentity option 函数值同一性（var re-export 的函数值与源完全同一）。
func TestTypeIdentity(t *testing.T) {
	f1 := redisx.WithPrefix("shell:")
	testsame.Assert[redisx.RedisClientOptionFunc](f1) // 双向同一

	opt := &redisx.RedisClientOption{Prefix: "shell:"}
	testsame.Assert[*redisxsrc.RedisClientOption](opt) // 结构体类型同一
}

// TestFuncIdentity 门面 re-export 的**函数值**与源包同一（指针相等）。
//
// 为什么单独测：type alias 的同一性由编译期保证，而 var 形式的函数 re-export
// 同一性靠的是「引的是同一个函数」——写错了（例如包了一层闭包）编译期照样过，
// 只有比指针才抓得到。源包新增导出符号时，门面壳必须原样转出（门面跟版税）。
func TestFuncIdentity(t *testing.T) {
	if reflect.ValueOf(redisx.NewClientFromClient).Pointer() !=
		reflect.ValueOf(redisxsrc.NewClientFromClient).Pointer() {
		t.Fatal("redisx.NewClientFromClient 与源包不是同一函数值")
	}
}
