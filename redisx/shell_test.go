package redisx_test

import (
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
