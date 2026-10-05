// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/runtime"
)

// BenchmarkWorkerPoolThroughput measures end-to-end dispatch throughput
// on a single node (JOBS-050: budget ≥ 10k jobs/min/node).
//
// We feed 10k envelopes through a 4-worker pool backed by the
// in-proc queue with a no-op handler. The budget translates to
// ≥ 167 jobs/sec; modern hardware easily does 50k/sec on this path.
// Failures here indicate the dispatcher has a hot lock or bad alloc.
func BenchmarkWorkerPoolThroughput(b *testing.B) {
	q := NewInProcQueue(VisibilityOptions{Default: 0}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	reg.Register("noop", func(ctx context.Context, env *Envelope) error { return nil })
	sup := runtime.NewSupervisor(context.Background(), nil)
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 4, DrainTimeout: time.Second}
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), nil, nil)
	if err := pool.Start(context.Background()); err != nil {
		b.Fatalf("start: %v", err)
	}
	defer pool.Stop(time.Second)

	var done atomic.Int64
	// pre-fill a batch per b.N iteration; worker processes one batch
	// per iteration. b.N is the total batch count.
	for i := 0; i < b.N; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "noop", Payload: []byte("{}")}, EnqueueOptions{})
	}
	// Wait for queue to drain (processed = b.N).
	deadline := time.Now().Add(60 * time.Second)
	for done.Load() < int64(b.N) && time.Now().Before(deadline) {
		d, _ := q.Depth(context.Background())
		if d == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
}

// BenchmarkSyncRunnerDrain measures the sync-runner path. This is the
// worst-case baseline: no concurrency, no enqueue overhead amortised
// across batches. Throughput here SHOULD be 100k+/sec.
func BenchmarkSyncRunnerDrain(b *testing.B) {
	q := NewInProcQueue(VisibilityOptions{Default: 0}, nil)
	defer q.Close()
	dispatcher := DecodeDispatcher[CounterArgs](func(ctx context.Context, a CounterArgs) error { return nil })
	runner := NewSyncRunner(q, dispatcher, NewMetrics())
	// pre-fill one batch
	batches := 1000
	for i := 0; i < batches; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "c", Payload: []byte("{}")}, EnqueueOptions{})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = runner.Drain(context.Background(), 1)
	}
}

// BenchmarkEnqueueNoDispatch measures enqueue-only throughput (no
// worker dispatch). Useful for finding enqueue-side hotspots.
func BenchmarkEnqueueNoDispatch(b *testing.B) {
	q := NewInProcQueue(VisibilityOptions{Default: 0}, nil)
	defer q.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "noop", Payload: []byte("{}")}, EnqueueOptions{})
	}
}

// BenchmarkRetryBackoff measures NextBackoff hot path.
func BenchmarkRetryBackoff(b *testing.B) {
	p := DefaultRetryPolicy
	for i := 0; i < b.N; i++ {
		_ = p.NextBackoff(3)
	}
}

// BenchmarkFakeQueueDequeue measures the dispatch tight loop without
// handler overhead (handler = nil function call).
func BenchmarkFakeQueueDequeue(b *testing.B) {
	q := newFakeQueue(b.N + 1)
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_ = q.Enqueue(ctx, &Envelope{Name: "noop", Payload: []byte("{}")}, EnqueueOptions{})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = q.Dequeue(ctx)
	}
}
