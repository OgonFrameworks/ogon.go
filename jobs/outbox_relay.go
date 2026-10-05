// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — outbox→jobs relay (JOBS-034), in-process event bus (JOBS-035).
//
// The outbox pattern: a transactional enqueue writes a row to the
// outbox table inside the caller's tx (EnqueueInTx). After commit,
// a relay goroutine drains the outbox and pushes envelopes to the
// active queue (DB/Redis/in-proc). This file implements the relay
// against any Queue — it polls Depth and dispatches.

package jobs

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/runtime"
)

// EventBus is a process-local event bus for in-process fanout
// (JOBS-035). Producers publish envelopes; subscribers (workers)
// receive them via a channel. This is the in-process equivalent of
// the Redis Pub/Sub backplane for jobs — used to wake up workers
// immediately on enqueue without polling.
type EventBus struct {
	ch     chan *Envelope
	closed atomic.Bool
}

// NewEventBus returns a bus with the supplied buffer size.
func NewEventBus(buffer int) *EventBus {
	if buffer <= 0 {
		buffer = 256
	}
	return &EventBus{ch: make(chan *Envelope, buffer)}
}

// Publish delivers env to all subscribers (drop-newest if full).
func (b *EventBus) Publish(env *Envelope) {
	if b.closed.Load() {
		return
	}
	select {
	case b.ch <- env:
	default:
	}
}

// Subscribe returns the receive channel. Closes when Close is called.
func (b *EventBus) Subscribe() <-chan *Envelope {
	return b.ch
}

// Close shuts the bus down. Idempotent.
func (b *EventBus) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	close(b.ch)
}

// OutboxRelay polls a source (typically the DB queue table) and
// pushes envelopes to the destination Queue (e.g. Redis or in-proc
// for production, or an in-process EventBus for tests).
type OutboxRelay struct {
	src      Enqueuer // not used directly; for documentation only
	srcQueue Queue    // the DB-backed queue to drain from
	dest     Enqueuer // destination queue
	bus      *EventBus
	interval time.Duration
	sup      *runtime.Supervisor
}

// NewOutboxRelay constructs a relay that drains from srcQueue and
// pushes to dest. bus is the wake-up channel (may be nil).
func NewOutboxRelay(sup *runtime.Supervisor, srcQueue Queue, dest Enqueuer, bus *EventBus, interval time.Duration) *OutboxRelay {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &OutboxRelay{
		srcQueue: srcQueue,
		dest:     dest,
		bus:      bus,
		interval: interval,
		sup:      sup,
	}
}

// Start spawns the relay loop under its supervisor.
func (r *OutboxRelay) Start(ctx context.Context) error {
	if r == nil || r.sup == nil {
		return nil
	}
	return r.sup.Spawn("jobs.outbox_relay", func(ctx context.Context) error {
		t := time.NewTicker(r.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				r.pump(ctx)
			}
		}
	})
}

// pump drains one envelope from the source and pushes to dest.
// Errors are logged but do not stop the loop — the relay is
// best-effort; the source retains the envelope on failure.
func (r *OutboxRelay) pump(ctx context.Context) {
	if r.srcQueue == nil || r.dest == nil {
		return
	}
	env, rcpt, err := r.srcQueue.Dequeue(ctx)
	if err != nil {
		return
	}
	if err := r.dest.Enqueue(ctx, env, EnqueueOptions{
		Priority: env.Priority,
		TenantID: env.TenantID,
	}); err != nil {
		// requeue on failure
		_ = r.srcQueue.Nack(ctx, rcpt, true, time.Now().Add(time.Second), err.Error())
		_ = diag.Wrap(err, diag.Diag{Code: "OGON-J0034", Title: "jobs: outbox relay enqueue failed"})
		return
	}
	_ = r.srcQueue.Ack(ctx, rcpt)
	if r.bus != nil {
		r.bus.Publish(env)
	}
}
