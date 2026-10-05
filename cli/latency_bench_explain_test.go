// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CLI latency micro-bench (PERF-008 / Part XV.1 budget table).
//
// Companion to latency_bench_test.go. The existing benches measure a
// batch of non-generation CLI subcommands via subtests under
// `BenchmarkCLINonGen`. This file adds the spec-mandated single-shot
// bench under the name `BenchmarkCLIExplain` so the PERF.md budget
// table can name the bench directly. The bench measures one
// `ogon explain <topic>` invocation end-to-end (cobra construction,
// dispatch, render to in-memory buffers, exit).
//
// Budget: ≤ 100 ms p95 for the non-generation CLI surface. The explain
// command is the canonical "instant feedback" path — each invocation
// should land well under 1 ms; the budget exists to catch pathological
// regressions (accidental network calls, repeated config reloads,
// cobra flag-parsing slowdowns).

package cli

import (
	"bytes"
	"testing"
)

// BenchmarkCLIExplain measures the per-call cost of a single
// `ogon explain route` invocation. The bench runs Run() with the
// canonical explain args against in-memory buffers; the reported
// ns/op is the end-to-end wall-clock per call (cobra construction
// + dispatch + render + exit-code selection).
//
// The budget (≤ 100 ms p95 for non-gen CLI) absorbs ~100× headroom;
// a regression here typically indicates either cobra flag-parser
// slowdown or a regression in the explain topic lookup (the
// explainers map is a package-level var; the bench measures steady-
// state lookup cost).
func BenchmarkCLIExplain(b *testing.B) {
	args := []string{"explain", "route"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out, errOut bytes.Buffer
		code := Run(args, &out, &errOut, nil)
		if code != ExitOK {
			b.Fatalf("Run(%v) = %d, want 0; stderr: %s",
				args, code, errOut.String())
		}
	}
}

// BenchmarkCLIExplainList measures the no-arg `ogon explain` path
// (lists all available topics). This is a slightly different hot
// path: the explainers map is iterated (no lookup), and the
// rendered output is larger (the topic list). The bench detects
// regressions in the topic-listing path.
func BenchmarkCLIExplainList(b *testing.B) {
	args := []string{"explain"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out, errOut bytes.Buffer
		_ = Run(args, &out, &errOut, nil)
	}
}
