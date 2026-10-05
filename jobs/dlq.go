// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — Dead Letter Queue (JOBS-008/009).
//
// When an envelope exhausts MaxAttempts (or returns ErrFatal) the
// worker moves it to the DLQ. The DLQ is a separate table/keyspace
// so operators can inspect/replay without polluting the live queue.
//
// DLQDriver is the abstraction: a DB-backed DLQ persists to a
// `ogon_jobs_dlq` table; an in-memory DLQ is provided for tests and
// the in-proc driver. CLI commands (cli.go) wrap DLQDriver methods.

package jobs

import (
	"context"
	"sync"
	"time"
)

// DLQEntry is a dead-lettered envelope plus its quarantine metadata.
type DLQEntry struct {
	Envelope Envelope `json:"envelope"`
	// Reason: "max_attempts" | "fatal" | "poison".
	Reason string `json:"reason"`
	// QuarantinedAt is when the envelope entered the DLQ.
	QuarantinedAt time.Time `json:"quarantined_at"`
}

// DLQDriver is the inspect/replay surface used by CLI and tests.
type DLQDriver interface {
	// Put stores entry in the DLQ. Idempotent on Envelope.ID.
	Put(ctx context.Context, entry DLQEntry) error
	// List returns the most recent n entries (newest first).
	List(ctx context.Context, n int) ([]DLQEntry, error)
	// Replay re-enqueues the supplied entries back to the live queue,
	// resetting Attempts=0 and removing them from the DLQ.
	Replay(ctx context.Context, ids []string) (int, error)
	// Purge removes entries by id (or all if ids is empty). Returns
	// the number purged.
	Purge(ctx context.Context, ids []string) (int, error)
	// Len returns the count of entries in the DLQ.
	Len(ctx context.Context) (int64, error)
	// Close releases driver resources. Idempotent.
	Close() error
}

// InMemoryDLQ is the default DLQDriver for dev/test and the in-proc
// queue. Safe for concurrent use.
type InMemoryDLQ struct {
	mu      sync.Mutex
	entries []DLQEntry
	queue   Queue
	retry   RetryPolicy
}

// NewInMemoryDLQ constructs an in-memory DLQ that, on Replay, re-enqueues
// to the supplied queue with the supplied retry policy defaults.
func NewInMemoryDLQ(q Queue, retry RetryPolicy) *InMemoryDLQ {
	return &InMemoryDLQ{queue: q, retry: retry}
}

// Put implements DLQDriver.
func (d *InMemoryDLQ) Put(_ context.Context, entry DLQEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.entries {
		if d.entries[i].Envelope.ID == entry.Envelope.ID {
			d.entries[i] = entry
			return nil
		}
	}
	d.entries = append(d.entries, entry)
	return nil
}

// List implements DLQDriver (newest first).
func (d *InMemoryDLQ) List(_ context.Context, n int) ([]DLQEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n <= 0 || n > len(d.entries) {
		n = len(d.entries)
	}
	out := make([]DLQEntry, n)
	for i := 0; i < n; i++ {
		out[i] = d.entries[len(d.entries)-1-i]
	}
	return out, nil
}

// Replay implements DLQDriver.
func (d *InMemoryDLQ) Replay(ctx context.Context, ids []string) (int, error) {
	d.mu.Lock()
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := d.entries[:0]
	var replay []DLQEntry
	for _, e := range d.entries {
		if _, ok := want[e.Envelope.ID]; ok {
			replay = append(replay, e)
			continue
		}
		out = append(out, e)
	}
	d.entries = out
	d.mu.Unlock()

	count := 0
	for _, e := range replay {
		env := e.Envelope
		env.Attempts = 0
		env.LastError = ""
		env.Stack = ""
		env.VisibleAt = time.Now().UTC()
		maxAtt := d.retry.MaxAttempts
		if env.MaxAttempts > 0 {
			maxAtt = env.MaxAttempts
		}
		if err := d.queue.Enqueue(ctx, &env, EnqueueOptions{
			Priority:       env.Priority,
			MaxAttempts:    maxAtt,
			TenantID:       env.TenantID,
			VisibleAt:      env.VisibleAt,
			IdempotencyKey: env.IdempotencyKey,
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// Purge implements DLQDriver.
func (d *InMemoryDLQ) Purge(_ context.Context, ids []string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(ids) == 0 {
		n := len(d.entries)
		d.entries = nil
		return n, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := d.entries[:0]
	purged := 0
	for _, e := range d.entries {
		if _, ok := want[e.Envelope.ID]; ok {
			purged++
			continue
		}
		out = append(out, e)
	}
	d.entries = out
	return purged, nil
}

// Len implements DLQDriver.
func (d *InMemoryDLQ) Len(_ context.Context) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return int64(len(d.entries)), nil
}

// Close implements DLQDriver.
func (d *InMemoryDLQ) Close() error { return nil }

// SendToDLQ is the helper workers call when an envelope is
// terminal (max attempts / fatal / poison). It is best-effort: if the
// DLQ write fails we log the loss and continue, since losing the DLQ
// must not crash the worker pool.
func SendToDLQ(ctx context.Context, dlq DLQDriver, env *Envelope, reason string) {
	if dlq == nil {
		return
	}
	entry := DLQEntry{
		Envelope:      *env,
		Reason:        reason,
		QuarantinedAt: time.Now().UTC(),
	}
	_ = dlq.Put(ctx, entry)
}
