// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pgx/v5 driver implementation. Wraps pgxpool.Pool behind the record.Driver
// interface so the rest of the subsystem stays engine-agnostic. Slow-query
// logging and otel span emission with redacted statements happen in the
// pool/metrics wrapper, not here — this file is intentionally thin.

package record

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxDriver wraps a *pgxpool.Pool.
type PgxDriver struct {
	pool *pgxpool.Pool
}

// NewPgxDriver constructs a driver from an existing pool. Ownership of the
// pool transfers to the driver; Close() will close it.
func NewPgxDriver(pool *pgxpool.Pool) *PgxDriver {
	return &PgxDriver{pool: pool}
}

// NewPgxDriverFromDSN parses the DSN and builds a default pool. Callers
// needing finer control over pool sizing should use NewPgxDriver with a
// pre-built *pgxpool.Pool.
func NewPgxDriverFromDSN(ctx context.Context, dsn string) (*PgxDriver, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("record: parse pgx dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("record: new pgx pool: %w", err)
	}
	return &PgxDriver{pool: pool}, nil
}

func (d *PgxDriver) Dialect() Dialect { return DialectPostgres }

func (d *PgxDriver) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	tag, err := d.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxResult{tag: tag}, nil
}

func (d *PgxDriver) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

func (d *PgxDriver) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return &pgxRow{row: d.pool.QueryRow(ctx, sql, args...)}
}

func (d *PgxDriver) BeginTx(ctx context.Context, opts TxOptions) (TxDriver, error) {
	pgxOpts := pgx.TxOptions{IsoLevel: toPgxIsolation(opts.Isolation), AccessMode: pgx.ReadWrite}
	if opts.ReadOnly {
		pgxOpts.AccessMode = pgx.ReadOnly
	}
	tx, err := d.pool.BeginTx(ctx, pgxOpts)
	if err != nil {
		return nil, err
	}
	return &pgxTx{tx: tx, pool: d.pool}, nil
}

func (d *PgxDriver) Close() error {
	if d.pool == nil {
		return nil
	}
	d.pool.Close()
	return nil
}

// Pool exposes the underlying *pgxpool.Pool for callers using the raw
// escape hatch (record.RawQuery, COPY FROM). This is the only breach in
// the abstraction — it is opt-in and documented (DATA-019).
func (d *PgxDriver) Pool() *pgxpool.Pool { return d.pool }

// pgxRows wraps pgx.Rows behind record.Rows.
type pgxRows struct {
	rows pgx.Rows
}

func (r *pgxRows) Next() bool { return r.rows.Next() }
func (r *pgxRows) Columns() ([]string, error) {
	fds := r.rows.FieldDescriptions()
	names := make([]string, len(fds))
	for i, fd := range fds {
		names[i] = fd.Name
	}
	return names, nil
}
func (r *pgxRows) Err() error             { return r.rows.Err() }
func (r *pgxRows) Close() error           { r.rows.Close(); return nil }
func (r *pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }

// pgxRow wraps pgx.Row. Scan returns ErrNoRows when the row is absent —
// callers should use errors.Is(err, ErrNoRows).
type pgxRow struct {
	row pgx.Row
}

func (r *pgxRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRows
	}
	return err
}

// ErrNoRows is the sentinel returned when QueryRow finds nothing.
var ErrNoRows = errors.New("record: no rows in result set")

// pgxTx wraps pgx.Tx.
type pgxTx struct {
	tx   pgx.Tx
	pool *pgxpool.Pool
}

func (t *pgxTx) Dialect() Dialect { return DialectPostgres }
func (t *pgxTx) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	tag, err := t.tx.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxResult{tag: tag}, nil
}
func (t *pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}
func (t *pgxTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return &pgxRow{row: t.tx.QueryRow(ctx, sql, args...)}
}
func (t *pgxTx) BeginTx(ctx context.Context, opts TxOptions) (TxDriver, error) {
	// nested via savepoints
	return t, nil
}
func (t *pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
func (t *pgxTx) Close() error                       { return nil }

func (t *pgxTx) Savepoint(ctx context.Context, name string) (Savepoint, error) {
	_, err := t.tx.Exec(ctx, "SAVEPOINT "+name)
	if err != nil {
		return nil, err
	}
	return &pgxSavepoint{tx: t.tx, name: name}, nil
}

type pgxSavepoint struct {
	tx   pgx.Tx
	name string
}

func (s *pgxSavepoint) Release(ctx context.Context) error {
	_, err := s.tx.Exec(ctx, "RELEASE SAVEPOINT "+s.name)
	return err
}
func (s *pgxSavepoint) Rollback(ctx context.Context) error {
	_, err := s.tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+s.name)
	return err
}

type pgxResult struct {
	tag pgconn.CommandTag
}

func (r pgxResult) LastInsertId() (int64, error) {
	return 0, errors.New("record: pgx does not expose LastInsertId; use RETURNING id")
}
func (r pgxResult) RowsAffected() (int64, error) { return r.tag.RowsAffected(), nil }

func toPgxIsolation(l IsolationLevel) pgx.TxIsoLevel {
	switch l {
	case LevelReadUncommitted:
		return pgx.ReadUncommitted
	case LevelReadCommitted:
		return pgx.ReadCommitted
	case LevelRepeatableRead:
		return pgx.RepeatableRead
	case LevelSerializable:
		return pgx.Serializable
	default:
		return pgx.ReadCommitted
	}
}
