// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — in-process driver (JOBS-004).
//
// The InProc driver is a process-local FIFO queue backed by a Go
// channel + a sync.Map of in-flight receipts. It is the default for
// `ogon dev` and the test sync runner. There is no persistence across
// restarts — every envelope in flight at shutdown is lost. That is
// acceptable for dev/test; production uses DB or Redis.

package jobs

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// InProcQueue is the in-process driver. Safe for concurrent use.
type InProcQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	ready    []*inProcEntry
	inflight map[string]*inProcEntry // envelope ID -> entry
	closed   bool
	vis      VisibilityOptions
	enf      *Enforcer
}

// inProcEntry is the in-memory representation of a queued envelope.
type inProcEntry struct {
	env       Envelope
	claim     time.Time // when Dequeue claimed it
	lease     time.Duration
	extension time.Time // next VisibleAt after heartbeat
}

// NewInProcQueue constructs an in-proc driver with the supplied
// visibility defaults and dedup enforcer (may be nil).
func NewInProcQueue(vis VisibilityOptions, enf *Enforcer) *InProcQueue {
	vis = vis.WithDefaults()
	if enf == nil {
		enf = NewEnforcer(5 * time.Minute)
	}
	q := &InProcQueue{
		inflight: make(map[string]*inProcEntry),
		vis:      vis,
		enf:      enf,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue implements Queue.
func (q *InProcQueue) Enqueue(_ context.Context, env *Envelope, opts EnqueueOptions) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	if env.ID == "" {
		env.ID = NewID()
	}
	if opts.IdempotencyKey != "" {
		if !q.enf.Claim(opts.IdempotencyKey) {
			return diag.New("OGON-J0014", "jobs: idempotency collision", "key in flight")
		}
	}
	now := time.Now().UTC()
	if env.EnqueuedAt.IsZero() {
		env.EnqueuedAt = now
	}
	if !opts.VisibleAt.IsZero() {
		env.VisibleAt = opts.VisibleAt
	} else if env.VisibleAt.IsZero() {
		env.VisibleAt = now
	}
	if opts.MaxAttempts > 0 {
		env.MaxAttempts = opts.MaxAttempts
	}
	if opts.TenantID != "" {
		env.TenantID = opts.TenantID
	}
	env.Priority = opts.Priority
	env.Attempts = 0
	entry := &inProcEntry{env: *env, lease: q.vis.TimeoutFor(env.Name)}
	q.ready = append(q.ready, entry)
	// keep ready sorted by (VisibleAt asc, Priority desc, EnqueuedAt asc)
	sort.SliceStable(q.ready, func(i, j int) bool {
		a, b := q.ready[i], q.ready[j]
		if !a.env.VisibleAt.Equal(b.env.VisibleAt) {
			return a.env.VisibleAt.Before(b.env.VisibleAt)
		}
		if a.env.Priority != b.env.Priority {
			return a.env.Priority > b.env.Priority
		}
		return a.env.EnqueuedAt.Before(b.env.EnqueuedAt)
	})
	q.cond.Broadcast()
	return nil
}

// Dequeue implements Queue. Blocks until an envelope is visible or
// the context is cancelled.
func (q *InProcQueue) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	// cooperative cancellation watcher
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-done:
		}
	}()

	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.closed {
			return nil, nil, ErrQueueClosed
		}
		now := time.Now()
		for i, e := range q.ready {
			if !e.env.VisibleAt.After(now) {
				// claim
				e.env.Attempts++
				e.env.LastError = ""
				e.env.Stack = ""
				e.claim = now
				q.inflight[e.env.ID] = e
				q.ready = append(q.ready[:i], q.ready[i+1:]...)
				rcpt := ReceiptMeta{ID: e.env.ID}
				out := e.env
				return &out, rcpt, nil
			}
		}
		// wait for new arrivals or context cancellation
		if ctx.Err() != nil {
			return nil, nil, ErrEmpty
		}
		// timed wait so visibility backoffs wake us
		wait := time.AfterFunc(100*time.Millisecond, q.cond.Broadcast)
		q.cond.Wait()
		wait.Stop()
	}
}

// Ack implements Queue.
func (q *InProcQueue) Ack(_ context.Context, r Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, r.EnvelopeID())
	q.cond.Broadcast()
	return nil
}

// Nack implements Queue.
func (q *InProcQueue) Nack(_ context.Context, r Receipt, requeue bool, nextVisibleAt time.Time, lastErr string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.inflight[r.EnvelopeID()]
	if !ok {
		// already acked or expired — best effort
		return nil
	}
	delete(q.inflight, r.EnvelopeID())
	e.env.LastError = lastErr
	if requeue {
		// keep Attempts as bumped on Dequeue
		e.env.VisibleAt = nextVisibleAt
		q.ready = append(q.ready, e)
		// re-sort is applied on next Dequeue scan; for in-proc, an
		// unsorted tail is fine because Dequeue scans linearly.
		q.resortLocked()
		q.cond.Broadcast()
	}
	return nil
}

func (q *InProcQueue) resortLocked() {
	sort.SliceStable(q.ready, func(i, j int) bool {
		a, b := q.ready[i], q.ready[j]
		if !a.env.VisibleAt.Equal(b.env.VisibleAt) {
			return a.env.VisibleAt.Before(b.env.VisibleAt)
		}
		if a.env.Priority != b.env.Priority {
			return a.env.Priority > b.env.Priority
		}
		return a.env.EnqueuedAt.Before(b.env.EnqueuedAt)
	})
}

// Depth implements Queue.
func (q *InProcQueue) Depth(_ context.Context) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(len(q.ready) + len(q.inflight)), nil
}

// Peek implements Queue. Returns up to n visible envelopes (not claimed).
func (q *InProcQueue) Peek(_ context.Context, n int) ([]*Envelope, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	out := make([]*Envelope, 0, n)
	for _, e := range q.ready {
		if !e.env.VisibleAt.After(now) {
			env := e.env
			out = append(out, &env)
			if n > 0 && len(out) >= n {
				break
			}
		}
	}
	return out, nil
}

// Close implements Queue. In-flight envelopes are not requeued —
// at-least-once semantics in dev mean a process crash loses them.
func (q *InProcQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	q.cond.Broadcast()
	return nil
}

// InflightCount exposes the count of unacked envelopes (for tests).
func (q *InProcQueue) InflightCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.inflight)
}

// Enforcer exposes the dedup tracker (for tests).
func (q *InProcQueue) Enforcer() *Enforcer { return q.enf }
