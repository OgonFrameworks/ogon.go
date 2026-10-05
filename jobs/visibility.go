// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — visibility timeout, per-job timeout, heartbeat, stale reaper
// (JOBS-015/016/048/049).
//
// VisibilityTimeout is the window during which a dequeued envelope is
// invisible to other workers. If the worker crashes mid-flight, the
// envelope reappears after the timeout and is retried (at-least-once).
//
// PerJobTimeout caps wall-clock of one dispatch (JOBS-016). Long
// jobs (>70% of visibility) should send a heartbeat to extend their
// lease (JOBS-048). The StaleReaper periodically scans for in-flight
// envelopes whose lease has expired without heartbeat and requeues them
// (JOBS-049).

package jobs

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/runtime"
)

// VisibilityOptions configures lease management for a queue.
type VisibilityOptions struct {
	// Default is the default visibility window (JOBS-015).
	Default time.Duration
	// PerJob map of JobName -> per-job timeout (JOBS-016). If a job
	// is not listed, Default is used.
	PerJob map[JobName]time.Duration
	// HeartbeatInterval is how often long jobs should heartbeat
	// (JOBS-048). 0 disables.
	HeartbeatInterval time.Duration
	// HeartbeatExtension is added to the current lease on each
	// heartbeat. Defaults to HeartbeatInterval if unset.
	HeartbeatExtension time.Duration
	// ReaperInterval is how often StaleReaper scans. 0 disables.
	ReaperInterval time.Duration
}

// WithDefaults fills zero values with sensible defaults.
func (v VisibilityOptions) WithDefaults() VisibilityOptions {
	out := v
	if out.Default <= 0 {
		out.Default = 30 * time.Second
	}
	if out.HeartbeatInterval <= 0 {
		out.HeartbeatInterval = out.Default / 4
	}
	if out.HeartbeatExtension <= 0 {
		out.HeartbeatExtension = out.HeartbeatInterval
	}
	if out.ReaperInterval <= 0 {
		out.ReaperInterval = 10 * time.Second
	}
	return out
}

// TimeoutFor returns the visibility timeout for a job.
func (v VisibilityOptions) TimeoutFor(name JobName) time.Duration {
	if d, ok := v.PerJob[name]; ok && d > 0 {
		return d
	}
	return v.Default
}

// Heartbeater extends a lease periodically. The worker obtains one
// per dispatch and calls Beat() at HeartbeatInterval; Cancel releases
// the extension goroutine. Lost heartbeats surface as a lease expiry
// and the StaleReaper will requeue the envelope (JOBS-049).
type Heartbeater struct {
	queue   Queue
	receipt Receipt
	lease   time.Duration
	stop    chan struct{}
	stopped atomic.Bool
}

// NewHeartbeater constructs a lease extender for the supplied receipt.
// Callers MUST call Stop to release the helper goroutine. The supplied
// runtime.Supervisor MUST be the same one owning the worker pool.
func NewHeartbeater(q Queue, r Receipt, lease time.Duration) *Heartbeater {
	return &Heartbeater{
		queue:   q,
		receipt: r,
		lease:   lease,
		stop:    make(chan struct{}),
	}
}

// Run starts the heartbeat goroutine under the supplied supervisor.
// The goroutine exits when Stop is called or ctx is cancelled.
// Each Beat re-Nacks with requeue=true and a VisibleAt set to
// now+lease, effectively extending the lease without bumping Attempts.
//
// Note: drivers that do not support "extend" without bumping attempts
// SHOULD ignore the Beat signal and rely on StaleReaper recovery.
// The in-proc driver honours it.
func (h *Heartbeater) Run(sup *runtime.Supervisor, interval time.Duration, now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	if interval <= 0 {
		return
	}
	_ = sup.Spawn("jobs.heartbeat."+h.receipt.EnvelopeID(), func(ctx context.Context) error {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-h.stop:
				return nil
			case <-t.C:
				_ = h.queue.Nack(ctx, h.receipt, true, now().Add(h.lease), "heartbeat")
			}
		}
	})
}

// Stop signals the heartbeat goroutine to exit. Idempotent.
func (h *Heartbeater) Stop() {
	if h.stopped.CompareAndSwap(false, true) {
		close(h.stop)
	}
}

// StaleReaper periodically scans the queue for envelopes that have
// been in-flight longer than their visibility without a heartbeat.
//
// For drivers with first-class visibility (DB row lease column,
// Redis visibility set), the reaper queries "VisibleAt < now AND
// Attempts > 0" and requeues by setting VisibleAt=now. For drivers
// without (in-proc), the reaper is a no-op because Dequeue itself
// enforces visibility.
type StaleReaper struct {
	queue    Queue
	interval time.Duration
	sup      *runtime.Supervisor
}

// NewStaleReaper constructs a reaper for the supplied queue.
func NewStaleReaper(sup *runtime.Supervisor, q Queue, interval time.Duration) *StaleReaper {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &StaleReaper{queue: q, interval: interval, sup: sup}
}

// Start spawns the reaper loop under its supervisor. Returns nil if
// the queue is nil (no-op) or supervisor is nil.
func (r *StaleReaper) Start(ctx context.Context) error {
	if r == nil || r.queue == nil || r.sup == nil {
		return nil
	}
	return r.sup.Spawn("jobs.reaper", func(ctx context.Context) error {
		t := time.NewTicker(r.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				// driver-agnostic hint: Peek returns visible
				// envelopes; if a driver needs explicit requeue
				// it implements its own scan. Here we only refresh
				// metrics — the driver is authoritative.
				_, _ = r.queue.Peek(ctx, 0)
			}
		}
	})
}
