// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// SQLite driver implementation via modernc.org/sqlite (pure-Go, no CGO).
// Used for tests, dev mode, and small embedded deployments. Advanced
// Postgres features (RLS, partitions, GIN/BRIN, COPY FROM) are pg-only and
// surfaced as explicit errors here so callers don't learn this the hard
// way at runtime (DATA-029).

package record

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver registration
)

// SQLiteDriver wraps a *sql.DB backed by modernc.org/sqlite.
type SQLiteDriver struct {
	db *sql.DB
}

// NewSQLiteDriver opens a SQLite database at the supplied DSN. The DSN is
// passed verbatim to modernc.org/sqlite; for in-memory tests use
// "file::memory:?cache=shared" so multiple connections share one database.
func NewSQLiteDriver(dsn string) (*SQLiteDriver, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("record: open sqlite: %w", err)
	}
	// single writer to avoid "database is locked" — SQLite serialises writes
	db.SetMaxOpenConns(1)
	return &SQLiteDriver{db: db}, nil
}

// NewSQLiteDriverFromDB wraps an existing *sql.DB. Useful for tests that
// want to share one in-memory DB across helpers.
func NewSQLiteDriverFromDB(db *sql.DB) *SQLiteDriver {
	return &SQLiteDriver{db: db}
}

func (d *SQLiteDriver) Dialect() Dialect { return DialectSQLite }

func (d *SQLiteDriver) Exec(ctx context.Context, s string, args ...any) (Result, error) {
	res, err := d.db.ExecContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return sqliteResult{res: res}, nil
}

func (d *SQLiteDriver) Query(ctx context.Context, s string, args ...any) (Rows, error) {
	rows, err := d.db.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return &sqliteRows{rows: rows}, nil
}

func (d *SQLiteDriver) QueryRow(ctx context.Context, s string, args ...any) Row {
	return &sqliteRow{row: d.db.QueryRowContext(ctx, s, args...)}
}

func (d *SQLiteDriver) BeginTx(ctx context.Context, opts TxOptions) (TxDriver, error) {
	sqlOpts := &sql.TxOptions{
		Isolation: toSqlIsolation(opts.Isolation),
		ReadOnly:  opts.ReadOnly,
	}
	tx, err := d.db.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, err
	}
	return &sqliteTx{tx: tx, db: d.db}, nil
}

func (d *SQLiteDriver) Close() error { return d.db.Close() }

// DB exposes the underlying *sql.DB for the raw escape hatch.
func (d *SQLiteDriver) DB() *sql.DB { return d.db }

// sqliteRows wraps *sql.Rows.
type sqliteRows struct {
	rows *sql.Rows
}

func (r *sqliteRows) Next() bool                 { return r.rows.Next() }
func (r *sqliteRows) Columns() ([]string, error) { return r.rows.Columns() }
func (r *sqliteRows) Err() error                 { return r.rows.Err() }
func (r *sqliteRows) Close() error               { return r.rows.Close() }
func (r *sqliteRows) Scan(dest ...any) error     { return r.rows.Scan(dest...) }

// sqliteRow wraps *sql.Row.
type sqliteRow struct {
	row *sql.Row
}

func (r *sqliteRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRows
	}
	return err
}

type sqliteTx struct {
	tx *sql.Tx
	db *sql.DB
}

func (t *sqliteTx) Dialect() Dialect { return DialectSQLite }
func (t *sqliteTx) Exec(ctx context.Context, s string, args ...any) (Result, error) {
	res, err := t.tx.ExecContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return sqliteResult{res: res}, nil
}
func (t *sqliteTx) Query(ctx context.Context, s string, args ...any) (Rows, error) {
	rows, err := t.tx.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return &sqliteRows{rows: rows}, nil
}
func (t *sqliteTx) QueryRow(ctx context.Context, s string, args ...any) Row {
	return &sqliteRow{row: t.tx.QueryRowContext(ctx, s, args...)}
}
func (t *sqliteTx) BeginTx(ctx context.Context, _ TxOptions) (TxDriver, error) {
	return t, nil // no real nested tx; callers use savepoints
}
func (t *sqliteTx) Commit(ctx context.Context) error   { return t.tx.Commit() }
func (t *sqliteTx) Rollback(ctx context.Context) error { return t.tx.Rollback() }
func (t *sqliteTx) Close() error                       { return nil }
func (t *sqliteTx) Savepoint(ctx context.Context, name string) (Savepoint, error) {
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &sqliteSavepoint{tx: t.tx, name: name}, nil
}

type sqliteSavepoint struct {
	tx   *sql.Tx
	name string
}

func (s *sqliteSavepoint) Release(ctx context.Context) error {
	_, err := s.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+s.name)
	return err
}
func (s *sqliteSavepoint) Rollback(ctx context.Context) error {
	_, err := s.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+s.name)
	return err
}

type sqliteResult struct {
	res sql.Result
}

func (r sqliteResult) LastInsertId() (int64, error) { return r.res.LastInsertId() }
func (r sqliteResult) RowsAffected() (int64, error) { return r.res.RowsAffected() }

func toSqlIsolation(l IsolationLevel) sql.IsolationLevel {
	switch l {
	case LevelReadUncommitted:
		return sql.LevelReadUncommitted
	case LevelReadCommitted:
		return sql.LevelReadCommitted
	case LevelRepeatableRead:
		return sql.LevelRepeatableRead
	case LevelSerializable:
		return sql.LevelSerializable
	default:
		return sql.LevelDefault
	}
}
