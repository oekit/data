package twolevel

// 本文件是 AGENTS.md「所有 error 禁止 `_` 忽略」硬规则的回归钉（#168）：
// 驱逐失败 / 订阅关闭失败必须留 Warn 信号——evictLocal 是跨实例失效的唯一
// 执行点，静默失败时该实例继续对外提供陈旧 L1 值，运维侧必须能从日志
// 看到（与 backfillLocal 的 Warn 口径一致）。用内部包测试直接触达未导出
// 收尾点。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/oekit/logx"
)

// stubLocal Del 恒返回注入错误的本地缓存 fake（其余方法 no-op，仅装配用）。
type stubLocal struct{ delErr error }

func (s stubLocal) Get(context.Context, string) (*int, error) { return nil, nil }
func (s stubLocal) Set(context.Context, string, *int) error   { return nil }
func (s stubLocal) SetWithTTL(context.Context, string, *int, time.Duration) error {
	return nil
}
func (s stubLocal) Del(context.Context, string) error             { return s.delErr }
func (s stubLocal) DelBatch(_ context.Context, _ ...string) error { return s.delErr }
func (s stubLocal) Exists(context.Context, string) (bool, error)  { return false, nil }

// warnRecord 一条 Warn 快照（msg + kv 平铺对）。
type warnRecord struct {
	msg string
	kv  map[string]any
}

// warnRecorder 捕获 Warn 的 logx.Logger：只拦截 Warn 与 WithAttrs——
// Delegating 的解析路径是 Default().WithAttrs(...)，必须落回捕获器，
// Warn 才进记录；其余方法透传基座。
type warnRecorder struct {
	logx.Logger
	mu    sync.Mutex
	warns []warnRecord
}

func (r *warnRecorder) Warn(msg string, keysAndValues ...any) {
	kv := map[string]any{}
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		if k, ok := keysAndValues[i].(string); ok {
			kv[k] = keysAndValues[i+1]
		}
	}
	r.mu.Lock()
	r.warns = append(r.warns, warnRecord{msg: msg, kv: kv})
	r.mu.Unlock()
}

// WithAttrs 返回自身：解析链必须落回捕获器（基座的 WithAttrs 会绕开记录）。
func (r *warnRecorder) WithAttrs(...logx.Attr) logx.Logger { return r }

// requireWarn 断言存在 msg 含 sub 且 match(kv) 为真的 Warn 记录。
func (r *warnRecorder) requireWarn(t *testing.T, sub string, match func(map[string]any) bool) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var seen []string
	for _, w := range r.warns {
		if strings.Contains(w.msg, sub) && match(w.kv) {
			return
		}
		seen = append(seen, w.msg)
	}
	t.Fatalf("未捕获 %q 的 Warn 信号（已捕获 %d 条：%v）", sub, len(seen), seen)
}

// installWarnRecorder 把全局默认 logger 换成捕获器并在 cleanup 恢复
// （Delegating 每次调用重新解析 Default，SetDefault 即生效）。
func installWarnRecorder(t *testing.T) *warnRecorder {
	t.Helper()
	rec := &warnRecorder{Logger: logx.Default()}
	logx.SetDefault(rec)
	t.Cleanup(func() { logx.SetDefault(rec.Logger) })
	return rec
}

// TestEvictLocalDelFailureLeavesWarnSignal 钉 #168：L1 驱逐失败必须留日志
// 信号——evictLocal 是跨实例失效的唯一执行点，静默失败时该实例继续对外
// 提供陈旧 L1 值，仅靠 TTL 兜底（与 backfillLocal 的 Warn 口径一致）。
func TestEvictLocalDelFailureLeavesWarnSignal(t *testing.T) {
	rec := installWarnRecorder(t)
	errDel := errors.New("local del boom")
	c := &TwoLevelCache[int]{local: stubLocal{delErr: errDel}}

	c.evictLocal("stale-key")

	rec.requireWarn(t, "two-level cache local evict failed", func(kv map[string]any) bool {
		err, _ := kv[logx.ErrKey].(error)
		return errors.Is(err, errDel)
	})
}

// TestBusSubscriptionCloseFailureLeavesWarnSignal 钉 #168：订阅关闭失败必须
// 留日志信号（连接未释放同样不可见）。pubsub 双 Close 第二次返回 ErrClosed，
// 以此注入确定性的 Close 错误。
func TestBusSubscriptionCloseFailureLeavesWarnSignal(t *testing.T) {
	rec := installWarnRecorder(t)
	mr := miniredis.RunT(t)
	ps := redis.NewClient(&redis.Options{Addr: mr.Addr()}).Subscribe(context.Background(), "test:close-guard")
	if err := ps.Close(); err != nil {
		t.Fatalf("预置 Close（注入 ErrClosed）: %v", err)
	}
	s := &busSubscription{pubsub: ps, cancel: func() {}}

	s.release() // 第二次 Close 返回 ErrClosed——修复前被 `_ =` 吞掉

	rec.requireWarn(t, "two-level bus subscription close failed", func(kv map[string]any) bool {
		err, _ := kv[logx.ErrKey].(error)
		return err != nil
	})
}
