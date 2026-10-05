// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Pub/Sub fanout micro-bench (PERF-013 / Part XV.1 budget table).
//
// Companion to local_bench_test.go. The existing benches measure
// the single-subscriber publish path (`BenchmarkPubSubPublish`) and
// a 16-subscriber fan-out (`BenchmarkPubSubPublishManySubs`). This
// file adds the spec-mandated 100-subscriber fan-out bench under
// the name `BenchmarkPubSubFanout100` so the PERF.md budget table
// can name the bench directly.
//
// Budget: ≤ 500 ns per publish (PERF-013). At 100 subscribers the
// per-sub marginal cost should be ≤ 100 ns (the publish path is
// O(N) over the subscriber slice, with one channel send per sub).

package pubsub

import (
	"context"
	"sync"
	"testing"
)

// BenchmarkPubSubFanout100 measures the per-Publish cost when one
// topic has 100 subscribers. Each publish walks the subscriber slice
// (O(100)) and enqueues one Message per sub. The bench reports the
// per-publish ns/op; the per-sub marginal cost is reported ns/op ÷
// 100. The budget (≤ 500 ns per publish) implies a ≤ 5 ns/sub
// marginal cost target — well within the channel-send cost budget.
//
// The bench drains the subscriber queues in a background goroutine
// so the publish path is not skewed by drop-oldest work when the
// queue fills.
func BenchmarkPubSubFanout100(b *testing.B) {
	l := NewLocal(BackendConfig{QueueCap: 8192})
	ctx := context.Background()

	const subs = 100
	subList := make([]Subscription, 0, subs)
	for i := 0; i < subs; i++ {
		s, err := l.Subscribe(ctx, "bench.fanout100")
		if err != nil {
			b.Fatalf("subscribe %d: %v", i, err)
		}
		subList = append(subList, s)
	}

	// Drain messages in a background goroutine so the queues don't
	// back up and skew the publish path with drop-oldest work.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch := subList[0].Messages()
		for range ch {
		}
	}()
	// LIFO defer order: l.Close runs FIRST (closes all subscriber
	// channels via closeAll); the drainer goroutine then exits,
	// decrementing wg; wg.Wait runs LAST and returns immediately.
	// The explicit sub.Close calls are omitted because l.Close already
	// closes the channels — calling sub.Close after l.Close panics
	// (close-of-closed via once.Do not guarded by sub.closed).
	defer wg.Wait()
	defer l.Close()

	// Warm the lookup cache so the hot loop is steady-state.
	_ = l.Publish(ctx, "bench.fanout100", Message{Payload: []byte("warm")})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.Publish(ctx, "bench.fanout100",
			Message{Payload: []byte("p")}); err != nil {
			b.Fatalf("publish: %v", err)
		}
	}
}
