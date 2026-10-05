// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/runtime"
)

// newTestSupervisor returns a runtime.Supervisor rooted at the supplied
// context with a logger that drops output.
func newTestSupervisor() *runtime.Supervisor {
	return runtime.NewSupervisor(context.Background(), nil)
}

func TestWorkerPoolProcessesAndAcks(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	var processed atomic.Int64
	reg.Register("counter", func(ctx context.Context, env *Envelope) error {
		processed.Add(1)
		return nil
	})
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 2, DrainTimeout: time.Second}
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), NewInMemoryDLQ(q, DefaultRetryPolicy), nil)
	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < 10; i++ {
		_ = q.Enqueue(context.Background(), &Envelope{Name: "counter", Payload: []byte("{}")}, EnqueueOptions{})
	}
	// give the pool a moment to process
	deadline := time.Now().Add(2 * time.Second)
	for processed.Load() < 10 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processed.Load() != 10 {
		t.Fatalf("processed %d, want 10", processed.Load())
	}
	_ = pool.Stop(time.Second)
}

func TestWorkerPoolRetriesUntilMax(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	var attempts atomic.Int64
	reg.Register("flaky", func(ctx context.Context, env *Envelope) error {
		_ = attempts.Add(1)
		return errors.New("always fails")
	})
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{
		Concurrency: 1, DrainTimeout: time.Second,
		Retry: RetryPolicy{Base: time.Millisecond, Mult: 1.0, Max: time.Millisecond, Jitter: 0, MaxAttempts: 3},
	}
	dlq := NewInMemoryDLQ(q, cfg.Retry)
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), dlq, nil)
	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = q.Enqueue(context.Background(), &Envelope{Name: "flaky", Payload: []byte("{}")}, EnqueueOptions{MaxAttempts: 3})
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := dlq.Len(context.Background())
		if n > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	n, _ := dlq.Len(context.Background())
	if n != 1 {
		t.Fatalf("dlq len = %d, want 1 after retries exhausted", n)
	}
	if attempts.Load() < 3 {
		t.Errorf("attempts = %d, want >= 3", attempts.Load())
	}
	_ = pool.Stop(time.Second)
}

func TestWorkerPoolPoisonQuarantinesUnknownHandler(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	reg := NewHandlerRegistry() // no handlers registered
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 1, DrainTimeout: time.Second}
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), dlq, nil)
	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = q.Enqueue(context.Background(), &Envelope{Name: "unknown", Payload: []byte("{}")}, EnqueueOptions{})
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := dlq.Len(context.Background())
		if n > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("poison envelope never reached DLQ")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = pool.Stop(time.Second)
}

func TestWorkerPoolErrFatalSendsToDLQImmediately(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	reg.Register("fatal", func(ctx context.Context, env *Envelope) error {
		return ErrFatal
	})
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 1, DrainTimeout: time.Second}
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), dlq, nil)
	_ = pool.Start(context.Background())
	_ = q.Enqueue(context.Background(), &Envelope{Name: "fatal", Payload: []byte("{}")}, EnqueueOptions{})
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := dlq.Len(context.Background())
		if n > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("fatal envelope never reached DLQ")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = pool.Stop(time.Second)
}

func TestWorkerPoolErrPoisonQuarantines(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{Default: time.Millisecond}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	reg.Register("poison", func(ctx context.Context, env *Envelope) error {
		return ErrPoison
	})
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	cfg := WorkerPoolConfig{Concurrency: 1, DrainTimeout: time.Second}
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	pool := NewWorkerPool(sup, q, reg, cfg, NewMetrics(), dlq, nil)
	_ = pool.Start(context.Background())
	_ = q.Enqueue(context.Background(), &Envelope{Name: "poison", Payload: []byte("{}")}, EnqueueOptions{})
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := dlq.Len(context.Background())
		if n > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("poison envelope never reached DLQ")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = pool.Stop(time.Second)
}

func TestWorkerPoolStopIsIdempotent(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	reg := NewHandlerRegistry()
	sup := newTestSupervisor()
	defer sup.Stop(time.Second)
	pool := NewWorkerPool(sup, q, reg, WorkerPoolConfig{Concurrency: 1}, NewMetrics(), nil, nil)
	_ = pool.Start(context.Background())
	_ = pool.Stop(time.Second)
	_ = pool.Stop(time.Second) // should not panic
}

func TestHandlerRegistryLookupMissReturnsNil(t *testing.T) {
	r := NewHandlerRegistry()
	if h := r.Lookup("missing"); h != nil {
		t.Errorf("Lookup should return nil for unregistered name, got %v", h)
	}
}
