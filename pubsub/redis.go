// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pubsub/redis — Redis sharded pub/sub backplane for multi-node (LIVE-009/010).
//
// This backend uses go-redis v9 PubSub with sharding by topic name: topics
// hash to a shard count (default 16) so a Redis cluster can scale fanout.
// Each shard runs one subscription goroutine. Publish uses PUBLISH (O(N)
// on the receiving node) — the sharding keeps any one Redis node from
// becoming a bottleneck.
//
// If Redis is unavailable at construction time, NewRedis returns an
// error; callers (typically the live hub) fall back to the Local backend.
// Reconnect/backoff is governed by go-redis internally.

package pubsub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// RedisConfig configures the Redis backend.
type RedisConfig struct {
	BackendConfig
	// Addr is the Redis host:port.
	Addr string
	// Password (optional).
	Password string
	// DB index (optional).
	DB int
	// Shards controls the topic→shard hash space. Default 16.
	Shards int
	// ChannelPrefix is prepended to all Redis channel names to namespace
	// the live subsystem (e.g. "ogon:live:").
	ChannelPrefix string
}

// Redis is a sharded Redis pubsub Backend.
type Redis struct {
	cfg    RedisConfig
	client *redis.Client

	// per-shard subscription goroutine state
	mu     sync.Mutex
	shards []*redisShard
	closed atomic.Bool
}

// redisShard owns one Redis PubSub connection.
type redisShard struct {
	index int
	psc   *redis.PubSub
	subs  sync.Map // chan Message -> *redisSubscription
	out   chan Message
}

// NewRedis constructs a Redis backend, pinging once to verify reachability.
// Returns an error if Redis is not reachable — callers MUST fall back to
// the Local backend in that case.
func NewRedis(ctx context.Context, cfg RedisConfig) (*Redis, error) {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:6379"
	}
	if cfg.Shards <= 0 {
		cfg.Shards = 16
	}
	if cfg.ChannelPrefix == "" {
		cfg.ChannelPrefix = "ogon:live:"
	}
	cfg.BackendConfig = cfg.BackendConfig.withDefaults()
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ogon/pubsub: redis ping failed: %w", err)
	}
	r := &Redis{cfg: cfg, client: client}
	return r, nil
}

// Publish implements Backend.
//
// We use PUBLISH on the topic's shard channel. The receiving node's
// subscription goroutine forwards the payload to its in-process
// subscribers via the Local fanout pattern.
func (r *Redis) Publish(ctx context.Context, topic string, msg Message) error {
	if r.closed.Load() {
		return errors.New("ogon/pubsub: backend closed")
	}
	msg.Topic = topic
	chanName := r.channelFor(topic)
	// Encode payload with topic prefix so subscribers can route.
	encoded := encodeRedisMsg(msg)
	return r.client.Publish(ctx, chanName, encoded).Err()
}

// Subscribe implements Backend. Patterns are subscribed per-shard; the
// returned Subscription receives messages matching any pattern.
func (r *Redis) Subscribe(ctx context.Context, patterns ...string) (Subscription, error) {
	if r.closed.Load() {
		return nil, errors.New("ogon/pubsub: backend closed")
	}
	if len(patterns) == 0 {
		return nil, errors.New("ogon/pubsub: at least one pattern required")
	}
	sub := &redisSubscription{
		backend:  r,
		patterns: append([]string(nil), patterns...),
		ch:       make(chan Message, r.cfg.BackendConfig.QueueCap),
	}
	// Subscribe per-shard: for each shard, register interest in patterns
	// matching the shard's channel space.
	for i := 0; i < r.cfg.Shards; i++ {
		shard := r.getOrCreateShard(ctx, i)
		shard.subs.Store(sub, struct{}{})
	}
	return sub, nil
}

