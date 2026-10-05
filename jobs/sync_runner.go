// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — sync runner + fake clock for tests (JOBS-026).
//
// The sync runner drains a queue synchronously, dispatching to the
// registered handlers without spawning a worker pool. It is the
// deterministic alternative to the worker pool for tests: no
// goroutine races, no timing flakiness.
//
// The fake clock controls time-based behaviour (retry backoff,
// visibility timeout, idempotency expiry) by injecting a now()
// function. Drivers that respect a supplied now function can be
// tested deterministically.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// FakeClock is a deterministic clock. Now returns the current fake
// time; Advance moves it forward. Safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a clock pinned to the supplied start (or
// 2024-01-01T00:00:00Z when zero).
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &FakeClock{now: start}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d. Returns the new now.
func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Set jumps the clock to t. t must be >= Now() or this is a no-op.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.now) {
		c.now = t
	}
}

// SyncRunner drains a queue synchronously, dispatching each envelope
// to the supplied dispatcher. It is intended for tests: no
// goroutines, no races.
type SyncRunner struct {
	queue      Queue
	dispatcher func(ctx context.Context, env *Envelope) error
	metrics    *Metrics
	stopped    atomic.Bool
}

// NewSyncRunner constructs a sync runner. dispatcher MUST be the
// handler-dispatch closure created by the registry.
func NewSyncRunner(q Queue, dispatcher func(context.Context, *Envelope) error, m *Metrics) *SyncRunner {
	if m == nil {
		m = NewMetrics()
	}
	return &SyncRunner{queue: q, dispatcher: dispatcher, metrics: m}
}

// Drain processes up to max envelopes synchronously. Returns the
// number processed and the first error encountered (after which it
// stops). ctx.Err() aborts the drain.
//
// Each Dequeue call uses a per-call timeout (drainPollTimeout) so
// callers can pass a context with a long deadline and still get
// prompt exit when the queue drains.
func (r *SyncRunner) Drain(ctx context.Context, max int) (int, error) {
	processed := 0
	for max <= 0 || processed < max {
		if r.stopped.Load() {
			return processed, nil
		}
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		dctx, cancel := context.WithTimeout(ctx, drainPollTimeout)
		env, rcpt, err := r.queue.Dequeue(dctx)
		cancel()
		if err != nil {
			if errors.Is(err, ErrEmpty) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return processed, nil
			}
			return processed, err
		}
		span := StartSpan(r.metrics, env.Name)
		err = r.dispatcher(ctx, env)
		span.Stop()
		if err == nil {
			_ = r.queue.Ack(ctx, rcpt)
			r.metrics.RecordAck(env.Name)
		} else {
			r.metrics.RecordRetry(env.Name)
		}
		processed++
	}
	return processed, nil
}

// drainPollTimeout caps each Dequeue call inside Drain. The SyncRunner
// is for tests; a 100ms cap is fast enough that draining an empty
// queue costs at most one tick before exit.
const drainPollTimeout = 100 * time.Millisecond

// Stop signals Drain to exit at the next envelope boundary.
func (r *SyncRunner) Stop() { r.stopped.Store(true) }

// DecodeDispatcher is a helper that decodes the envelope's payload
// into the supplied typed args and delegates to the supplied handler.
// Use as the dispatcher argument to NewSyncRunner.
func DecodeDispatcher[A Args](h Handler[A]) func(context.Context, *Envelope) error {
	return func(ctx context.Context, env *Envelope) error {
		var a A
		if err := json.Unmarshal(env.Payload, &a); err != nil {
			return err
		}
		return h(ctx, a)
	}
}
