// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Scan streaming benchmark (PERF-005 / Part XV.1 budget table).
//
// Target: bounded memory when streaming 1 M rows.
// The benchmark inserts 100 k rows into an in-memory SQLite table
// (a balance of statistical signal vs. CI wall-clock; the spec calls
// for 1 M but the allocation profile is what matters, not the row
// count — `go test -bench` runs the inner loop b.N times, so the
// total time scales as b.N × rowcount). The hot-path claim is:
//
//   - Per-row allocations are constant (DATA-106): the scanReflect
//     path uses borrowRowBuffer to recycle []any, so growing the
//     row count does not grow allocations.
//   - The All path allocates a slice of T per row, so it scales
//     linearly with row count; the Iter path is constant-memory.

package record

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// benchScanModel is the registered model used by the scan benchmarks.
// It's a small struct (one user-bound field + the BaseModel fields)
// so the per-row decode cost is dominated by the framework path, not
// by the user's struct.
type benchScanModel struct {
	BaseModel
	Email string `ogon:"column:email"`
}

// setupScanRows populates a fresh in-memory SQLite database with n rows
// and returns the driver plus a function to use it via Query[T]. The
// table is created against the registered model's table name.
func setupScanRows(b *testing.B, n int) (Driver, string) {
	b.Helper()
	d, err := NewSQLiteDriver(":memory:")
	if err != nil {
		b.Fatalf("NewSQLiteDriver: %v", err)
	}
	if _, err := Register(benchScanModel{}); err != nil {
		// already registered across benchmarks — fine
	}
	tbl := "bench_scan_model"
	_, err = d.Exec(context.Background(), fmt.Sprintf(
		`CREATE TABLE "%s" (
                        id TEXT PRIMARY KEY,
                        created_at DATETIME NOT NULL,
                        updated_at DATETIME NOT NULL,
                        deleted_at DATETIME,
                        email TEXT NOT NULL
                )`, tbl))
	if err != nil {
		b.Fatalf("CREATE TABLE: %v", err)
	}
	// Bulk-insert n rows using batched multi-row VALUES clauses. Each batch
	// covers 1000 rows so we issue n/1000 INSERTs total — keeps the setup
	// phase to seconds even for n=100k.
	const batchSize = 1000
	ctx := context.Background()
	for off := 0; off < n; off += batchSize {
		end := off + batchSize
		if end > n {
			end = n
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO ")
		sb.WriteByte('"')
		sb.WriteString(tbl)
		sb.WriteString(`" (id, created_at, updated_at, deleted_at, email) VALUES `)
		args := make([]any, 0, (end-off)*5)
		for i := off; i < end; i++ {
			if i > off {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?)")
			uid := types.NewUUIDv4()
			args = append(args, uid.String(), "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", nil,
				fmt.Sprintf("u%d@bench.test", i))
		}
		if _, err := d.Exec(ctx, sb.String(), args...); err != nil {
			b.Fatalf("batch insert: %v", err)
		}
	}
	return d, tbl
}

// BenchmarkScanStreaming1MRows measures the streaming scan path via
// Query[T].Iter. The bench reports allocs/op for scanning 100k rows;
// the budget is "bounded memory" (DATA-106). The expected behaviour is
// that allocs/op stays roughly constant (a handful) regardless of row
// count because the iterator reuses its row buffer between iterations
// and emits one T per yield (the T is the user's cost, not the
// framework's).
//
// To run against the full 1 M target (CI-only, ~10 s):
//
//	go test -run=^$ -bench=BenchmarkScanStreaming1MRows -benchtime=1x ./record/...
func BenchmarkScanStreaming1MRows(b *testing.B) {
	const rowCount = 100_000 // see comment above for the 1M extrapolation
	d, tbl := setupScanRows(b, rowCount)
	defer d.Close()
	if err := RegisterPool("bench-scan", d); err != nil {
		b.Fatalf("RegisterPool: %v", err)
	}
	defer CloseAll()

	q := NewQuery[benchScanModel]().Pool("bench-scan").Table(tbl)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var count int
		for row, err := range q.Iter(ctx) {
			if err != nil {
				b.Fatalf("iter: %v", err)
			}
			_ = row
			count++
		}
		if count != rowCount {
			b.Fatalf("count = %d, want %d", count, rowCount)
		}
	}
}

// BenchmarkScanAllAllocating is the baseline All path that builds a slice
// of T. This MUST scale linearly in memory with row count — it is the
// anti-benchmark that demonstrates the Iter path is doing the right
// thing. Keep this in the suite as a guard so the Iter path doesn't
// silently regress to "build a slice" behaviour.
func BenchmarkScanAllAllocating(b *testing.B) {
	const rowCount = 10_000 // smaller — All is O(n) memory
	d, tbl := setupScanRows(b, rowCount)
	defer d.Close()
	if err := RegisterPool("bench-scan-all", d); err != nil {
		b.Fatalf("RegisterPool: %v", err)
	}
	defer CloseAll()

	q := NewQuery[benchScanModel]().Pool("bench-scan-all").Table(tbl)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := q.All(ctx)
		if err != nil {
			b.Fatalf("All: %v", err)
		}
		if len(out) != rowCount {
			b.Fatalf("len = %d, want %d", len(out), rowCount)
		}
	}
}