func (r *Redis) getOrCreateShard(ctx context.Context, idx int) *redisShard {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.shards) <= idx {
		r.shards = append(r.shards, nil)
	}
	if r.shards[idx] == nil {
		shard := &redisShard{
			index: idx,
			out:   make(chan Message, r.cfg.BackendConfig.QueueCap),
		}
		// PSubscribe to the shard pattern: prefix + idx + ".*" so any
		// topic that hashes to this shard arrives here.
		pattern := fmt.Sprintf("%sshard%d:*", r.cfg.ChannelPrefix, idx)
		shard.psc = r.client.PSubscribe(ctx, pattern)
		go r.shardLoop(ctx, shard, pattern)
		r.shards[idx] = shard
	}
	return r.shards[idx]
}

// shardLoop drains the Redis PubSub channel and fans out to subscribers.
func (r *Redis) shardLoop(ctx context.Context, shard *redisShard, pattern string) {
	defer close(shard.out)
	ch := shard.psc.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			decoded, ok := decodeRedisMsg([]byte(msg.Payload))
			if !ok {
				continue
			}
			// Fan out to subscribers of this shard.
			shard.subs.Range(func(k, _ any) bool {
				sub := k.(*redisSubscription)
				if !sub.matches(decoded.Topic) {
					return true
				}
				select {
				case sub.ch <- decoded:
				default:
					// drop-oldest
					if r.cfg.BackendConfig.DropPolicy == "drop-oldest" {
						select {
						case <-sub.ch:
						default:
						}
						select {
						case sub.ch <- decoded:
						default:
						}
					}
				}
				return true
			})
		}
	}
}

// channelFor picks the shard channel for a topic.
func (r *Redis) channelFor(topic string) string {
	idx := shardIndex(topic, r.cfg.Shards)
	return fmt.Sprintf("%sshard%d:%s", r.cfg.ChannelPrefix, idx, topic)
}

// shardIndex hashes topic to a shard in [0, n).
func shardIndex(topic string, n int) int {
	h := uint32(0)
	for i := 0; i < len(topic); i++ {
		h = h*16777619 ^ uint32(topic[i])
	}
	return int(h % uint32(n))
}

// Close implements Backend.
func (r *Redis) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	shards := r.shards
	r.shards = nil
	r.mu.Unlock()
	for _, s := range shards {
		if s == nil {
			continue
		}
		_ = s.psc.Close()
	}
	return r.client.Close()
}

// redisSubscription is the Subscription returned by Redis.Subscribe.
type redisSubscription struct {
	backend  *Redis
	patterns []string
	ch       chan Message
	once     sync.Once
}

func (s *redisSubscription) Messages() <-chan Message { return s.ch }

func (s *redisSubscription) Close() error {
	s.once.Do(func() {
		close(s.ch)
		for i := 0; i < s.backend.cfg.Shards; i++ {
			if i < len(s.backend.shards) && s.backend.shards[i] != nil {
				s.backend.shards[i].subs.Delete(s)
			}
		}
	})
	return nil
}

func (s *redisSubscription) matches(topic string) bool {
	return matchAny(s.patterns, topic)
}

// encodeRedisMsg/decodeRedisMsg: length-prefixed topic + payload.
// Format: "<len>:<topic><payload>" where len is decimal topic length.
func encodeRedisMsg(m Message) []byte {
	tl := strconv.Itoa(len(m.Topic))
	hdr := tl + ":"
	out := make([]byte, len(hdr)+len(m.Topic)+len(m.Payload))
	copy(out, hdr)
	copy(out[len(hdr):], m.Topic)
	copy(out[len(hdr)+len(m.Topic):], m.Payload)
	return out
}

func decodeRedisMsg(b []byte) (Message, bool) {
	// find first ':'
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(b) || b[i] != ':' {
		return Message{}, false
	}
	n, err := strconv.Atoi(string(b[:i]))
	if err != nil || i+1+n > len(b) {
		return Message{}, false
	}
	topic := string(b[i+1 : i+1+n])
	payload := b[i+1+n:]
	return Message{Topic: topic, Payload: payload}, true
}
