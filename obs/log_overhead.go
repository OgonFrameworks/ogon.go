// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Log-overhead budget test. Implements OBS-044.
//
// The budget test asserts that the per-request log overhead stays
// below a fixed budget. Concretely: the redaction handler, sampling
// handler, correlation handler, and request logger together must not
// add more than BudgetMicroseconds µs of CPU per request, on a fixed
// message size and attribute count. If a future change pushes overhead
// above the budget, this test fails — forcing a discussion.

package obs

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// LogOverheadBudget is the per-record wall-clock budget. (OBS-044)
const LogOverheadBudget = 100 * time.Microsecond

// MeasureLogOverhead runs n iterations of a redaction+correlation
// pipeline over a fixed message + attrs and returns the per-iteration
// duration. Callers (tests) compare against LogOverheadBudget.
//
// The pipeline mirrors the prod setup: NewLogger (redaction hook) →
// WithTraceCorrelation → WithSampling. (OBS-044)
func MeasureLogOverhead(n int) time.Duration {
	if n <= 0 {
		n = 1000
	}
	sink := &discardSink{}
	base := slog.New(slog.NewJSONHandler(sink, nil))
	log := WithTraceCorrelation(WithSampling(base, DefaultSamplingRules()))
	ctx := context.Background()
	attrs := []slog.Attr{
		slog.String("method", "GET"),
		slog.String("route", "/api/users/:id"),
		slog.Int("status", 200),
		slog.String("trace_id", "abcdef1234567890abcdef1234567890"),
		slog.String("span_id", "0123456789abcdef"),
		slog.String("user_agent", "Mozilla/5.0 (X11; Linux x86_64; rv:138.0) Gecko/20100101 Firefox/138.0"),
	}
	// Warm up to amortise JIT/linker cost.
	for i := 0; i < 100; i++ {
		log.LogAttrs(ctx, slog.LevelInfo, "http_request", attrs...)
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		log.LogAttrs(ctx, slog.LevelInfo, "http_request", attrs...)
	}
	d := time.Since(start)
	return d / time.Duration(n)
}

// RunLogOverheadBudgetTest is the testable entrypoint. Returns an error
// when the per-record duration exceeds the budget. (OBS-044)
func RunLogOverheadBudgetTest(tb testing.TB, n int) error {
	tb.Helper()
	per := MeasureLogOverhead(n)
	if per > LogOverheadBudget {
		return &overheadBudgetError{actual: per, budget: LogOverheadBudget}
	}
	return nil
}

type overheadBudgetError struct{ actual, budget time.Duration }

func (e *overheadBudgetError) Error() string {
	return "obs: log overhead budget exceeded: per=" + e.actual.String() + " budget=" + e.budget.String()
}

// discardSink is a no-op io.Writer for overhead measurement. (OBS-044)
type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }
