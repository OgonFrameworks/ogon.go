// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Router match micro-bench (PERF-001 / Part XV.1 budget table).
//
// This file is the single-route baseline companion to the 10k-route bench
// in router_bench_test.go. The 1-route case isolates the pure dispatch
// cost when the trie has only one node — useful to bound the floor of
// the match path and detect regressions in the lookup header (method map
// index, root-node walk, params-map allocation when there are no params).
//
// Target: < 200 ns/op, 0 allocs/op (no params map on a static route).

package http

import "testing"

// BenchmarkRouterMatch1Route is the single-route baseline. The trie
// contains exactly one static GET route; the bench loop matches that
// route b.N times. The reported ns/op is the floor of the match path
// (method index + root visit + MatchOK return). The 10k-route budget
// (< 1 µs) subsumes this baseline; the 1-route number is reported for
// regression triage: a > 2× slowdown here points at the method-map
// lookup or the root-node bookkeeping, not the trie walk itself.
func BenchmarkRouterMatch1Route(b *testing.B) {
	r := NewRouter()
	_ = r.MapGet("/healthz", func(c *Ctx) error { return c.NoContent(200) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, outcome, _ := r.Match("GET", "/healthz")
		if outcome != MatchOK {
			b.Fatalf("match failed: %v", outcome)
		}
	}
}
