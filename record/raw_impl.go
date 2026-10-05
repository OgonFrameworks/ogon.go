// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Driver-specific CopyFrom helpers. Kept in a separate file so the raw.go
// surface stays engine-agnostic.

package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// copyFromPgx uses pgx.CopyFrom for native binary bulk load.
func copyFromPgx(ctx context.Context, cfg CopyFromConfig, rows [][]any) (int64, error) {
	d := PoolOf(ctx, cfg.Pool)
	pg, ok := d.(*PgxDriver)
	if !ok {
		return 0, fmt.Errorf("record: pool %s is not a *PgxDriver", cfg.Pool)
	}
	// translate [][]any → pgx.CopyFromSource
	src := &pgxCopySource{rows: rows, idx: 0}
	n, err := pg.pool.CopyFrom(ctx, pgx.Identifier{cfg.Table}, cfg.Columns, src)
	if err != nil {
		return 0, fmt.Errorf("record: pgx CopyFrom: %w", err)
	}
	return n, nil
}

type pgxCopySource struct {
	rows [][]any
	idx  int
}

func (s *pgxCopySource) Next() bool             { s.idx++; return s.idx-1 < len(s.rows) }
func (s *pgxCopySource) Values() ([]any, error) { return s.rows[s.idx-1], nil }
func (s *pgxCopySource) Err() error             { return nil }

// copyFromSQLite falls back to prepared-INSERT in transactional batches.
func copyFromSQLite(ctx context.Context, cfg CopyFromConfig, rows [][]any) (int64, error) {
	d := PoolOf(ctx, cfg.Pool)
	if d == nil {
		return 0, fmt.Errorf("record: pool %s missing", cfg.Pool)
	}
	placeholders := make([]string, len(cfg.Columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	sqlStr := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)",
		cfg.Table,
		quoteIdentList(cfg.Columns),
		strings.Join(placeholders, ", "),
	)

	var total int64
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 1000
	}
	err := Transaction(ctx, cfg.Pool, func(tx *Tx) error {
		for i, r := range rows {
			if _, err := tx.Exec(ctx, sqlStr, r...); err != nil {
				return fmt.Errorf("record: sqlite copy insert row %d: %w", i, err)
			}
			total++
			if int(total)%batch == 0 {
				// let the surrounding tx keep going — modernc.org/sqlite
				// uses a single writer, so batching beyond the tx boundary
				// adds no throughput.
			}
		}
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, nil
}

// quoteIdentList renders a comma-separated, quoted column list.
func quoteIdentList(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
	}
	return strings.Join(out, ", ")
}

// compile-time: silence unused import when pgx CopyFrom signature drifts
var _ = time.Second
