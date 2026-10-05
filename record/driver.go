// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Driver abstraction — the single seam between OgonRecord and the two
// supported database engines (Postgres via pgx/v5, SQLite via modernc.org/sqlite
// through database/sql). The Query/Transaction/Raw subsystems talk only to
// this interface; engine-specific behaviour is opt-in via Dialect().

package record

import (
	"context"
)

// Dialect names the engine family.
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// Row is the minimal scanning surface QueryRow returns. Both pgx and
// database/sql satisfy it.
type Row interface {
	Scan(dest ...any) error
}

// Rows is the streaming surface Query returns. The iter-based Query[T].Iter
// uses Rows.Next + Rows.Scan under the hood; closing happens via the
// iterator's Stop function so callers always release the rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Columns() ([]string, error)
	Err() error
}

// Result is the post-Exec outcome.
type Result interface {
	LastInsertId() (int64, error)
	RowsAffected() (int64, error)
}

// TxOptions mirrors database/sql.TxOptions; isolation levels are the only
// knob the runtime layer needs (read-only hint, isolation level).
type TxOptions struct {
	Isolation IsolationLevel
	ReadOnly  bool
}

// IsolationLevel lists the levels the transaction helper recognises.
type IsolationLevel int

const (
	LevelDefault IsolationLevel = iota
	LevelReadUncommitted
	LevelReadCommitted
	LevelRepeatableRead
	LevelSerializable
)

// Driver is the contract both pgx and sqlite implementations satisfy.
// Implementations MUST be safe for concurrent use.
type Driver interface {
	// Dialect reports the engine family for SQL emission decisions.
	Dialect() Dialect
	// Exec runs an INSERT/UPDATE/DELETE/DDL statement.
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	// Query runs a SELECT and returns streaming rows.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	// QueryRow runs a SELECT returning at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) Row
	// BeginTx starts a transaction with the given options.
	BeginTx(ctx context.Context, opts TxOptions) (TxDriver, error)
	// Close releases the underlying pool/connection.
	Close() error
}

// TxDriver is the transactional Driver surface. Methods mirror Driver.
type TxDriver interface {
	Driver
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	// Savepoint creates a nested savepoint. Returning a nil driver-backed
	// implementation when the engine does not support savepoints is an error.
	Savepoint(ctx context.Context, name string) (Savepoint, error)
}

// Savepoint is a nested transaction marker.
type Savepoint interface {
	Release(ctx context.Context) error
	Rollback(ctx context.Context) error
}
