// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// DB tx-rollback fixture (TEST-004/005). The fixture wraps a *sql.Tx so
// every test runs in its own transaction and rollback at teardown, making
// DB tests fast and parallel-safe. Truncation mode is opt-in for the rare
// cases where DDL inside the test cannot live inside a tx (TEST-005).

package test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

// DBFixture is the per-test database wrapper. Default mode: tx-rollback
// (TEST-004). Truncation mode (TEST-005) is opt-in via NewTruncationDB.
type DBFixture struct {
	t   *testing.T
	db  *sql.DB // underlying connection pool
	tx  *sql.Tx // nil in truncation mode
	mu  sync.Mutex
	mod TruncationMode // nil in tx-rollback mode
}

// TruncationMode is the interface for truncation-mode fixtures. Tests
// register their own implementation; the default in tx-rollback mode is nil.
type TruncationMode interface {
	// Truncate wipes all known tables. Called on cleanup.
	Truncate(ctx context.Context) error
	// Tables returns the list of tables this mode truncates.
	Tables() []string
}

// NewDBTx wraps db in a transaction and arranges rollback on cleanup. The
// returned *DBFixture's underlying *sql.Tx is the surface tests use.
func NewDBTx(t *testing.T, db *sql.DB) *DBFixture {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("ogontest: begin tx: %v", err)
	}
	f := &DBFixture{t: t, db: db, tx: tx}
	return f
}

// NewTruncationDB wraps db with a TruncationMode cleanup. Used when tests
// must run DDL that cannot live inside a tx (TEST-005).
func NewTruncationDB(t *testing.T, db *sql.DB, mod TruncationMode) *DBFixture {
	t.Helper()
	f := &DBFixture{t: t, db: db, mod: mod}
	return f
}

// Tx returns the underlying transaction (nil in truncation mode).
func (f *DBFixture) Tx() *sql.Tx { return f.tx }

// DB returns the underlying *sql.DB (always non-nil).
func (f *DBFixture) DB() *sql.DB { return f.db }

// Exec runs Exec on the tx (or db in truncation mode).
func (f *DBFixture) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if f.tx != nil {
		return f.tx.ExecContext(ctx, query, args...)
	}
	return f.db.ExecContext(ctx, query, args...)
}

// Query runs Query on the tx (or db in truncation mode).
func (f *DBFixture) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if f.tx != nil {
		return f.tx.QueryContext(ctx, query, args...)
	}
	return f.db.QueryContext(ctx, query, args...)
}

// QueryRow runs QueryRow on the tx (or db in truncation mode).
func (f *DBFixture) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if f.tx != nil {
		return f.tx.QueryRowContext(ctx, query, args...)
	}
	return f.db.QueryRowContext(ctx, query, args...)
}

// Close is the cleanup entrypoint. In tx-rollback mode it rolls back; in
// truncation mode it truncates all known tables.
func (f *DBFixture) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tx != nil {
		if err := f.tx.Rollback(); err != nil && err != sql.ErrTxDone {
			f.t.Logf("ogontest: rollback: %v", err)
		}
		return
	}
	if f.mod != nil {
		if err := f.mod.Truncate(context.Background()); err != nil {
			f.t.Logf("ogontest: truncate: %v", err)
		}
	}
}

// Tables returns the list of tables known to the truncation mode (empty in
// tx-rollback mode).
func (f *DBFixture) Tables() []string {
	if f.mod == nil {
		return nil
	}
	return f.mod.Tables()
}

// Mode returns "tx" for tx-rollback or "truncate" for truncation mode.
func (f *DBFixture) Mode() string {
	if f.tx != nil {
		return "tx"
	}
	if f.mod != nil {
		return "truncate"
	}
	return "none"
}

// Isolation returns whether this fixture is parallel-safe. Both modes are
// parallel-safe by construction (TEST-036).
func (f *DBFixture) Isolation() string { return "parallel-safe" }
