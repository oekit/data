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
// 生命周期（#140）：Bus 只是「频道 + 连接」的装配件，可被多个
// TwoLevelCache 经 WithBus 共享——每个实例 Start 时 acquire 自己的订阅
// （独立 goroutine / 独立 ctx watcher），Close 只关闭自己的订阅，不影响
// 其他持有者（旧实现是单一订阅 + 全局 stop，任一实例 Close 把共享 Bus
// 整个杀掉，其余实例的 L1 失效广播无声死亡）。
type Bus struct {
	client  *redisx.Client
	channel string
}

// busSubscription 是一次 acquire 得到的独立订阅：生命周期只属于发起
// acquire 的那个 TwoLevelCache。
type busSubscription struct {
	pubsub *redis.PubSub
	cancel context.CancelFunc
	once   sync.Once
}

// NewBus 创建失效广播。client 用于 Publish 与 Subscribe（redisx.Client
// 的键转换规则对两端一致）；channel 建议按实体类型/键空间划分。
func NewBus(client *redisx.Client, channel string) *Bus {
	return &Bus{client: client, channel: channel}
}

// acquire 建立一条属于调用方的独立订阅（#140）：每个 TwoLevelCache 的
// Start 各自 acquire（各自的 onEvict、各自的 ctx watcher），Close 时
// release 自己的——共享同一 Bus 的实例互不影响。
func (b *Bus) acquire(parent context.Context, onEvict func(key string)) (*busSubscription, error) {
	if b.client == nil {
		// 无 client 的 Bus（零值/显式 nil client）在 TwoLevelCache 装配层
		// 已拒绝；这里兜底防直接调用。
		return nil, errors.New("twolevel: bus 未初始化（NewBus 的 client 不得为 nil）")
	}
	ctx, cancel := context.WithCancel(parent)
	pubsub := b.client.Subscribe(ctx, b.channel)

	s := &busSubscription{pubsub: pubsub, cancel: cancel}
	// ctx 取消（调用方 ctx 或 release 的 cancel）⇒ 订阅收尾；release 幂等
	go func() {
		<-ctx.Done()
		s.release()
	}()
	async.GoSafe(func() {
		msgs := pubsub.Channel()
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
	return s, nil
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

// release 关闭本订阅（幂等）：cancel ctx（令订阅命令与 watcher 退出）并
// Close pubsub。只影响本订阅，不影响同一 Bus 的其他持有者（#140）。
func (s *busSubscription) release() {
	s.once.Do(func() {
		s.cancel()
		if err := s.pubsub.Close(); err != nil {
			// 订阅关闭失败必须留信号（#168，AGENTS.md「所有 error 禁止 `_`
			// 忽略」）：连接未释放不可见，静默丢弃后泄漏无从排查。
			twoLevelLog.Warn("two-level bus subscription close failed", logx.ErrKey, err)
		}
	})
}
