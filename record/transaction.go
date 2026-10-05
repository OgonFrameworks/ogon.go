// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Transaction helper. record.Transaction(ctx, fn) runs fn inside a tx,
// commits on nil return, rolls back on non-nil. Supports savepoints
// (Tx.Savepoint), isolation level selection, and optimistic-concurrency
// retry on serialisation failure (DATA-032..034, DATA-096).
//
// Server processes never auto-migrate (DATA-009); migrations run via the
// dedicated migration_runner.go path, not via Transaction.

package record

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Tx is the user-facing transaction handle. It embeds the underlying
// TxDriver for direct Exec/Query access.
type Tx struct {
	TxDriver
	name    string
	log     *slog.Logger
	saveIdx int
}

// Transaction runs fn inside a transaction on the named pool. The default
// pool ("") is used when name is empty. On nil error the tx is committed;
// on non-nil error the tx is rolled back. The context passed to fn is
// derived from the caller's ctx.
//
// Serialisation failures are retried up to MaxRetries times (DATA-096).
// Set MaxRetries=0 to disable retry — useful for explicit pessimistic flows.
func Transaction(ctx context.Context, poolName string, fn func(*Tx) error) error {
	return TransactionWith(ctx, poolName, TxOptions{Isolation: LevelReadCommitted}, fn)
}

// MaxRetries caps serialisation-failure retries. Override per-call via
// TxRetryBudget(ctx, n) in the future; the global is enough for now.
var MaxRetries = 3

// retryBackoff is the linear backoff between serialisation retries.
var retryBackoff = 5 * time.Millisecond

// TransactionWith lets the caller specify isolation/read-only.
func TransactionWith(ctx context.Context, poolName string, opts TxOptions, fn func(*Tx) error) error {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return diag.New("OGON-D0001", "no such pool", "pool "+poolName+" not registered")
	}

	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		td, err := d.BeginTx(ctx, opts)
		if err != nil {
			recordTx(poolName, false)
			return diag.Wrap(err, diag.Diag{Code: "OGON-D0010", Title: "begin tx failed"})
		}
		tx := &Tx{TxDriver: td, name: poolName}

		fnerr := fn(tx)
		if fnerr == nil {
			if cerr := tx.Commit(ctx); cerr != nil {
				if isSerialization(cerr) && attempt < MaxRetries {
					lastErr = cerr
					recordTx(poolName, false)
					time.Sleep(retryBackoff)
					continue
				}
				recordTx(poolName, false)
				return diag.Wrap(cerr, diag.Diag{Code: "OGON-D0011", Title: "commit failed"})
			}
			recordTx(poolName, true)
			return nil
		}
		// rollback unconditionally — pgx/sqlite return ErrTxDone if already
		// committed, but Rollback is a no-op then.
		_ = tx.Rollback(ctx)
		if isSerialization(fnerr) && attempt < MaxRetries {
			lastErr = fnerr
			time.Sleep(retryBackoff)
			continue
		}
		recordTx(poolName, false)
		return fnerr
	}
	return diag.Wrap(lastErr, diag.Diag{
		Code:  "OGON-D0012",
		Title: "tx retry budget exhausted",
		What:  fmt.Sprintf("retried %d times, last error: %v", MaxRetries, lastErr),
	})
}

// Savepoint runs fn inside a nested savepoint. Rolls back to the
// savepoint on non-nil return; releases on nil.
func (t *Tx) Savepoint(ctx context.Context, name string, fn func(*Tx) error) error {
	if name == "" {
		t.saveIdx++
		name = fmt.Sprintf("sp_%d", t.saveIdx)
	}
	sp, err := t.TxDriver.Savepoint(ctx, name)
	if err != nil {
		return err
	}
	fnerr := fn(t)
	if fnerr == nil {
		if rerr := sp.Release(ctx); rerr != nil {
			return rerr
		}
		return nil
	}
	// Roll back to savepoint, then release (so it doesn't linger).
	_ = sp.Rollback(ctx)
	_ = sp.Release(ctx)
	return fnerr
}

// isSerialization reports whether err is a serialization/deadlock failure
// worth retrying. pgx returns pgconn.PgError with code 40001 or 40P01;
// SQLite returns "database is locked" (modernc) — both are non-fatal here.
func isSerialization(err error) bool {
	if err == nil {
		return false
	}
	// conservative string match so we don't import pgconn here (avoid cycle)
	msg := err.Error()
	switch {
	case contains(msg, "could not serialize access"),
		contains(msg, "deadlock detected"),
		contains(msg, "database is locked"),
		contains(msg, "database table is locked"):
		return true
	}
	return false
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

var _ = errors.New // keep errors import for future use
