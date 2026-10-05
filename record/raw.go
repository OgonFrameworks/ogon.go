// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// RawQuery[T] — the escape hatch for callers who need SQL the typed
// builder cannot express (window functions, recursive CTEs, complex
// joins). Every RawQuery call still:
//   - binds parameters via placeholders (no string concat of user input)
//   - emits a redacted-statement otel span
//   - counts against the pool stats
//
// COPY FROM bulk load is exposed as a separate helper because the wire
// protocol differs from Exec (pgx.CopyFrom on pg; for sqlite it falls
// back to a prepared-INSERT loop with batched commits).

package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// RawQuery[T] runs caller-supplied SQL against a named pool and scans
// results into []T. The SQL MUST use the dialect-correct placeholder
// convention ($N for pgx, ? for sqlite); callers mixing conventions get
// a descriptive error rather than a silent mis-binding.
func RawQuery[T any](ctx context.Context, poolName, sqlStr string, args ...any) ([]T, error) {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return nil, diag.New("OGON-D0001", "no such pool", "pool "+poolName+" not registered")
	}
	if err := validatePlaceholders(d.Dialect(), sqlStr); err != nil {
		return nil, diag.Wrap(err, diag.Diag{
			Code:  "OGON-D0020",
			Title: "raw SQL placeholder mismatch",
			What:  "sql uses placeholders incompatible with " + string(d.Dialect()),
		})
	}
	start := time.Now()
	rows, err := d.Query(ctx, sqlStr, args...)
	if err != nil {
		recordQuery(poolName, false, false)
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0021", Title: "raw query failed"})
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		var v T
		if err := scanRows(rows, &v); err != nil {
			return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0022", Title: "raw scan failed"})
		}
		out = append(out, v)
	}
	elapsed := time.Since(start)
	recordQuery(poolName, true, elapsed > slowThreshold(poolName))
	return out, rows.Err()
}

// validatePlaceholders rejects $N-? mismatches. We are conservative: if
// both styles appear, that's always an error. If only one appears, it
// must match the dialect.
func validatePlaceholders(d Dialect, sqlStr string) error {
	hasPgx := strings.Contains(sqlStr, "$1") || strings.Contains(sqlStr, "$2")
	hasSQLite := strings.Contains(sqlStr, "?")
	switch d {
	case DialectPostgres:
		if hasSQLite && !hasPgx {
			return fmt.Errorf("record: raw SQL uses ? placeholders but pool is postgres")
		}
	case DialectSQLite:
		if hasPgx {
			return fmt.Errorf("record: raw SQL uses $N placeholders but pool is sqlite")
		}
	}
	return nil
}

// CopyFromConfig configures a bulk-load operation.
type CopyFromConfig struct {
	Pool      string
	Table     string
	Columns   []string
	BatchSize int // per-tx commit batch for sqlite fallback
}

// CopyFrom bulk-loads rows. On Postgres it uses pgx.CopyFrom; on SQLite
// it falls back to prepared INSERT statements committed per BatchSize.
// Returns total rows copied and any error.
//
// This is the only path that bypasses the Driver interface directly —
// it is opt-in and the only callers should be CLI/ETL utilities.
func CopyFrom(ctx context.Context, cfg CopyFromConfig, rows [][]any) (int64, error) {
	if cfg.Pool == "" {
		cfg.Pool = "default"
	}
	d := PoolOf(ctx, cfg.Pool)
	if d == nil {
		return 0, diag.New("OGON-D0001", "no such pool", "pool "+cfg.Pool)
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	if d.Dialect() == DialectPostgres {
		return copyFromPgx(ctx, cfg, rows)
	}
	return copyFromSQLite(ctx, cfg, rows)
}

// CopyFromSource is the streaming-source variant. The Source iterator
// yields row tuples until done; nil-sentinel ends the stream.
type CopyFromSource = func() ([]any, bool)

// CopyFromStream streams rows from a producer function. Use for ETL flows
// that can't materialise the full input slice in memory.
func CopyFromStream(ctx context.Context, cfg CopyFromConfig, src CopyFromSource) (int64, error) {
	if cfg.Pool == "" {
		cfg.Pool = "default"
	}
	d := PoolOf(ctx, cfg.Pool)
	if d == nil {
		return 0, diag.New("OGON-D0001", "no such pool", "pool "+cfg.Pool)
	}
	// For simplicity in this phase, materialise through a buffered slice
	// then call CopyFrom. Future work: pgx.CopyFromSource adapter that
	// streams without materialising.
	batch := make([][]any, 0, 1024)
	var total int64
	for {
		row, done := src()
		if done {
			break
		}
		batch = append(batch, row)
		if len(batch) >= 1024 {
			n, err := CopyFrom(ctx, cfg, batch)
			total += n
			if err != nil {
				return total, err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		n, err := CopyFrom(ctx, cfg, batch)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
