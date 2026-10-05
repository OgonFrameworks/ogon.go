// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — Queue interface (JOBS-002..004).
//
// Every driver (DB, Redis, in-proc) implements Queue. The worker pool
// and cron scheduler depend only on this surface, so swapping drivers
// is a configuration change rather than a code change.

package jobs

import (
	"context"
	"errors"
	"time"
)

// Queue is the persistence + dispatch surface a driver implements.
// Implementations MUST be safe for concurrent use by many goroutines
// (one process, N workers, M enqueue callers).
//
// Semantics: at-least-once delivery. After Dequeue returns an
// envelope, the worker MUST Ack or Nack within the queue's
// visibility timeout or the envelope becomes eligible for redelivery
// (JOBS-015).
type Queue interface {
	// Enqueue inserts an envelope. If opts.IdempotencyKey collides
	// within the dedup window, Enqueue returns ErrAlreadyExists and
	// makes no new row (JOBS-013).
	// Implementations MUST set Envelope.ID, EnqueuedAt, VisibleAt
	// (to opts.VisibleAt or now) and Attempts=0 when persisting.
	Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error

	// Dequeue claims the next visible envelope, atomically bumping
	// Attempts and extending VisibleAt to now+visibility. The
	// returned Receipt MUST be passed to Ack/Nack. Returns ErrEmpty
	// when no envelope is ready.
	Dequeue(ctx context.Context) (*Envelope, Receipt, error)

	// Ack marks env as completed; the envelope is permanently removed
	// from the queue (or moved to a "done" log if the driver keeps one).
	Ack(ctx context.Context, r Receipt) error

	// Nack returns the envelope to the queue with the supplied next
	// visible-at. If requeue=false the envelope is sent straight to
	// the DLQ (JOBS-008).
	Nack(ctx context.Context, r Receipt, requeue bool, nextVisibleAt time.Time, lastErr string) error

	// Depth returns the count of currently-visible envelopes plus
	// in-flight (unacked) ones. Best-effort; used by metrics
	// (JOBS-020) and `ogon jobs list` (JOBS-027).
	Depth(ctx context.Context) (int64, error)

	// Peek retrieves the next N visible envelopes WITHOUT claiming
	// them. Used by `ogon jobs list` and DLQ inspection. Implementations
	// may cap N.
	Peek(ctx context.Context, n int) ([]*Envelope, error)

	// Close releases driver resources. Idempotent.
	Close() error
}

// Receipt is the driver-issued handle returned by Dequeue. The worker
// passes it back to Ack/Nack. Concrete drivers type-assert or marshal
// this to their primary key shape (e.g. int64 row id, Redis stream id).
type Receipt interface {
	// EnvelopeID returns the envelope id this receipt refers to.
	EnvelopeID() string
}

// ReceiptMeta is a generic in-memory Receipt for drivers that don't
// need anything beyond the envelope id (in-proc, sqlite single-row).
type ReceiptMeta struct {
	ID    string
	Token string // optional driver token (e.g. Redis stream id)
}

// EnvelopeID implements Receipt.
func (r ReceiptMeta) EnvelopeID() string { return r.ID }

// ErrQueueClosed is returned by operations on a closed Queue.
var ErrQueueClosed = errors.New("jobs: queue closed")

// Enqueuer is the minimal subset of Queue used by callers that only
// enqueue (e.g. the tx-outbox helper). Defined so the tx helper can
// accept either a real Queue or a stub.
type Enqueuer interface {
	Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error
}

// EnqueueFunc adapts a plain function to Enqueuer. Useful in tests.
type EnqueueFunc func(ctx context.Context, env *Envelope, opts EnqueueOptions) error

// Enqueue calls f.
func (f EnqueueFunc) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	return f(ctx, env, opts)
}

// PeekAt returns the supplied envelopes filtered to those visible now.
// Driver-agnostic helper for tests that simulate Peek.
func PeekAt(now time.Time, envs []*Envelope, n int) []*Envelope {
	if n <= 0 {
		return nil
	}
	out := make([]*Envelope, 0, n)
	for _, e := range envs {
		if len(out) >= n {
			break
		}
		if !e.VisibleAt.After(now) {
			out = append(out, e)
		}
	}
	return out
}
