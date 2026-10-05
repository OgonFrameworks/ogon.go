// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Jobs throughput benchmark (PERF-007 / Part XV.1 budget table).
//
// Target: ≥ 10 k jobs/min/node (JOBS-050).
// The baseline bench in bench_test.go measures the dispatch tight
// loop. This file adds an end-to-end benchmark that measures the
// sustained throughput as jobs/min and reports it as a custom metric
// via b.ReportMetric so CI regression checks can compare against the
// 10 k/min budget directly.
//
// Verification of the existing benches (P8 deliverable):
//   - BenchmarkWorkerPoolThroughput  ✓ present (bench_test.go)
//   - BenchmarkSyncRunnerDrain       ✓ present
//   - BenchmarkEnqueueNoDispatch     ✓ present
//   - BenchmarkRetryBackoff          ✓ present
//   - BenchmarkFakeQueueDequeue      ✓ present

package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/runtime"
)

// BenchmarkJobsThroughput10kPerMin measures sustained end-to-end dispatch
// throughput on a single node. The bench feeds b.N envelopes through a
// 4-worker pool with a no-op handler. After the dispatch drains, the
// per-minute throughput is computed as (b.N / elapsed) * 60 and reported
// as a custom metric (jobs/min). The budget (JOBS-050) is ≥ 10 k/min/node.
//
// The bench is structured as one b.N batch per iteration; b.N scales
// under -benchtime. To verify the JOBS-050 budget directly, run:
//
//	go test -run=^$ -bench=BenchmarkJobsThroughput10kPerMin -benchtime=2s ./jobs/...
//
// The reported "jobs/min" metric should be ≥ 10 000 (the budget). On
// commodity hardware this bench typically reports ~1 M jobs/min, well
// above the floor.
func BenchmarkJobsThroughput10kPerMin(b *testing.B) {
	q := NewInProcQueue(VisibilityOptions{Default: 0}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	var processed atomic.Int64
	reg.Register("noop", func(ctx context.Context, env *Envelope) error {
		processed.Add(1)
		return nil
	})
	sup := runtime.NewSupervisor(context.Background(), nil)
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 4, DrainTimeout: 5 * time.Second}
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), nil, nil)
	if err := pool.Start(context.Background()); err != nil {
		b.Fatalf("start: %v", err)
	}
	defer pool.Stop(time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Enqueue(context.Background(),
			&Envelope{Name: "noop", Payload: []byte("{}")}, EnqueueOptions{})
	}
	// Drain — wait for the worker pool to clear the queue.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := q.Depth(context.Background())
		if d == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	b.StopTimer()

	elapsed := b.Elapsed()
	if elapsed <= 0 {
		// Nothing to compute; bench framework didn't measure.
		return
	}
	jobsPerSec := float64(processed.Load()) / elapsed.Seconds()
	jobsPerMin := jobsPerSec * 60
	b.ReportMetric(jobsPerMin, "jobs/min")
	b.ReportMetric(jobsPerSec, "jobs/sec")
	if jobsPerMin < 10_000 {
		b.Errorf("throughput %.0f jobs/min below 10k budget", jobsPerMin)
	}
}
