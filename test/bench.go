// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Bench wrappers (TEST-020/021/022). Three layers:
//   1. Std wrappers — small helpers around testing.B so bench tests are
//      short and consistent.
//   2. DX bench suite — ergonomic micro-benches for app-developer paths
//      (handler dispatch, JSON encode, etc.).
//   3. Perf bench suite — gate-able, regression-graded benchmarks used by
//      `ogon benchmark perf` (PERF-009/010).

package test

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// BenchCase describes a single benchmark function. Tests register these so
// the DX/perf suites can be enumerated by `ogon benchmark dx|perf`.
type BenchCase struct {
	Name     string
	Category string // "dx" or "perf"
	Run      func(b *testing.B)
}

var (
	benchMu    sync.RWMutex
	benchCases = map[string]BenchCase{}
)

// RegisterBench installs a bench case. Safe to call from init() in any
// _test.go file. Duplicate Name silently overwrites — last-writer wins.
func RegisterBench(c BenchCase) {
	benchMu.Lock()
	defer benchMu.Unlock()
	if c.Category == "" {
		c.Category = "dx"
	}
	benchCases[c.Name] = c
}

// ListBenches returns all registered cases sorted by Name.
func ListBenches() []BenchCase {
	benchMu.RLock()
	defer benchMu.RUnlock()
	out := make([]BenchCase, 0, len(benchCases))
	for _, c := range benchCases {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListDXBenches returns only the cases registered under the "dx" category.
func ListDXBenches() []BenchCase {
	benchMu.RLock()
	defer benchMu.RUnlock()
	var out []BenchCase
	for _, c := range benchCases {
		if c.Category == "dx" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListPerfBenches returns only the cases registered under the "perf" category.
func ListPerfBenches() []BenchCase {
	benchMu.RLock()
	defer benchMu.RUnlock()
	var out []BenchCase
	for _, c := range benchCases {
		if c.Category == "perf" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BenchMemory reports the heap-allocated bytes and allocs per op for fn.
// Wraps b.ReportMetric so the values appear in the bench output. This is
// the wrapper used by the perf suite (TEST-022).
func BenchMemory(b *testing.B, fn func(b *testing.B)) {
	b.Helper()
	b.ReportAllocs()
	fn(b)
}

// BenchTimeBudget runs fn for at most the supplied duration. Returns the
// actual measured ns/op. Use for smoke tests that need a number quickly.
func BenchTimeBudget(b *testing.B, budget time.Duration, fn func(b *testing.B)) time.Duration {
	b.Helper()
	start := time.Now()
	b.Run(fmt.Sprintf("budget-%s", budget), func(b *testing.B) {
		fn(b)
	})
	return time.Since(start)
}

// BenchReport writes a JSON-ish record to b's output. The receiver is
// *testing.B so the report lands in the bench log.
func BenchReport(b *testing.B, name string, fields map[string]any) {
	b.Helper()
	var s string
	for k, v := range fields {
		s += fmt.Sprintf("%s=%v ", k, v)
	}
	b.ReportMetric(0, name+"_meta") // placeholder so the name is in the report
	b.Log(fmt.Sprintf("%s: %s", name, s))
}

// RunDXSuite runs every DX bench case as a subtest of b.
func RunDXSuite(b *testing.B) {
	b.Helper()
	for _, c := range ListDXBenches() {
		c := c
		b.Run(c.Name, func(b *testing.B) {
			c.Run(b)
		})
	}
}

// RunPerfSuite runs every perf bench case as a subtest of b.
func RunPerfSuite(b *testing.B) {
	b.Helper()
	for _, c := range ListPerfBenches() {
		c := c
		b.Run(c.Name, func(b *testing.B) {
			c.Run(b)
		})
	}
}

// MustBench is the panic-on-misuse variant of RegisterBench. Use when the
// caller cannot recover from a misconfiguration.
func MustBench(c BenchCase) {
	if c.Name == "" {
		panic("ogontest: RegisterBench: empty name")
	}
	if c.Run == nil {
		panic("ogontest: RegisterBench: nil Run")
	}
	RegisterBench(c)
}
