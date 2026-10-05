// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Live fanout benchmark (PERF-006 / Part XV.1 budget table).
//
// Target: 10 k simulated connections; measure the per-broadcast latency.
// Each iteration enqueues one envelope to 10 000 mock connections via
// the hub's fanout worker pool. The bench measures the publish-side
// cost (BroadcastToChannel), which is the goroutine-blocking slice the
// caller pays. The actual delivery happens asynchronously on the
// fanout worker pool; that cost is amortised across the worker count.

package live

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/pubsub"
	"github.com/OgonFrameworks/ogon.go/runtime"
)

// newBenchHub builds a Hub with the high-rate-limit / large-queue config
// used by the fanout benchmarks. MsgPerSec is bumped so the rate limiter
// does not drop test messages; QueueCap is bumped so the fanout queue
// does not back up against the worker pool's bound.
func newBenchHub() *Hub {
	backend := pubsub.NewLocal(pubsub.BackendConfig{QueueCap: 1024})
	cfg := HubConfig{
		WorkerPoolSize:  16,
		QueueCap:        512,
		DropPolicy:      DropOldest,
		MsgPerSec:       1_000_000, // effectively unlimited for the bench
		SlowClientEvict: 5 * time.Second,
		MaxConns:        20_000,
		MaxConnsPerUser: 20_000,
		ResumeWindowCap: 1024,
		ResumeTTL:       10 * time.Second,
	}
	sup := runtime.NewSupervisor(context.Background(), nil)
	h := NewHub(cfg, backend, sup, nil)
	return h
}

// BenchmarkFanout10kConns measures the time spent inside one
// BroadcastToChannel call when the channel has 10 000 subscribed mock
// connections. Each iteration walks the subs slice (O(N)) and pushes one
// fanoutJob per connection to the hub's bounded worker pool.
//
// Budget target: 10 k simulated conns with bounded fanout latency. The
// measured cost is the publish-side path (the goroutine the broadcaster
// runs on); delivery to per-conn queues happens on the worker pool
// asynchronously. The reported ns/op is the per-broadcast cost — divide
// by 10 000 for the per-connection cost.
func BenchmarkFanout10kConns(b *testing.B) {
	h := newBenchHub()
	defer h.Close(context.Background()) //nolint:errcheck

	const conns = 10_000
	ctx := context.Background()
	const channel = "bench.fanout"

	// Spin up conns and subscribe each to the bench channel.
	for i := 0; i < conns; i++ {
		c := newConnection(fmt.Sprintf("c%d", i), User{ID: fmt.Sprintf("u%d", i)}, h)
		if err := h.Register(c); err != nil {
			b.Fatalf("register %d: %v", i, err)
		}
		if err := c.Subscribe(ctx, channel); err != nil {
			b.Fatalf("subscribe %d: %v", i, err)
		}
	}

	env := &Envelope{Type: TypeMessage, Payload: []byte(`{"hi":1}`)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// BroadcastToChannel returns queued count; for a 10k-channel this is 10k.
		queued := h.BroadcastToChannel(ctx, channel, env)
		if queued != conns {
			b.Fatalf("queued = %d, want %d", queued, conns)
		}
	}
}

// BenchmarkFanout1kConns is a smaller variant for faster CI runs.
// Same shape, 1 k connections; budget is the same per-conn cost.
func BenchmarkFanout1kConns(b *testing.B) {
	h := newBenchHub()
	defer h.Close(context.Background()) //nolint:errcheck

	const conns = 1_000
	ctx := context.Background()
	const channel = "bench.fanout1k"

	for i := 0; i < conns; i++ {
		c := newConnection(fmt.Sprintf("c%d", i), User{ID: fmt.Sprintf("u%d", i)}, h)
		if err := h.Register(c); err != nil {
			b.Fatalf("register %d: %v", i, err)
		}
		if err := c.Subscribe(ctx, channel); err != nil {
			b.Fatalf("subscribe %d: %v", i, err)
		}
	}

	env := &Envelope{Type: TypeMessage, Payload: []byte(`{"hi":1}`)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		queued := h.BroadcastToChannel(ctx, channel, env)
		if queued != conns {
			b.Fatalf("queued = %d, want %d", queued, conns)
		}
	}
}

// BenchmarkFanoutConcurrent measures concurrent broadcast behaviour:
// multiple goroutines broadcast to disjoint channels simultaneously.
// The atomic counter confirms no broadcasts were dropped on the
// publish-side path; the bench reports per-broadcast cost under load.
func BenchmarkFanoutConcurrent(b *testing.B) {
	h := newBenchHub()
	defer h.Close(context.Background()) //nolint:errcheck

	const channels = 32
	const connsPerChannel = 64
	ctx := context.Background()

	for ch := 0; ch < channels; ch++ {
		channel := fmt.Sprintf("bench.chan.%d", ch)
		for i := 0; i < connsPerChannel; i++ {
			c := newConnection(fmt.Sprintf("c%d.%d", ch, i), User{ID: fmt.Sprintf("u%d.%d", ch, i)}, h)
			if err := h.Register(c); err != nil {
				b.Fatalf("register %d.%d: %v", ch, i, err)
			}
			if err := c.Subscribe(ctx, channel); err != nil {
				b.Fatalf("subscribe %d.%d: %v", ch, i, err)
			}
		}
	}

	var queued atomic.Int64
	env := &Envelope{Type: TypeMessage, Payload: []byte(`{"hi":1}`)}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ch := int(queued.Add(1)) % channels
			channel := fmt.Sprintf("bench.chan.%d", ch)
			q := h.BroadcastToChannel(ctx, channel, env)
			if q != connsPerChannel {
				b.Fatalf("queued = %d, want %d", q, connsPerChannel)
			}
		}
	})
}
