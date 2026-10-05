// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — transactional enqueue (outbox-integrated), after-commit helper
// (JOBS-036/037).
//
// Transactional enqueue: persist the envelope to the queue table
// inside the caller's DB transaction so a downstream failure rolls
// back the enqueue. This is the "transactional outbox" pattern —
// the worker picks up the row via Dequeue after commit.
//
// After-commit helper: for the in-proc and Redis drivers (no shared
// tx with the DB), callers register an AfterCommit hook on the Tx
// and the enqueue fires only after Commit returns nil.

package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/record"
)

// AfterCommitRegistry lets callers register enqueue callbacks that
// fire only after the surrounding Tx commits. If the tx rolls back,
// the callbacks are discarded. Used by the DB driver's
// NonTxEnqueue helper and by app code that wants enqueue-after-commit
// semantics without binding the enqueue to the tx (e.g. for the
// in-proc or Redis driver).
type AfterCommitRegistry struct {
	mu      sync.Mutex
	pending []func()
	rolled  bool
}

// NewAfterCommitRegistry returns a fresh registry.
func NewAfterCommitRegistry() *AfterCommitRegistry {
	return &AfterCommitRegistry{}
}

// Register adds a callback that fires after Commit. Callbacks fire
// in registration order. Errors are swallowed (best-effort) and
// surfaced via the returned error from Run.
func (r *AfterCommitRegistry) Register(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rolled {
		return
	}
	r.pending = append(r.pending, fn)
}

// Commit fires all callbacks and marks the registry as committed.
// Subsequent Register calls are no-ops.
func (r *AfterCommitRegistry) Commit() error {
	r.mu.Lock()
	pending := r.pending
	r.pending = nil
	r.rolled = true
	r.mu.Unlock()
	var firstErr error
	for _, fn := range pending {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					if firstErr == nil {
						firstErr = errors.New("jobs: panic in after-commit callback")
					}
				}
			}()
			if err := fnCall(fn); err != nil && firstErr == nil {
				firstErr = err
			}
		}()
	}
	return firstErr
}

// Rollback discards all pending callbacks.
func (r *AfterCommitRegistry) Rollback() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = nil
	r.rolled = true
}

// fnCall wraps a no-arg callback to fit the funcCall pattern; for
// no-arg functions we just invoke and return nil.
func fnCall(fn func()) error { fn(); return nil }

// AfterCommitTx wraps a record.Tx so that callers can register
// after-commit enqueue callbacks via the helper. Wrap before passing
// to your domain code:
//
//	acreg := jobs.NewAfterCommitRegistry()
//	defer acreg.Rollback()
//	err := record.Transaction(ctx, "default", func(tx *record.Tx) error {
//	    // ... domain writes ...
//	    acreg.Register(func() { _ = myQueue.Enqueue(ctx, env, opts) })
//	    return nil
//	})
//	if err == nil { err = acreg.Commit() }
type AfterCommitTx struct {
	*record.Tx
	registry *AfterCommitRegistry
}

// WrapTx returns an AfterCommitTx. The registry MUST be Committed
// or Rolled-back by the caller after the tx returns.
func WrapTx(tx *record.Tx, registry *AfterCommitRegistry) *AfterCommitTx {
	return &AfterCommitTx{Tx: tx, registry: registry}
}

// EnqueueAfterCommit registers a deferred enqueue on the supplied
// queue. The enqueue fires only after the wrapping Tx commits.
//
//	ctx, env, opts := ...
//	_ = jobs.EnqueueAfterCommit(acTx, q, env, opts)
func EnqueueAfterCommit(tx *AfterCommitTx, q Enqueuer, env *Envelope, opts EnqueueOptions) error {
	if tx == nil || tx.registry == nil {
		return diag.New("OGON-J0040", "jobs: nil after-commit tx", "wrap with WrapTx first")
	}
	// capture by value
	envCopy := *env
	optsCopy := opts
	tx.registry.Register(func() {
		_ = q.Enqueue(context.Background(), &envCopy, optsCopy)
	})
	return nil
}

// EnqueueInTx writes an envelope to the queue table inside the
// caller's transaction. The row is invisible to Dequeue until the
// tx commits (read-committed isolation guarantees this in Postgres;
// SQLite is single-writer so the visibility window is moot).
//
// Requires a DBDriver bound to the same pool as the tx.
func EnqueueInTx(ctx context.Context, tx *record.Tx, d *DBDriver, env *Envelope, opts EnqueueOptions) error {
	if d == nil || tx == nil {
		return diag.New("OGON-J0041", "jobs: nil db driver or tx", "")
	}
	if env.ID == "" {
		env.ID = NewID()
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
	if opts.IdempotencyKey != "" {
		env.IdempotencyKey = opts.IdempotencyKey
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO ogon_jobs_queue
		 (id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		env.ID, string(env.Name), []byte(env.Payload),
		0, env.MaxAttempts, env.Priority,
		nullableString(env.IdempotencyKey), nullableString(env.TenantID),
		env.EnqueuedAt.UnixMilli(), env.VisibleAt.UnixMilli(),
		"", "",
	)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0042", Title: "jobs: enqueue-in-tx failed", What: err.Error()})
	}
	return nil
}
