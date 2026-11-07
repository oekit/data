package twolevel

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/logx"
	"github.com/oekit/redisx"
	"github.com/redis/go-redis/v9"
)

// twoLevelLog 懒解析 logger：SDK 基础包禁止 WithService 硬编码 service name。
var twoLevelLog = logx.Delegating("cache-twolevel")

// busVersion 是广播消息格式版本：字段演进时递增，旧实例遇到未知版本
// 按 keys 语义尽力处理（当前仅 v1）。
const busVersion = 1

// busMessage 是失效广播的消息体：一批被失效的缓存键。
type busMessage struct {
	Version int      `json:"v"`
	Keys    []string `json:"keys"`
}

// Bus 是跨实例 L1 失效广播：Del/DelBatch 时向 Redis 频道发布失效消息，
// 订阅循环收到后驱逐本实例的 L1 副本（含发布者自身，幂等）。
//
// 只广播失效、不广播回填：Set/回填意味着 L2 有了新值，他实例旧 L1 由
// 下一次写路径广播驱逐或 TTL 兜底——与 cache-aside 的整体语义一致。
// 发布是 best-effort：失败记日志不阻断删除（远端实例靠 TTL 兜底）。
//
// 生命周期由 TwoLevelCache 管理（WithBus 挂接 + Start/Close），不单独
// Start/Stop；单生命周期：stop 后再 start 直接报错拒绝（ctx watcher 的
// done 通道一次性，复用需新建 Bus）。
type Bus struct {
	client  *redisx.Client
	channel string

	mu        sync.Mutex
	started   bool
	stopped   bool
	pubsub    *redis.PubSub
	done      chan struct{}
	closeOnce sync.Once
}

// 编译期守卫：Bus 依赖 sync.Once/closeOnce。
var _ = sync.Once{}

// NewBus 创建失效广播。client 用于 Publish 与 Subscribe（redisx.Client
// 的键转换规则对两端一致）；channel 建议按实体类型/键空间划分。
func NewBus(client *redisx.Client, channel string) *Bus {
	return &Bus{client: client, channel: channel, done: make(chan struct{})}
}

// start 启动订阅循环（运行中幂等 no-op）。stop 后调用返回错误——done
// 通道一次性，重启只会得到一个 ctx 取消监听已静默失效的循环（watcher
// 立即走已关闭的 done 分支退出，此后 ctx 取消不再触发 stop），必须拒绝。
func (b *Bus) start(ctx context.Context, onEvict func(key string)) error {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return errors.New("twolevel: bus 已停止（单生命周期，复用需新建 Bus）")
	}
	if b.started || b.client == nil {
		b.mu.Unlock()
		return nil
	}
	b.started = true
	pubsub := b.client.Subscribe(ctx, b.channel)
	b.pubsub = pubsub
	b.mu.Unlock()

	// redis.PubSub.Channel() 自带断线重连；Close 后 channel 关闭，循环退出
	msgs := pubsub.Channel()

	// ctx 取消 watcher：go-redis 的 Subscribe(ctx) 只用 ctx 约束订阅命令，
	// Channel() 会一直收到 Close 为止——文档承诺的"ctx 取消停止循环"
	// 由此兑现；显式 stop（Close）经 done 通道让 watcher 退出。
	async.GoSafe(func() {
		select {
		case <-ctx.Done():
			b.stop()
		case <-b.done:
		}
	})

	async.GoSafe(func() {
		for msg := range msgs {
			var m busMessage
			if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
				twoLevelLog.Warn("two-level bus malformed message",
					"channel", b.channel, "err", err)
				continue
			}
			if m.Version > busVersion {
				twoLevelLog.Warn("two-level bus unknown message version, skipped",
					"channel", b.channel, "version", m.Version)
				continue
			}
			for _, k := range m.Keys {
				onEvict(k)
			}
		}
	})
	return nil
}

// publish 发布失效消息（best-effort）。
func (b *Bus) publish(ctx context.Context, keys []string) {
	if b == nil || len(keys) == 0 {
		return
	}
	blob, err := json.Marshal(busMessage{Version: busVersion, Keys: keys})
	if err != nil {
		twoLevelLog.Warn("two-level bus marshal failed", "keys", keys, "err", err)
		return
	}
	if err := b.client.Publish(ctx, b.channel, blob).Err(); err != nil {
		twoLevelLog.Warn("two-level bus publish failed",
			"channel", b.channel, "keys", keys, "err", err)
	}
}

// stop 停止订阅循环。置 stopped 拒绝后续 start（done 通道一次性，见
// start 的注释）；重复调用幂等。
func (b *Bus) stop() {
	b.mu.Lock()
	if b.pubsub != nil {
		_ = b.pubsub.Close()
		b.pubsub = nil
	}
	b.started = false
	b.stopped = true
	b.mu.Unlock()
	// done 关闭一次性（Close 后复用需新建 Bus，见类型文档）
	b.closeOnce.Do(func() { close(b.done) })
}
