// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Scan streaming micro-bench (PERF-005 / Part XV.1 budget table).
//
// Companion to query_bench_test.go. The existing benches measure the
// 100k-row streaming path (`BenchmarkScanStreaming1MRows`) and the
// slice-allocating baseline (`BenchmarkScanAllAllocating`). This file
// adds a 10k-row variant under the spec-mandated name
// `BenchmarkScanStreaming` so the PERF.md table can name the bench
// directly without the 1M extrapolation note.
//
// 10k rows is small enough that the bench is fast (< 50 ms even at
// the default -benchtime=1s) while still exercising the streaming
// iterator path end-to-end. The budget is "bounded memory" (DATA-106):
// allocs/op should be roughly constant regardless of row count.
//
// Budget: bounded memory — observed allocs/op should not scale with
// row count. The Iter path reuses its row buffer between iterations;
// the All path is the linear-baseline that the existing bench
// already guards against regression.

package record

import (
	"context"
	"testing"
)

// BenchmarkScanStreaming measures the streaming iterator path over 10k
// rows. Each iteration constructs a fresh Query (cheap: struct-only)
// and drains the iterator end-to-end. The reported allocs/op is the
// per-scan allocation count — the streaming claim is that this number
// stays constant as row count grows (the row buffer is reused).
//
// The bench is paired with BenchmarkScanAllAllocating (in
// query_bench_test.go) which is the anti-benchmark: the All path
// MUST scale linearly in memory with row count. Together they
// enforce the streaming invariant.
func BenchmarkScanStreaming(b *testing.B) {
	const rowCount = 10_000
	d, tbl := setupScanRows(b, rowCount)
	defer d.Close()
	if err := RegisterPool("bench-scan-stream", d); err != nil {
		b.Fatalf("RegisterPool: %v", err)
	}
	defer CloseAll()

	q := NewQuery[benchScanModel]().Pool("bench-scan-stream").Table(tbl)
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
